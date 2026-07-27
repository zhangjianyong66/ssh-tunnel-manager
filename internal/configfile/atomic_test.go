package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONCreatesPrivateDurableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "config.json")
	want := map[string]any{"version": float64(1), "enabled": true}
	if err := WriteJSON(path, want); err != nil {
		t.Fatal(err)
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
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(value, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != want["version"] || got["enabled"] != want["enabled"] {
		t.Fatalf("value = %#v, want %#v", got, want)
	}
}

func TestWriteJSONRejectsEmptyPathWithoutCreatingFile(t *testing.T) {
	if err := WriteJSON("", map[string]bool{"enabled": true}); err == nil {
		t.Fatal("expected empty path error")
	}
}
