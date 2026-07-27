package ssh

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// askpass passes secrets through private named pipes. The helper script and
// environment contain only paths; secret values remain in process memory.
type askpass struct {
	dir         string
	script      string
	fingerprint string
	confirm     bool
	done        chan struct{}
	once        sync.Once
	wg          sync.WaitGroup
}

func newAskpass(password, passphrase string, confirmation ...string) (*askpass, error) {
	if password == "" && passphrase == "" && len(confirmation) == 0 {
		return &askpass{}, nil
	}
	dir, err := os.MkdirTemp("", "stm-askpass-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	script := filepath.Join(dir, "askpass.sh")
	fingerprint := ""
	if len(confirmation) > 0 {
		fingerprint = confirmation[0]
	}
	content := "#!/bin/sh\nbase=${0%/*}\nprompt=$1\ncase \"$prompt\" in\n  *passphrase*|*Passphrase*) cat \"$base/passphrase\" ;;\n  *password*|*Password*) cat \"$base/password\" ;;\n  *authenticity*|*Authenticity*|*yes/no*|*fingerprint*|*Fingerprint*)\n    if [ \"${STM_HOST_KEY_CONFIRM:-}\" = yes ] && [ -n \"${STM_HOST_KEY_FINGERPRINT:-}\" ] && printf '%s' \"$prompt\" | grep -F -- \"$STM_HOST_KEY_FINGERPRINT\" >/dev/null 2>&1; then printf '%s\\n' yes; else printf '%s\\n' no; fi ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	a := &askpass{dir: dir, script: script, fingerprint: fingerprint, confirm: fingerprint != "", done: make(chan struct{})}
	for name, secret := range map[string]string{"password": password, "passphrase": passphrase} {
		path := filepath.Join(dir, name)
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			a.Close()
			return nil, err
		}
		a.wg.Add(1)
		go a.serve(path, secret)
	}
	return a, nil
}

func (a *askpass) serve(path, value string) {
	defer a.wg.Done()
	for {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0o600)
		if err == nil {
			_, _ = syscall.Write(fd, []byte(value+"\n"))
			_ = syscall.Close(fd)
			select {
			case <-a.done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		select {
		case <-a.done:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (a *askpass) Env() []string {
	if a == nil || a.script == "" {
		return nil
	}
	env := []string{"SSH_ASKPASS=" + a.script, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=stm"}
	if a.confirm {
		env = append(env, "STM_HOST_KEY_CONFIRM=yes", "STM_HOST_KEY_FINGERPRINT="+a.fingerprint)
	}
	return env
}

func (a *askpass) ExtraFiles() []*os.File { return nil }

func (a *askpass) Close() error {
	if a == nil {
		return nil
	}
	if a.done != nil {
		a.once.Do(func() { close(a.done) })
	}
	a.wg.Wait()
	if a.dir == "" {
		return nil
	}
	if err := os.RemoveAll(a.dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
