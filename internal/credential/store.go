// Package credential abstracts secret storage from SSH connection management.
package credential

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

var (
	// ErrUnavailable indicates that no usable Secret Service session is present.
	ErrUnavailable = errors.New("Secret Service 不可用")
	// ErrNotFound indicates that the requested secret is not stored.
	ErrNotFound = errors.New("凭据不存在")
)

// Ref identifies a credential without containing its secret value.
type Ref struct {
	Host     string
	Username string
	Purpose  string
}

// Store reads and writes secrets without exposing them to callers that do not
// need the value. Implementations must never log the returned secret.
type Store interface {
	Lookup(context.Context, Ref) (string, error)
	Save(context.Context, Ref, string) error
	Delete(context.Context, Ref) error
}

// MemoryStore is an in-memory store for tests and one-process credentials.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[Ref]string
}

// NewMemoryStore creates an empty in-memory credential store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{values: make(map[Ref]string)} }

func (s *MemoryStore) Lookup(_ context.Context, ref Ref) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[ref]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) Save(_ context.Context, ref Ref, value string) error {
	if value == "" {
		return errors.New("凭据不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[ref] = value
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, ref Ref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}

type keyringBackend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (systemKeyring) Delete(service, user string) error { return keyring.Delete(service, user) }

// SecretServiceStore accesses Linux Secret Service through its D-Bus client.
// Secret values never enter command arguments, environment variables or files.
type SecretServiceStore struct {
	backend keyringBackend
}

// NewSecretServiceStore creates the production Linux keyring adapter.
func NewSecretServiceStore() *SecretServiceStore {
	return &SecretServiceStore{backend: systemKeyring{}}
}

func (s *SecretServiceStore) keyring() keyringBackend {
	if s == nil || s.backend == nil {
		return systemKeyring{}
	}
	return s.backend
}

func (s *SecretServiceStore) Lookup(_ context.Context, ref Ref) (string, error) {
	value, err := s.keyring().Get(serviceName(ref), accountName(ref))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: 查询失败", ErrUnavailable)
	}
	return value, nil
}

func (s *SecretServiceStore) Save(_ context.Context, ref Ref, value string) error {
	if value == "" {
		return errors.New("凭据不能为空")
	}
	if err := s.keyring().Set(serviceName(ref), accountName(ref), value); err != nil {
		return fmt.Errorf("%w: 保存失败", ErrUnavailable)
	}
	return nil
}

func (s *SecretServiceStore) Delete(_ context.Context, ref Ref) error {
	if err := s.keyring().Delete(serviceName(ref), accountName(ref)); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: 删除失败", ErrUnavailable)
	}
	return nil
}

func serviceName(ref Ref) string {
	return "ssh-tunnel-manager/" + url.QueryEscape(ref.Purpose)
}

func accountName(ref Ref) string {
	return url.QueryEscape(ref.Username) + "@" + url.QueryEscape(ref.Host)
}
