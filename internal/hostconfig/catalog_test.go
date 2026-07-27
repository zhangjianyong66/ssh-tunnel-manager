package hostconfig

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

type fakeSystemLoader struct {
	config sshconfig.Config
	err    error
}

func (f fakeSystemLoader) Load(string) (sshconfig.Config, error) { return f.config, f.err }

type fakeEffectiveResolver struct {
	values map[string]sshconfig.Effective
	errors map[string]error
}

func (f fakeEffectiveResolver) Resolve(_ context.Context, _ string, alias string) (sshconfig.Effective, error) {
	if err := f.errors[alias]; err != nil {
		return sshconfig.Effective{}, err
	}
	value, ok := f.values[alias]
	if !ok {
		return sshconfig.Effective{}, errors.New("missing effective config")
	}
	return value, nil
}

func TestCatalogMergesSourcesAndValidatesSystemJump(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	loader := fakeSystemLoader{config: sshconfig.Config{Hosts: []sshconfig.Host{{Alias: "system-jump", Source: "/home/user/.ssh/config"}}}}
	resolver := fakeEffectiveResolver{values: map[string]sshconfig.Effective{
		"system-jump": {HostName: "jump.example", Port: 22, User: "alice", ProxyJump: "none", ProxyCommand: "none"},
	}}
	catalog, err := newCatalog(context.Background(), "/home/user/.ssh/config", loader, resolver, store)
	if err != nil {
		t.Fatal(err)
	}
	created, err := catalog.Create(context.Background(), Profile{Alias: "target", HostName: "192.0.2.210", Port: 22, Username: "ubuntu", JumpHost: "system-jump"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Alias != "target" || !catalog.Has("target") || !catalog.Has("system-jump") {
		t.Fatalf("created = %#v, snapshot = %#v", created, catalog.Snapshot())
	}
	snapshot := catalog.Snapshot()
	if len(snapshot.Hosts) != 2 || snapshot.Hosts[0].Source != SourceSystem || snapshot.Hosts[1].Source != SourceManaged || !snapshot.Hosts[1].Editable {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if _, err := catalog.Create(context.Background(), Profile{Alias: "system-jump", HostName: "other.example", Port: 22, Username: "alice"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("system conflict = %v", err)
	}
}

func TestCatalogMarksBrokenAndNestedSystemJumpInvalid(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(Profile{Alias: "missing-target", HostName: "target.example", Port: 22, Username: "alice", JumpHost: "missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(Profile{Alias: "nested-target", HostName: "target.example", Port: 22, Username: "alice", JumpHost: "system-nested"}); err != nil {
		t.Fatal(err)
	}
	loader := fakeSystemLoader{config: sshconfig.Config{Hosts: []sshconfig.Host{{Alias: "system-nested", Source: "/config"}}}}
	resolver := fakeEffectiveResolver{values: map[string]sshconfig.Effective{
		"system-nested": {HostName: "jump.example", Port: 22, User: "alice", ProxyJump: "another"},
	}}
	catalog, err := newCatalog(context.Background(), "/config", loader, resolver, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range catalog.Snapshot().Hosts {
		if host.Source == SourceManaged && (host.Valid || host.Issue == "") {
			t.Fatalf("host should be invalid: %#v", host)
		}
	}
}

func TestCatalogManagedErrorKeepsSystemHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, loadErr := NewFileStore(path)
	if loadErr == nil {
		t.Fatal("expected managed load error")
	}
	loader := fakeSystemLoader{config: sshconfig.Config{Hosts: []sshconfig.Host{{Alias: "system", Source: "/config"}}}}
	catalog, err := newCatalog(context.Background(), "/config", loader, fakeEffectiveResolver{}, store)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := catalog.Snapshot()
	if len(snapshot.Hosts) != 1 || snapshot.Hosts[0].Alias != "system" || snapshot.ManagedError == "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRenderQuotesPathsAndDisablesImplicitJump(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, `id "quoted"`)
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := Render([]Profile{{Alias: "server", HostName: "example.com", Port: 2222, Username: "alice", IdentityFile: key}}, "/home/user/.ssh/config")
	if err != nil {
		t.Fatal(err)
	}
	text := string(value)
	for _, want := range []string{"Host server", `HostName "example.com"`, `Port "2222"`, "ProxyJump none", `Include "/home/user/.ssh/config"`, `\"quoted\"`} {
		if !strings.Contains(text, want) {
			t.Errorf("render missing %q:\n%s", want, text)
		}
	}
}

func TestRenderedConfigIsAcceptedByOpenSSH(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("system ssh is unavailable")
	}
	directory := t.TempDir()
	systemPath := filepath.Join(directory, "system-config")
	if err := os.WriteFile(systemPath, []byte("Host system-only\n    HostName system.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles := []Profile{
		{Alias: "jump", HostName: "jump.example", Port: 60022, Username: "alice"},
		{Alias: "target", HostName: "192.0.2.210", Port: 2222, Username: "ubuntu", JumpHost: "jump"},
	}
	value, err := Render(profiles, systemPath)
	if err != nil {
		t.Fatal(err)
	}
	renderedPath := filepath.Join(directory, "rendered-config")
	if err := os.WriteFile(renderedPath, value, 0o600); err != nil {
		t.Fatal(err)
	}
	effective, err := (sshconfig.EffectiveResolver{}).Resolve(context.Background(), renderedPath, "target")
	if err != nil {
		t.Fatal(err)
	}
	if effective.HostName != "192.0.2.210" || effective.Port != 2222 || effective.User != "ubuntu" || effective.ProxyJump != "jump" {
		t.Fatalf("effective = %#v", effective)
	}
}
