package preference

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStoreDefaultsAndPersistsAutoRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "config.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.AutoRefresh("server-a"); err != nil || enabled {
		t.Fatalf("default = %v, err = %v", enabled, err)
	}
	if err := store.SetAutoRefresh("server-a", true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := reloaded.AutoRefresh("server-a"); err != nil || !enabled {
		t.Fatalf("reloaded = %v, err = %v", enabled, err)
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

func TestFileStoreRejectsMalformedFileWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"version":1,"hosts":{},"unknown":true}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(path)
	if err == nil {
		t.Fatal("expected malformed config error")
	}
	if enabled, lookupErr := store.AutoRefresh("server-a"); lookupErr == nil || enabled {
		t.Fatalf("lookup = %v, err = %v", enabled, lookupErr)
	}
	if err := store.SetAutoRefresh("server-a", true); err == nil {
		t.Fatal("expected write to preserve malformed config")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != string(original) {
		t.Fatalf("malformed config overwritten: %q", value)
	}
}

func TestFileStoreConcurrentUpdatesRemainValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for _, host := range []string{"server-a", "server-b", "server-c"} {
		host := host
		wait.Add(1)
		go func() {
			defer wait.Done()
			if updateErr := store.SetAutoRefresh(host, true); updateErr != nil {
				t.Errorf("set %s: %v", host, updateErr)
			}
		}()
	}
	wait.Wait()
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"server-a", "server-b", "server-c"} {
		if enabled, lookupErr := reloaded.AutoRefresh(host); lookupErr != nil || !enabled {
			t.Fatalf("%s = %v, err = %v", host, enabled, lookupErr)
		}
	}
}

func TestFileStoreRejectsInvalidHostAndOversizedFile(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAutoRefresh("-bad", true); err == nil {
		t.Fatal("expected invalid host error")
	}
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, make([]byte, maxConfigSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("expected oversized config error")
	}
}
