// Package preference persists non-sensitive user preferences.
package preference

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/configfile"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

const (
	configVersion = 1
	maxConfigSize = 1 << 20
)

// Store reads and updates per-host port refresh preferences.
type Store interface {
	AutoRefresh(string) (bool, error)
	SetAutoRefresh(string, bool) error
}

type hostPreference struct {
	AutoRefresh bool `json:"autoRefresh"`
}

type config struct {
	Version int                       `json:"version"`
	Hosts   map[string]hostPreference `json:"hosts"`
}

// FileStore keeps preferences in one versioned JSON file.
type FileStore struct {
	mu      sync.Mutex
	path    string
	config  config
	loadErr error
}

// DefaultPath returns the XDG-compatible preference file path.
func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	if directory == "" {
		return "", errors.New("用户配置目录为空")
	}
	return filepath.Join(directory, "ssh-tunnel-manager", "config.json"), nil
}

// NewFileStore loads path. A malformed file returns a usable default store and
// an error; the store will not overwrite the malformed file.
func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{
		path: path,
		config: config{
			Version: configVersion,
			Hosts:   make(map[string]hostPreference),
		},
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		store.loadErr = fmt.Errorf("读取偏好设置失败: %w", err)
		return store, store.loadErr
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		store.loadErr = fmt.Errorf("读取偏好设置失败: %w", err)
		return store, store.loadErr
	}
	if len(value) > maxConfigSize {
		store.loadErr = errors.New("偏好设置文件过大")
		return store, store.loadErr
	}
	loaded, err := decodeConfig(value)
	if err != nil {
		store.loadErr = err
		return store, err
	}
	store.config = loaded
	return store, nil
}

// AutoRefresh returns the saved value for host. Missing hosts default to false.
func (s *FileStore) AutoRefresh(host string) (bool, error) {
	if err := validateHost(host); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return false, s.loadErr
	}
	return s.config.Hosts[host].AutoRefresh, nil
}

// SetAutoRefresh atomically persists the value for host.
func (s *FileStore) SetAutoRefresh(host string, enabled bool) error {
	if err := validateHost(host); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	next := cloneConfig(s.config)
	next.Hosts[host] = hostPreference{AutoRefresh: enabled}
	if err := writeConfig(s.path, next); err != nil {
		return err
	}
	s.config = next
	return nil
}

func decodeConfig(value []byte) (config, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var loaded config
	if err := decoder.Decode(&loaded); err != nil {
		return config{}, fmt.Errorf("偏好设置格式无效: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return config{}, err
	}
	if loaded.Version != configVersion {
		return config{}, fmt.Errorf("不支持的偏好设置版本: %d", loaded.Version)
	}
	if loaded.Hosts == nil {
		loaded.Hosts = make(map[string]hostPreference)
	}
	for host := range loaded.Hosts {
		if err := validateHost(host); err != nil {
			return config{}, fmt.Errorf("偏好设置包含无效 Host: %w", err)
		}
	}
	return loaded, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("偏好设置包含无效尾随内容: %w", err)
	}
	return errors.New("偏好设置包含多个 JSON 值")
}

func cloneConfig(source config) config {
	result := config{Version: source.Version, Hosts: make(map[string]hostPreference, len(source.Hosts))}
	for host, value := range source.Hosts {
		result.Hosts[host] = value
	}
	return result
}

func writeConfig(path string, value config) error {
	if err := configfile.WriteJSON(path, value); err != nil {
		return fmt.Errorf("保存偏好设置失败: %w", err)
	}
	return nil
}

func validateHost(host string) error {
	return sshconfig.ValidateAlias(host)
}
