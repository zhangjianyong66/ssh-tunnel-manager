// Package hostconfig owns project-managed, non-secret SSH host profiles.
package hostconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/configfile"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

const (
	configVersion = 1
	maxConfigSize = 1 << 20
	maxProfiles   = 256
)

var (
	// ErrInvalid indicates an invalid profile or persisted config.
	ErrInvalid = errors.New("项目 SSH Host 配置无效")
	// ErrNotFound indicates that a managed profile does not exist.
	ErrNotFound = errors.New("项目 SSH Host 不存在")
	// ErrConflict indicates that an alias already exists.
	ErrConflict = errors.New("SSH Host 别名已存在")
	// ErrInvalidJump indicates a missing or nested jump host.
	ErrInvalidJump = errors.New("跳板机配置无效")
	// ErrReferenced indicates that another managed profile uses this profile.
	ErrReferenced = errors.New("SSH Host 仍被其他配置引用")
	// ErrUnavailable indicates that the persisted config cannot be changed.
	ErrUnavailable = errors.New("项目 SSH Host 配置不可用")
)

// Profile is one project-managed OpenSSH Host. It never contains credentials.
type Profile struct {
	Alias        string `json:"alias"`
	HostName     string `json:"hostName"`
	Port         uint16 `json:"port"`
	Username     string `json:"username"`
	IdentityFile string `json:"identityFile,omitempty"`
	JumpHost     string `json:"jumpHost,omitempty"`
}

type config struct {
	Version int       `json:"version"`
	Hosts   []Profile `json:"hosts"`
}

// Store manages project Host profiles.
type Store interface {
	List() ([]Profile, error)
	Create(Profile) (Profile, error)
	Update(string, Profile) (Profile, error)
	Delete(string) error
}

// FileStore persists profiles in one versioned JSON file.
type FileStore struct {
	mu       sync.Mutex
	path     string
	profiles []Profile
	loadErr  error
}

// DefaultPath returns the XDG-compatible managed Host file path.
func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	if directory == "" {
		return "", errors.New("用户配置目录为空")
	}
	return filepath.Join(directory, "ssh-tunnel-manager", "hosts.json"), nil
}

// NewFileStore loads path. Invalid content leaves a read-only empty store so
// callers can continue serving system SSH hosts without overwriting the file.
func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{path: path, profiles: []Profile{}}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		store.loadErr = fmt.Errorf("%w: 读取配置失败", ErrUnavailable)
		return store, store.loadErr
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		store.loadErr = fmt.Errorf("%w: 读取配置失败", ErrUnavailable)
		return store, store.loadErr
	}
	if len(value) > maxConfigSize {
		err := fmt.Errorf("%w: 配置文件过大", ErrInvalid)
		store.loadErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
		return store, err
	}
	loaded, err := decodeConfig(value)
	if err != nil {
		store.loadErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
		return store, err
	}
	store.profiles = loaded.Hosts
	return store, nil
}

// NewUnavailableStore returns a read-only Store used when the XDG path cannot
// be determined. System SSH hosts can remain available through Catalog.
func NewUnavailableStore(cause error) *FileStore {
	if cause == nil {
		cause = errors.New("配置路径不可用")
	}
	return &FileStore{profiles: []Profile{}, loadErr: fmt.Errorf("%w: %v", ErrUnavailable, cause)}
}

// List returns an isolated profile snapshot.
func (s *FileStore) List() ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return []Profile{}, s.loadErr
	}
	return cloneProfiles(s.profiles), nil
}

// Create validates and persists a new profile.
func (s *FileStore) Create(profile Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return Profile{}, s.loadErr
	}
	if len(s.profiles) >= maxProfiles {
		return Profile{}, fmt.Errorf("%w: Host 数量超过上限", ErrInvalid)
	}
	if indexOf(s.profiles, profile.Alias) >= 0 {
		return Profile{}, ErrConflict
	}
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return Profile{}, err
	}
	next := append(cloneProfiles(s.profiles), normalized)
	if err := validateManagedReferences(next); err != nil {
		return Profile{}, err
	}
	if err := s.persist(next); err != nil {
		return Profile{}, err
	}
	s.profiles = next
	return normalized, nil
}

// Update replaces the mutable fields of alias. Aliases are immutable.
func (s *FileStore) Update(alias string, profile Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return Profile{}, s.loadErr
	}
	if err := sshconfig.ValidateAlias(alias); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if profile.Alias != alias {
		return Profile{}, fmt.Errorf("%w: Host 别名不可修改", ErrInvalid)
	}
	index := indexOf(s.profiles, alias)
	if index < 0 {
		return Profile{}, ErrNotFound
	}
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return Profile{}, err
	}
	next := cloneProfiles(s.profiles)
	next[index] = normalized
	if err := validateManagedReferences(next); err != nil {
		return Profile{}, err
	}
	if err := s.persist(next); err != nil {
		return Profile{}, err
	}
	s.profiles = next
	return normalized, nil
}

// Delete removes an unreferenced profile.
func (s *FileStore) Delete(alias string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	index := indexOf(s.profiles, alias)
	if index < 0 {
		return ErrNotFound
	}
	for _, profile := range s.profiles {
		if profile.JumpHost == alias {
			return fmt.Errorf("%w: %s", ErrReferenced, profile.Alias)
		}
	}
	next := cloneProfiles(s.profiles[:index])
	next = append(next, s.profiles[index+1:]...)
	if err := s.persist(next); err != nil {
		return err
	}
	s.profiles = next
	return nil
}

func (s *FileStore) persist(profiles []Profile) error {
	if err := configfile.WriteJSON(s.path, config{Version: configVersion, Hosts: profiles}); err != nil {
		return fmt.Errorf("%w: 保存配置失败: %v", ErrUnavailable, err)
	}
	return nil
}

func decodeConfig(value []byte) (config, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var loaded config
	if err := decoder.Decode(&loaded); err != nil {
		return config{}, fmt.Errorf("%w: JSON 格式错误: %v", ErrInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return config{}, fmt.Errorf("%w: 包含多个 JSON 值", ErrInvalid)
		}
		return config{}, fmt.Errorf("%w: 包含无效尾随内容: %v", ErrInvalid, err)
	}
	if loaded.Version != configVersion {
		return config{}, fmt.Errorf("%w: 不支持版本 %d", ErrInvalid, loaded.Version)
	}
	if len(loaded.Hosts) > maxProfiles {
		return config{}, fmt.Errorf("%w: Host 数量超过上限", ErrInvalid)
	}
	seen := make(map[string]bool, len(loaded.Hosts))
	for index, profile := range loaded.Hosts {
		normalized, err := normalizeProfile(profile)
		if err != nil {
			return config{}, fmt.Errorf("第 %d 个 Host: %w", index+1, err)
		}
		if seen[normalized.Alias] {
			return config{}, fmt.Errorf("%w: 重复别名 %s", ErrConflict, normalized.Alias)
		}
		seen[normalized.Alias] = true
		loaded.Hosts[index] = normalized
	}
	if err := validateManagedReferences(loaded.Hosts); err != nil {
		return config{}, err
	}
	if loaded.Hosts == nil {
		loaded.Hosts = []Profile{}
	}
	return loaded, nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	if err := sshconfig.ValidateAlias(profile.Alias); err != nil {
		return Profile{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	profile.HostName = strings.TrimSpace(profile.HostName)
	if !validHostName(profile.HostName) {
		return Profile{}, fmt.Errorf("%w: HostName 无效", ErrInvalid)
	}
	if profile.Port == 0 {
		return Profile{}, fmt.Errorf("%w: 端口必须在 1 到 65535 之间", ErrInvalid)
	}
	if !validConfigAtom(profile.Username) {
		return Profile{}, fmt.Errorf("%w: 用户名无效", ErrInvalid)
	}
	if profile.JumpHost != "" {
		if err := sshconfig.ValidateAlias(profile.JumpHost); err != nil {
			return Profile{}, fmt.Errorf("%w: 跳板机别名无效", ErrInvalid)
		}
		if profile.JumpHost == profile.Alias {
			return Profile{}, fmt.Errorf("%w: 不能引用自身作为跳板机", ErrInvalid)
		}
	}
	identity, err := normalizeIdentityFile(profile.IdentityFile)
	if err != nil {
		return Profile{}, err
	}
	profile.IdentityFile = identity
	return profile, nil
}

func normalizeIdentityFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: 无法展开私钥路径", ErrInvalid)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("%w: 不支持其他用户的私钥路径", ErrInvalid)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: 私钥路径必须是绝对路径或 ~/ 路径", ErrInvalid)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: 私钥文件不存在或不是普通文件", ErrInvalid)
	}
	return path, nil
}

func validHostName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	if len(value) > 253 || strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '-' {
				return false
			}
			if character > unicode.MaxASCII {
				return false
			}
		}
	}
	return true
}

func validConfigAtom(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune("#\"'\\", character) {
			return false
		}
	}
	return true
}

func validateManagedReferences(profiles []Profile) error {
	byAlias := make(map[string]Profile, len(profiles))
	for _, profile := range profiles {
		byAlias[profile.Alias] = profile
	}
	for _, profile := range profiles {
		jump, managed := byAlias[profile.JumpHost]
		if !managed {
			continue
		}
		if jump.JumpHost != "" {
			return fmt.Errorf("%w: 跳板机 %s 不能再使用跳板机", ErrInvalid, jump.Alias)
		}
	}
	return nil
}

func cloneProfiles(source []Profile) []Profile {
	if len(source) == 0 {
		return []Profile{}
	}
	return append([]Profile(nil), source...)
}

func indexOf(profiles []Profile, alias string) int {
	for index, profile := range profiles {
		if profile.Alias == alias {
			return index
		}
	}
	return -1
}
