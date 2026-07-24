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

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

type fakeSessions struct {
	mu      sync.Mutex
	states  map[string]sshmanager.Snapshot
	options sshmanager.ConnectOptions
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
	app, err := NewApp(configPath, sessions)
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
	app, err := NewApp(configPath, newFakeSessions())
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
