package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
