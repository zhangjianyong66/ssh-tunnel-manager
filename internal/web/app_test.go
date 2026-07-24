package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/portdiscovery"
	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/tunnel"
)

type fakeSessions struct {
	mu           sync.Mutex
	states       map[string]sshmanager.Snapshot
	options      sshmanager.ConnectOptions
	onDisconnect func(string)
}

type fakePorts struct {
	mu               sync.Mutex
	snapshots        map[string]portdiscovery.Snapshot
	refreshes        int
	err              error
	onSetAutoRefresh func(string, bool)
}

type fakeTunnels struct {
	mu             sync.Mutex
	snapshots      []tunnel.Snapshot
	createSnapshot tunnel.Snapshot
	createErr      error
	stopErr        error
	stopHostErr    error
	createCalls    int
	stopIDs        []string
	stoppedHosts   []string
	onStopHost     func(string)
	logs           map[string][]tunnel.LogEntry
}

func newFakeTunnels() *fakeTunnels {
	return &fakeTunnels{snapshots: []tunnel.Snapshot{}, logs: make(map[string][]tunnel.LogEntry)}
}

func (f *fakeTunnels) Create(_ context.Context, host string, remotePort uint16) (tunnel.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return f.createSnapshot, f.createErr
	}
	if f.createSnapshot.ID == "" {
		f.createSnapshot = tunnel.Snapshot{
			ID:         "tunnel-id",
			Host:       host,
			RemotePort: remotePort,
			LocalPort:  remotePort,
			Address:    fmt.Sprintf("127.0.0.1:%d", remotePort),
			Status:     tunnel.StatusRunning,
		}
		f.snapshots = []tunnel.Snapshot{f.createSnapshot}
	}
	return f.createSnapshot, nil
}

func (f *fakeTunnels) List() []tunnel.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tunnel.Snapshot(nil), f.snapshots...)
}

func (f *fakeTunnels) Logs(id string) ([]tunnel.LogEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	logs, ok := f.logs[id]
	return append([]tunnel.LogEntry(nil), logs...), ok
}

func (f *fakeTunnels) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopIDs = append(f.stopIDs, id)
	return f.stopErr
}

func (f *fakeTunnels) StopHost(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stoppedHosts = append(f.stoppedHosts, host)
	if f.onStopHost != nil {
		f.onStopHost(host)
	}
	return f.stopHostErr
}

func newFakePorts() *fakePorts {
	return &fakePorts{snapshots: make(map[string]portdiscovery.Snapshot)}
}

func (f *fakePorts) Snapshot(host string) portdiscovery.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if snapshot, ok := f.snapshots[host]; ok {
		return snapshot
	}
	return portdiscovery.Snapshot{Host: host, Ports: []portdiscovery.Port{}}
}

func (f *fakePorts) Refresh(_ context.Context, host string) (portdiscovery.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if f.err != nil {
		return f.snapshots[host], f.err
	}
	snapshot := f.snapshots[host]
	snapshot.Host = host
	snapshot.Ports = []portdiscovery.Port{{Number: 8080, Process: "node"}}
	f.snapshots[host] = snapshot
	return snapshot, nil
}

func (f *fakePorts) SetAutoRefresh(host string, enabled bool) (portdiscovery.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onSetAutoRefresh != nil {
		f.onSetAutoRefresh(host, enabled)
	}
	if f.err != nil {
		return f.snapshots[host], f.err
	}
	snapshot := f.snapshots[host]
	snapshot.Host = host
	snapshot.AutoRefresh = enabled
	f.snapshots[host] = snapshot
	return snapshot, nil
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{states: make(map[string]sshmanager.Snapshot)}
}

func (f *fakeSessions) Connect(_ context.Context, host string, options sshmanager.ConnectOptions) (sshmanager.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.options = options
	f.states[host] = sshmanager.Snapshot{Host: host, Status: sshmanager.StatusConnected}
	return f.states[host], nil
}

func (f *fakeSessions) Disconnect(_ context.Context, host string) (sshmanager.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onDisconnect != nil {
		f.onDisconnect(host)
	}
	f.states[host] = sshmanager.Snapshot{Host: host, Status: sshmanager.StatusDisconnected}
	return f.states[host], nil
}

func (f *fakeSessions) Snapshot(host string) sshmanager.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state, ok := f.states[host]; ok {
		return state
	}
	return sshmanager.Snapshot{Host: host, Status: sshmanager.StatusDisconnected}
}

type fakePreferences struct {
	mu      sync.Mutex
	values  map[string]bool
	setErr  error
	readErr error
}

func newFakePreferences() *fakePreferences {
	return &fakePreferences{values: make(map[string]bool)}
}

func (f *fakePreferences) AutoRefresh(host string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[host], f.readErr
}

func (f *fakePreferences) SetAutoRefresh(host string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.values[host] = enabled
	return nil
}

func TestAppHostsAndConnectDoNotEchoSecret(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	app, err := NewApp(configPath, sessions, newFakePorts(), newFakeTunnels())
	if err != nil {
		t.Fatal(err)
	}
	hostsRequest := httptest.NewRequest(http.MethodGet, "/api/ssh-hosts", nil)
	hostsResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(hostsResponse, hostsRequest)
	if hostsResponse.Code != http.StatusOK || !strings.Contains(hostsResponse.Body.String(), "server-a") {
		t.Fatalf("hosts response = %d %s", hostsResponse.Code, hostsResponse.Body.String())
	}

	secret := "http-secret-value"
	body := fmt.Sprintf(`{"password":%q,"savePassword":false}`, secret)
	connectRequest := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/connect", strings.NewReader(body))
	connectRequest.Header.Set("Content-Type", "application/json")
	connectResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(connectResponse, connectRequest)
	if connectResponse.Code != http.StatusOK {
		t.Fatalf("connect response = %d %s", connectResponse.Code, connectResponse.Body.String())
	}
	if strings.Contains(connectResponse.Body.String(), secret) {
		t.Fatal("HTTP response leaked secret")
	}
	if sessions.options.Password != secret {
		t.Fatal("connection options did not receive password")
	}
}

func TestAppRejectsUnknownHostAndUnknownRequestField(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts(), newFakeTunnels())
	if err != nil {
		t.Fatal(err)
	}

	unknown := httptest.NewRequest(http.MethodPost, "/api/servers/server-b/connect", strings.NewReader("{}"))
	unknownResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown host status = %d", unknownResponse.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/connect", strings.NewReader(`{"secret":"value"}`))
	invalidResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status = %d", invalidResponse.Code)
	}
}

func TestAppPortDiscoveryRoutes(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
	ports := newFakePorts()
	app, err := NewApp(configPath, sessions, ports, newFakeTunnels())
	if err != nil {
		t.Fatal(err)
	}
	refresh := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/ports/refresh", nil)
	refreshResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusOK || !strings.Contains(refreshResponse.Body.String(), "8080") {
		t.Fatalf("refresh = %d %s", refreshResponse.Code, refreshResponse.Body.String())
	}
	auto := httptest.NewRequest(http.MethodPut, "/api/servers/server-a/ports/auto-refresh", strings.NewReader(`{"enabled":true}`))
	autoResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(autoResponse, auto)
	if autoResponse.Code != http.StatusOK || !strings.Contains(autoResponse.Body.String(), `"autoRefresh":true`) {
		t.Fatalf("auto = %d %s", autoResponse.Code, autoResponse.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/servers/server-a/ports/auto-refresh", strings.NewReader(`{"unexpected":true}`))
	invalidResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid request = %d", invalidResponse.Code)
	}
	missing := httptest.NewRequest(http.MethodPut, "/api/servers/server-a/ports/auto-refresh", strings.NewReader(`{}`))
	missingResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled = %d", missingResponse.Code)
	}
}

func TestAppPersistsAndRestoresAutoRefreshAfterManualConnect(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	ports := newFakePorts()
	preferences := newFakePreferences()
	preferences.values["server-a"] = true
	app, err := NewApp(configPath, sessions, ports, newFakeTunnels(), preferences)
	if err != nil {
		t.Fatal(err)
	}
	if ports.Snapshot("server-a").AutoRefresh {
		t.Fatal("preference triggered refresh before manual connection")
	}
	connect := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/connect", strings.NewReader(`{}`))
	connectResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(connectResponse, connect)
	if connectResponse.Code != http.StatusOK || !ports.Snapshot("server-a").AutoRefresh {
		t.Fatalf("connect = %d %s, ports = %#v", connectResponse.Code, connectResponse.Body.String(), ports.Snapshot("server-a"))
	}

	auto := httptest.NewRequest(http.MethodPut, "/api/servers/server-a/ports/auto-refresh", strings.NewReader(`{"enabled":false}`))
	autoResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(autoResponse, auto)
	if autoResponse.Code != http.StatusOK || preferences.values["server-a"] || ports.Snapshot("server-a").AutoRefresh {
		t.Fatalf("disable = %d %s", autoResponse.Code, autoResponse.Body.String())
	}
}

func TestAppPreferenceWriteFailureDoesNotChangeRuntimeRefresh(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
	preferences := newFakePreferences()
	preferences.setErr = errors.New("disk unavailable")
	ports := newFakePorts()
	app, err := NewApp(configPath, sessions, ports, newFakeTunnels(), preferences)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/servers/server-a/ports/auto-refresh", strings.NewReader(`{"enabled":true}`))
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "preference_write_failed") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if ports.Snapshot("server-a").AutoRefresh {
		t.Fatal("runtime refresh changed after preference write failure")
	}
}

func TestAppPortRefreshRequiresConnection(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts(), newFakeTunnels())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/ports/refresh", nil)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "server_not_connected") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestAppMapsPortDiscoveryErrors(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", &portdiscovery.Error{Code: portdiscovery.ErrorTimeout, Message: "远程端口探测超时"}, http.StatusGatewayTimeout, "discovery_timeout"},
		{"failed", &portdiscovery.Error{Code: portdiscovery.ErrorFailed, Message: "internal detail"}, http.StatusBadGateway, "discovery_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := newFakeSessions()
			sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
			ports := newFakePorts()
			ports.err = test.err
			app, err := NewApp(configPath, sessions, ports, newFakeTunnels())
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/servers/server-a/ports/refresh", nil)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "internal detail") {
				t.Fatal("internal discovery detail leaked to HTTP response")
			}
		})
	}
}

func TestAppTunnelRoutes(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
	tunnels := newFakeTunnels()
	app, err := NewApp(configPath, sessions, newFakePorts(), tunnels)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/api/tunnels", strings.NewReader(`{"host":"server-a","remotePort":8080}`))
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"tunnel-id"`) || !strings.Contains(response.Body.String(), `"address":"127.0.0.1:8080"`) {
			t.Fatalf("create = %d %s", response.Code, response.Body.String())
		}
	}
	if tunnels.createCalls != 2 {
		t.Fatalf("create calls = %d", tunnels.createCalls)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	listResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"tunnels":[`) || !strings.Contains(listResponse.Body.String(), `"id":"tunnel-id"`) {
		t.Fatalf("list = %d %s", listResponse.Code, listResponse.Body.String())
	}

	stopRequest := httptest.NewRequest(http.MethodDelete, "/api/tunnels/tunnel-id", nil)
	stopResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(stopResponse, stopRequest)
	if stopResponse.Code != http.StatusNoContent || stopResponse.Body.Len() != 0 {
		t.Fatalf("stop = %d %s", stopResponse.Code, stopResponse.Body.String())
	}
	if len(tunnels.stopIDs) != 1 || tunnels.stopIDs[0] != "tunnel-id" {
		t.Fatalf("stop ids = %#v", tunnels.stopIDs)
	}
}

func TestAppTunnelLogsRoute(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tunnels := newFakeTunnels()
	tunnels.logs["tunnel-id"] = []tunnel.LogEntry{{Level: "info", Message: "SSH 隧道已运行"}}
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts(), tunnels)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tunnels/tunnel-id/logs", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"logs":[`) || !strings.Contains(response.Body.String(), "SSH 隧道已运行") {
		t.Fatalf("logs = %d %s", response.Code, response.Body.String())
	}
	missing := httptest.NewRecorder()
	app.Handler().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/tunnels/missing/logs", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "tunnel_not_found") {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
}

func TestAppTunnelListIsNeverNull(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts(), newFakeTunnels())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tunnels", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tunnels":[]`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestAppRejectsInvalidTunnelRequests(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
	tunnels := newFakeTunnels()
	app, err := NewApp(configPath, sessions, newFakePorts(), tunnels)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"missing host", `{"remotePort":8080}`},
		{"blank host", `{"host":" ","remotePort":8080}`},
		{"missing port", `{"host":"server-a"}`},
		{"null port", `{"host":"server-a","remotePort":null}`},
		{"zero port", `{"host":"server-a","remotePort":0}`},
		{"negative port", `{"host":"server-a","remotePort":-1}`},
		{"large port", `{"host":"server-a","remotePort":65536}`},
		{"fraction port", `{"host":"server-a","remotePort":80.5}`},
		{"unknown field", `{"host":"server-a","remotePort":8080,"localPort":8081}`},
		{"trailing value", `{"host":"server-a","remotePort":8080}{}`},
		{"oversized", `{"host":"server-a","remotePort":8080}` + strings.Repeat(" ", 4097)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/tunnels", strings.NewReader(test.body)))
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
	if tunnels.createCalls != 0 {
		t.Fatalf("invalid requests reached service: %d", tunnels.createCalls)
	}
}

func TestAppTunnelCreateRequiresKnownConnectedHost(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tunnels := newFakeTunnels()
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts(), tunnels)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"unknown", `{"host":"server-b","remotePort":8080}`, http.StatusNotFound, "host_not_found"},
		{"disconnected", `{"host":"server-a","remotePort":8080}`, http.StatusConflict, "server_not_connected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/tunnels", strings.NewReader(test.body)))
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
	if tunnels.createCalls != 0 {
		t.Fatalf("invalid hosts reached service: %d", tunnels.createCalls)
	}
}

func TestAppMapsTunnelErrorsWithoutLeakingDetails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", &tunnel.Error{Code: tunnel.ErrorInvalid, Message: "internal detail"}, http.StatusBadRequest, "invalid_tunnel"},
		{"disconnected", &tunnel.Error{Code: tunnel.ErrorServerNotConnected, Message: "internal detail"}, http.StatusConflict, "server_not_connected"},
		{"port unavailable", &tunnel.Error{Code: tunnel.ErrorLocalPortUnavailable, Message: "internal detail"}, http.StatusConflict, "local_port_unavailable"},
		{"timeout", &tunnel.Error{Code: tunnel.ErrorTimeout, Message: "internal detail"}, http.StatusGatewayTimeout, "tunnel_timeout"},
		{"cancelled", &tunnel.Error{Code: tunnel.ErrorCancelled, Message: "internal detail"}, http.StatusRequestTimeout, "tunnel_cancelled"},
		{"start failed", &tunnel.Error{Code: tunnel.ErrorStartFailed, Message: "internal detail"}, http.StatusBadGateway, "tunnel_start_failed"},
		{"closed", &tunnel.Error{Code: tunnel.ErrorServiceClosed, Message: "internal detail"}, http.StatusServiceUnavailable, "service_closed"},
		{"unknown", errors.New("internal detail"), http.StatusBadGateway, "tunnel_start_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := newFakeSessions()
			sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
			tunnels := newFakeTunnels()
			tunnels.createErr = test.err
			app, err := NewApp(configPath, sessions, newFakePorts(), tunnels)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/tunnels", strings.NewReader(`{"host":"server-a","remotePort":8080}`)))
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "internal detail") {
				t.Fatal("internal tunnel detail leaked to HTTP response")
			}
		})
	}
}

func TestAppDisconnectCleansHostInOrder(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var order []string
	tunnels := newFakeTunnels()
	tunnels.onStopHost = func(string) { order = append(order, "tunnels") }
	ports := newFakePorts()
	ports.onSetAutoRefresh = func(string, bool) { order = append(order, "discovery") }
	sessions := newFakeSessions()
	sessions.states["server-a"] = sshmanager.Snapshot{Host: "server-a", Status: sshmanager.StatusConnected}
	sessions.onDisconnect = func(string) { order = append(order, "ssh") }
	app, err := NewApp(configPath, sessions, ports, tunnels)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/servers/server-a/disconnect", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if got := strings.Join(order, ","); got != "tunnels,discovery,ssh" {
		t.Fatalf("cleanup order = %s", got)
	}
}
