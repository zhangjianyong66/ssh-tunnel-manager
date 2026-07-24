package web

import (
	"context"
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
)

type fakeSessions struct {
	mu      sync.Mutex
	states  map[string]sshmanager.Snapshot
	options sshmanager.ConnectOptions
}

type fakePorts struct {
	mu        sync.Mutex
	snapshots map[string]portdiscovery.Snapshot
	refreshes int
	err       error
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

func TestAppHostsAndConnectDoNotEchoSecret(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessions()
	app, err := NewApp(configPath, sessions, newFakePorts())
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
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts())
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
	app, err := NewApp(configPath, sessions, ports)
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

func TestAppPortRefreshRequiresConnection(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("Host server-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(configPath, newFakeSessions(), newFakePorts())
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
			app, err := NewApp(configPath, sessions, ports)
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
