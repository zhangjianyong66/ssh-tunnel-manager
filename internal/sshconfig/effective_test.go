package sshconfig

import (
	"context"
	"reflect"
	"testing"
)

func TestEffectiveResolverUsesArgumentVectorAndParsesOutput(t *testing.T) {
	var gotBinary string
	var gotArgs []string
	resolver := EffectiveResolver{Run: func(_ context.Context, binary string, args []string) ([]byte, error) {
		gotBinary = binary
		gotArgs = append([]string(nil), args...)
		return []byte("host jump\nuser alice\nhostname jump.example\nport 60022\nidentityfile ~/.ssh/id_a\nidentityfile ~/.ssh/id_b\nproxyjump none\nproxycommand none\n"), nil
	}}
	effective, err := resolver.Resolve(context.Background(), "/tmp/ssh config", "jump")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"-G", "-F", "/tmp/ssh config", "--", "jump"}
	if gotBinary != "ssh" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("command = %q %#v", gotBinary, gotArgs)
	}
	if effective.HostName != "jump.example" || effective.Port != 60022 || effective.User != "alice" || len(effective.IdentityFiles) != 2 || effective.ProxyJump != "none" {
		t.Fatalf("effective = %#v", effective)
	}
}

func TestEffectiveResolverRejectsInvalidAliasBeforeExecution(t *testing.T) {
	called := false
	resolver := EffectiveResolver{Run: func(context.Context, string, []string) ([]byte, error) {
		called = true
		return nil, nil
	}}
	if _, err := resolver.Resolve(context.Background(), "/tmp/config", "-oBad=yes"); err == nil {
		t.Fatal("expected invalid alias error")
	}
	if called {
		t.Fatal("runner called for invalid alias")
	}
}

func TestParseEffectiveRejectsMissingAndInvalidPort(t *testing.T) {
	for _, value := range []string{
		"hostname example\nuser alice\n",
		"hostname example\nuser alice\nport 0\n",
		"hostname example\nuser alice\nport bad\n",
	} {
		if _, err := parseEffective([]byte(value)); err == nil {
			t.Errorf("parseEffective(%q) succeeded", value)
		}
	}
}
