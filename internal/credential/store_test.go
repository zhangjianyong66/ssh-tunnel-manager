package credential

import (
	"context"
	"errors"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	s := NewMemoryStore()
	ref := Ref{Host: "example", Username: "alice", Purpose: "password"}
	if _, err := s.Lookup(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup error = %v, want ErrNotFound", err)
	}
	if err := s.Save(context.Background(), ref, "secret-value"); err != nil {
		t.Fatal(err)
	}
	value, err := s.Lookup(context.Background(), ref)
	if err != nil || value != "secret-value" {
		t.Fatalf("lookup = %q, %v", value, err)
	}
	if err := s.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup after delete = %v", err)
	}
}

type fakeKeyring struct {
	values map[string]string
	err    error
}

func (f *fakeKeyring) key(service, user string) string { return service + "\x00" + user }
func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	value, ok := f.values[f.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (f *fakeKeyring) Set(service, user, password string) error {
	if f.err != nil {
		return f.err
	}
	f.values[f.key(service, user)] = password
	return nil
}
func (f *fakeKeyring) Delete(service, user string) error {
	if f.err != nil {
		return f.err
	}
	key := f.key(service, user)
	if _, ok := f.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.values, key)
	return nil
}

func TestSecretServiceStoreLifecycle(t *testing.T) {
	backend := &fakeKeyring{values: make(map[string]string)}
	store := &SecretServiceStore{backend: backend}
	ref := Ref{Host: "server/a", Username: "alice@example", Purpose: "password"}
	if err := store.Save(context.Background(), ref, "secret-value"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Lookup(context.Background(), ref)
	if err != nil || value != "secret-value" {
		t.Fatalf("lookup = %q, %v", value, err)
	}
	if err := store.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(context.Background(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup after delete = %v", err)
	}
}

func TestSecretServiceStoreUnavailableFailsSafely(t *testing.T) {
	store := &SecretServiceStore{backend: &fakeKeyring{err: errors.New("no session bus")}}
	ref := Ref{Host: "example", Purpose: "password"}
	if _, err := store.Lookup(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lookup error = %v, want ErrUnavailable", err)
	}
	if err := store.Save(context.Background(), ref, "secret-value"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("save error = %v, want ErrUnavailable", err)
	}
	if err := store.Delete(context.Background(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("delete error = %v, want ErrUnavailable", err)
	}
}
