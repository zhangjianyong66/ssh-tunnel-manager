package ssh

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAskpassUsesPrivateNamedPipesWithoutEmbeddingSecrets(t *testing.T) {
	password := "server-password"
	passphrase := "key-passphrase"
	helper, err := newAskpass(password, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	dir := helper.dir
	defer helper.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o", info.Mode().Perm())
	}
	script, err := os.ReadFile(helper.script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), password) || strings.Contains(string(script), passphrase) {
		t.Fatal("helper script contains a secret")
	}
	for prompt, want := range map[string]string{
		"alice@example password:":          password,
		"Enter passphrase for key example": passphrase,
	} {
		output, commandErr := exec.Command(helper.script, prompt).Output()
		if commandErr != nil {
			t.Fatalf("askpass command: %v", commandErr)
		}
		if strings.TrimSpace(string(output)) != want {
			t.Fatalf("askpass output = %q, want selected secret", output)
		}
	}
	if err := helper.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("helper directory was not removed: %v", err)
	}
}
