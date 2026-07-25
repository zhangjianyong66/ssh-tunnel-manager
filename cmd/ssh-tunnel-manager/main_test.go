package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingTunnelCloser struct{ order *[]string }

func (c recordingTunnelCloser) Close(context.Context) error {
	*c.order = append(*c.order, "tunnels")
	return nil
}

type recordingDiscoveryCloser struct{ order *[]string }

func (c recordingDiscoveryCloser) Close() error {
	*c.order = append(*c.order, "discovery")
	return nil
}

type recordingSSHCloser struct{ order *[]string }

func (c recordingSSHCloser) Close(context.Context) error {
	*c.order = append(*c.order, "ssh")
	return nil
}

type recordingHTTPCloser struct{ order *[]string }

func (c recordingHTTPCloser) Shutdown(context.Context) error {
	*c.order = append(*c.order, "http")
	return nil
}

func TestAuthorizeRequiresTokenAndSetsStrictCookie(t *testing.T) {
	unauthorized := httptest.NewRecorder()
	if authorize(unauthorized, httptest.NewRequest(http.MethodGet, "/api/ssh-hosts", nil), "token-value") {
		t.Fatal("request without token was authorized")
	}
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", unauthorized.Code)
	}

	login := httptest.NewRecorder()
	if authorize(login, httptest.NewRequest(http.MethodGet, "/?token=token-value", nil), "token-value") {
		t.Fatal("root token request should redirect")
	}
	result := login.Result()
	if result.StatusCode != http.StatusFound || len(result.Cookies()) != 1 {
		t.Fatalf("login response = %d, cookies=%d", result.StatusCode, len(result.Cookies()))
	}
	cookie := result.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe cookie: %#v", cookie)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/ssh-hosts", nil)
	authorizedRequest.AddCookie(cookie)
	if !authorize(httptest.NewRecorder(), authorizedRequest, "token-value") {
		t.Fatal("valid cookie was rejected")
	}
}

func TestShutdownHandlerRequiresTokenBeforeStopping(t *testing.T) {
	stopped := make(chan struct{}, 1)
	handler := shutdownHandler("token-value", func() { stopped <- struct{}{} })

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/shutdown", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	select {
	case <-stopped:
		t.Fatal("unauthorized request stopped the application")
	default:
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, "/api/shutdown", nil)
	authorizedRequest.AddCookie(&http.Cookie{Name: cookieName, Value: "token-value"})
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("authorized request did not stop the application")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8765", "localhost:8765", "[::1]:8765"} {
		if !isLoopbackAddr(addr) {
			t.Fatalf("loopback rejected: %s", addr)
		}
	}
	if isLoopbackAddr("0.0.0.0:8765") {
		t.Fatal("non-loopback accepted")
	}
}

func TestParseOptions(t *testing.T) {
	opts, err := parseOptions([]string{"--addr", "127.0.0.1:9000", "--open-browser", "--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if opts.addr != "127.0.0.1:9000" || !opts.openBrowser || !opts.showVersion {
		t.Fatalf("options = %#v", opts)
	}

	defaults, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions(defaults) error = %v", err)
	}
	if defaults.addr != defaultAddr || defaults.openBrowser || defaults.showVersion {
		t.Fatalf("default options = %#v", defaults)
	}
}

func TestStartConsoleOpensBrowserAfterListening(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	var openedURL string
	serverErr, err := startConsole(server, "127.0.0.1:0", "http://127.0.0.1:8765/?token=test", true, io.Discard, func(rawURL string) error {
		openedURL = rawURL
		return nil
	})
	if err != nil {
		t.Fatalf("startConsole() error = %v", err)
	}
	if openedURL != "http://127.0.0.1:8765/?token=test" {
		t.Fatalf("opened URL = %q", openedURL)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("server.Close() error = %v", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestStartConsoleDoesNotOpenBrowserWhenListenFails(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	opened := false
	_, err = startConsole(&http.Server{}, listener.Addr().String(), "http://unused", true, io.Discard, func(string) error {
		opened = true
		return nil
	})
	if err == nil {
		t.Fatal("startConsole() succeeded for occupied address")
	}
	if opened {
		t.Fatal("browser opened before listener was bound")
	}
}

func TestStartConsoleKeepsServingWhenBrowserFails(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})}
	serverErr, err := startConsole(server, "127.0.0.1:0", "http://unused", true, io.Discard, func(string) error {
		return errors.New("browser unavailable")
	})
	if err != nil {
		t.Fatalf("startConsole() error = %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("server.Close() error = %v", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestOpenBrowser(t *testing.T) {
	binDir := t.TempDir()
	xdgOpen := filepath.Join(binDir, "xdg-open")
	if err := os.WriteFile(xdgOpen, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write xdg-open: %v", err)
	}
	t.Setenv("PATH", binDir)
	if err := openBrowser("http://127.0.0.1:8765/"); err != nil {
		t.Fatalf("openBrowser() error = %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	if err := openBrowser("http://127.0.0.1:8765/"); err == nil {
		t.Fatal("openBrowser() succeeded without xdg-open")
	}
}

func TestShutdownRuntimeClosesServicesInDependencyOrder(t *testing.T) {
	var order []string
	shutdownRuntime(
		context.Background(),
		recordingTunnelCloser{order: &order},
		recordingDiscoveryCloser{order: &order},
		recordingSSHCloser{order: &order},
		recordingHTTPCloser{order: &order},
	)
	if got := strings.Join(order, ","); got != "tunnels,discovery,ssh,http" {
		t.Fatalf("shutdown order = %s", got)
	}
}
