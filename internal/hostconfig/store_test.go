package hostconfig

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStoreCRUDAndPermissions(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "id_test")
	if err := os.WriteFile(key, []byte("not-a-real-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config", "hosts.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Profile{Alias: "jump", HostName: "jump.example.com", Port: 60022, Username: "alice", IdentityFile: key})
	if err != nil {
		t.Fatal(err)
	}
	if created.IdentityFile != key {
		t.Fatalf("identity = %q, want %q", created.IdentityFile, key)
	}
	if _, err := store.Create(Profile{Alias: "target", HostName: "192.0.2.210", Port: 22, Username: "ubuntu", JumpHost: "jump"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("jump"); !errors.Is(err, ErrReferenced) {
		t.Fatalf("delete referenced = %v", err)
	}
	updated, err := store.Update("target", Profile{Alias: "target", HostName: "192.168.1.211", Port: 2222, Username: "ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.HostName != "192.168.1.211" || updated.Port != 2222 {
		t.Fatalf("updated = %#v", updated)
	}
	if err := store.Delete("jump"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := reloaded.List()
	if err != nil || len(profiles) != 1 || profiles[0].Alias != "target" {
		t.Fatalf("profiles = %#v, err = %v", profiles, err)
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("permissions dir=%o file=%o", directoryInfo.Mode().Perm(), fileInfo.Mode().Perm())
	}
}

func TestFileStoreRejectsInvalidProfilesAndNestedManagedJump(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	invalid := []Profile{
		{Alias: "-bad", HostName: "example.com", Port: 22, Username: "alice"},
		{Alias: "bad-host", HostName: "https://example.com", Port: 22, Username: "alice"},
		{Alias: "bad-port", HostName: "example.com", Username: "alice"},
		{Alias: "bad-user", HostName: "example.com", Port: 22, Username: "two users"},
		{Alias: "bad-key", HostName: "example.com", Port: 22, Username: "alice", IdentityFile: "relative"},
		{Alias: "self", HostName: "example.com", Port: 22, Username: "alice", JumpHost: "self"},
	}
	for _, profile := range invalid {
		if _, err := store.Create(profile); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%#v) = %v", profile, err)
		}
	}
	if _, err := store.Create(Profile{Alias: "first", HostName: "first.example", Port: 22, Username: "alice", JumpHost: "system-jump"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(Profile{Alias: "target", HostName: "target.example", Port: 22, Username: "alice", JumpHost: "first"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested jump = %v", err)
	}
}

func TestFileStoreRejectsDuplicateAndAliasChange(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{Alias: "server", HostName: "example.com", Port: 22, Username: "alice"}
	if _, err := store.Create(profile); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(profile); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	profile.Alias = "renamed"
	if _, err := store.Update("server", profile); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rename = %v", err)
	}
}

func TestFileStoreMalformedFileRemainsUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	original := []byte(`{"version":1,"hosts":[],"unknown":true}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(path)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("load = %v", err)
	}
	if profiles, listErr := store.List(); listErr == nil || len(profiles) != 0 {
		t.Fatalf("list = %#v, err = %v", profiles, listErr)
	}
	if _, createErr := store.Create(Profile{Alias: "server", HostName: "example.com", Port: 22, Username: "alice"}); createErr == nil {
		t.Fatal("expected read-only store")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != string(original) {
		t.Fatalf("malformed file overwritten: %q", value)
	}
}

func TestFileStoreConcurrentCreatesRemainValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	aliases := []string{"a", "b", "c", "d"}
	var wait sync.WaitGroup
	for _, alias := range aliases {
		alias := alias
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, createErr := store.Create(Profile{Alias: alias, HostName: alias + ".example", Port: 22, Username: "alice"}); createErr != nil {
				t.Errorf("create %s: %v", alias, createErr)
			}
		}()
	}
	wait.Wait()
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := reloaded.List()
	if err != nil || len(profiles) != len(aliases) {
		t.Fatalf("profiles = %#v, err = %v", profiles, err)
	}
}

func TestDefaultPathUsesXDGConfigHome(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(directory, "ssh-tunnel-manager", "hosts.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}
