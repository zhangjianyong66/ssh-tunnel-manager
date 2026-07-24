package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExpandsIncludeAndFiltersPatterns(t *testing.T) {
	dir := t.TempDir()
	include := filepath.Join(dir, "conf.d", "extra.conf")
	if err := os.MkdirAll(filepath.Dir(include), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(include, []byte("Host included\n  HostName example.test\nHost *.wild !excluded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "config")
	content := "Include conf.d/*.conf\nHost first second # inline comment\nHost first\nHost ?wild\n"
	if err := os.WriteFile(root, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 3 {
		t.Fatalf("got %d hosts, want 3: %#v", len(cfg.Hosts), cfg.Hosts)
	}
	for i, want := range []string{"included", "first", "second"} {
		if cfg.Hosts[i].Alias != want {
			t.Fatalf("host %d = %q, want %q", i, cfg.Hosts[i].Alias, want)
		}
	}
}

func TestLoadMissingConfigIsEmpty(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 0 || len(cfg.Diagnostics) != 0 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadIncludeCycleProducesDiagnostic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("Include b\nHost a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("Include a\nHost b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 2 || len(cfg.Diagnostics) == 0 {
		t.Fatalf("expected hosts and cycle diagnostic: %#v", cfg)
	}
}
