package hostconfig

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

const (
	SourceSystem  = "system"
	SourceManaged = "managed"
)

// Host is the merged API projection for system and project-managed aliases.
type Host struct {
	Alias        string `json:"alias"`
	Source       string `json:"source"`
	SourceFile   string `json:"sourceFile,omitempty"`
	Editable     bool   `json:"editable"`
	HostName     string `json:"hostName,omitempty"`
	Port         uint16 `json:"port,omitempty"`
	Username     string `json:"username,omitempty"`
	IdentityFile string `json:"identityFile,omitempty"`
	JumpHost     string `json:"jumpHost,omitempty"`
	Valid        bool   `json:"valid"`
	Issue        string `json:"issue,omitempty"`
}

// Snapshot is one isolated merged catalog view.
type Snapshot struct {
	Hosts        []Host                 `json:"hosts"`
	Diagnostics  []sshconfig.Diagnostic `json:"diagnostics,omitempty"`
	ManagedError string                 `json:"managedError,omitempty"`
}

type systemLoader interface {
	Load(string) (sshconfig.Config, error)
}

type effectiveResolver interface {
	Resolve(context.Context, string, string) (sshconfig.Effective, error)
}

// Catalog merges system aliases with the managed Store and owns cross-source
// validation. Store remains the sole persisted source of managed profiles.
type Catalog struct {
	mu            sync.RWMutex
	systemPath    string
	loader        systemLoader
	effective     effectiveResolver
	store         Store
	system        sshconfig.Config
	profiles      []Profile
	managedErr    error
	invalidReason map[string]string
}

// NewCatalog creates and loads a production catalog.
func NewCatalog(ctx context.Context, systemPath string, store Store) (*Catalog, error) {
	return newCatalog(ctx, systemPath, sshconfig.Loader{}, sshconfig.EffectiveResolver{}, store)
}

func newCatalog(ctx context.Context, systemPath string, loader systemLoader, effective effectiveResolver, store Store) (*Catalog, error) {
	if store == nil {
		return nil, errors.New("项目 SSH Host Store 不能为空")
	}
	if loader == nil || effective == nil {
		return nil, errors.New("SSH 配置解析器不能为空")
	}
	catalog := &Catalog{systemPath: systemPath, loader: loader, effective: effective, store: store}
	if err := catalog.Refresh(ctx); err != nil {
		return nil, err
	}
	return catalog, nil
}

// Refresh reloads the system config and managed snapshot. A malformed managed
// file is exposed as a diagnostic while system hosts remain available.
func (c *Catalog) Refresh(ctx context.Context) error {
	system, err := c.loader.Load(c.systemPath)
	if err != nil {
		return err
	}
	profiles, managedErr := c.store.List()
	if managedErr != nil {
		profiles = []Profile{}
	}
	invalid := c.validateProfiles(ctx, system, profiles)
	c.mu.Lock()
	c.system = system
	c.profiles = profiles
	c.managedErr = managedErr
	c.invalidReason = invalid
	c.mu.Unlock()
	return nil
}

// Snapshot returns a copy that callers may safely modify.
func (c *Catalog) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := Snapshot{
		Hosts:       make([]Host, 0, len(c.system.Hosts)+len(c.profiles)),
		Diagnostics: append([]sshconfig.Diagnostic(nil), c.system.Diagnostics...),
	}
	if c.managedErr != nil {
		result.ManagedError = c.managedErr.Error()
	}
	for _, systemHost := range c.system.Hosts {
		result.Hosts = append(result.Hosts, Host{Alias: systemHost.Alias, Source: SourceSystem, SourceFile: systemHost.Source, Valid: true})
	}
	for _, profile := range c.profiles {
		issue := c.invalidReason[profile.Alias]
		result.Hosts = append(result.Hosts, managedHost(profile, issue))
	}
	return result
}

// Has reports whether alias is present and valid in the merged catalog.
func (c *Catalog) Has(alias string) bool {
	for _, host := range c.Snapshot().Hosts {
		if host.Alias == alias && host.Valid {
			return true
		}
	}
	return false
}

// Managed returns a managed profile by alias.
func (c *Catalog) Managed(alias string) (Profile, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, profile := range c.profiles {
		if profile.Alias == alias {
			return profile, true
		}
	}
	return Profile{}, false
}

// ReferencedBy returns managed aliases that use alias as their jump host.
func (c *Catalog) ReferencedBy(alias string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var result []string
	for _, profile := range c.profiles {
		if profile.JumpHost == alias {
			result = append(result, profile.Alias)
		}
	}
	return result
}

// Create validates cross-source constraints and persists a profile.
func (c *Catalog) Create(ctx context.Context, profile Profile) (Profile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.managedErr != nil {
		return Profile{}, c.managedErr
	}
	if systemContains(c.system, profile.Alias) || indexOf(c.profiles, profile.Alias) >= 0 {
		return Profile{}, ErrConflict
	}
	if err := c.validateCandidate(ctx, profile, ""); err != nil {
		return Profile{}, err
	}
	created, err := c.store.Create(profile)
	if err != nil {
		return Profile{}, err
	}
	c.profiles = append(c.profiles, created)
	c.invalidReason = c.validateProfiles(ctx, c.system, c.profiles)
	return created, nil
}

// Update changes a managed profile while keeping its alias immutable.
func (c *Catalog) Update(ctx context.Context, alias string, profile Profile) (Profile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.managedErr != nil {
		return Profile{}, c.managedErr
	}
	if indexOf(c.profiles, alias) < 0 {
		return Profile{}, ErrNotFound
	}
	if err := c.validateCandidate(ctx, profile, alias); err != nil {
		return Profile{}, err
	}
	updated, err := c.store.Update(alias, profile)
	if err != nil {
		return Profile{}, err
	}
	c.profiles[indexOf(c.profiles, alias)] = updated
	c.invalidReason = c.validateProfiles(ctx, c.system, c.profiles)
	return updated, nil
}

// Delete removes a managed profile. Store enforces managed references.
func (c *Catalog) Delete(alias string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.managedErr != nil {
		return c.managedErr
	}
	index := indexOf(c.profiles, alias)
	if index < 0 {
		return ErrNotFound
	}
	if err := c.store.Delete(alias); err != nil {
		return err
	}
	c.profiles = append(cloneProfiles(c.profiles[:index]), c.profiles[index+1:]...)
	delete(c.invalidReason, alias)
	return nil
}

// Render returns an OpenSSH config for the current managed snapshot.
func (c *Catalog) Render() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.managedErr != nil {
		return nil, c.managedErr
	}
	return Render(c.profiles, c.systemPath)
}

func (c *Catalog) validateCandidate(ctx context.Context, profile Profile, updating string) error {
	if profile.Alias != updating && updating != "" {
		return fmt.Errorf("%w: Host 别名不可修改", ErrInvalid)
	}
	if profile.JumpHost == "" {
		return nil
	}
	for _, managed := range c.profiles {
		if managed.Alias == profile.JumpHost {
			if managed.JumpHost != "" {
				return fmt.Errorf("%w: %s 不能再使用跳板机", ErrInvalidJump, managed.Alias)
			}
			return nil
		}
	}
	if !systemContains(c.system, profile.JumpHost) {
		return fmt.Errorf("%w: %s 不存在", ErrInvalidJump, profile.JumpHost)
	}
	if err := c.validateSystemJump(ctx, profile.JumpHost); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJump, err)
	}
	return nil
}

func (c *Catalog) validateProfiles(ctx context.Context, system sshconfig.Config, profiles []Profile) map[string]string {
	invalid := make(map[string]string)
	systemJumpIssues := make(map[string]string)
	managed := make(map[string]Profile, len(profiles))
	for _, profile := range profiles {
		managed[profile.Alias] = profile
		if systemContains(system, profile.Alias) {
			invalid[profile.Alias] = "与系统 SSH Host 重名"
		}
	}
	for _, profile := range profiles {
		if profile.JumpHost == "" {
			continue
		}
		if jump, ok := managed[profile.JumpHost]; ok {
			if jump.JumpHost != "" {
				invalid[profile.Alias] = "跳板机不能再使用跳板机"
			}
			continue
		}
		if !systemContains(system, profile.JumpHost) {
			invalid[profile.Alias] = "跳板机不存在"
			continue
		}
		issue, checked := systemJumpIssues[profile.JumpHost]
		if !checked {
			if err := c.validateSystemJump(ctx, profile.JumpHost); err != nil {
				issue = err.Error()
			}
			systemJumpIssues[profile.JumpHost] = issue
		}
		if issue != "" {
			invalid[profile.Alias] = issue
		}
	}
	return invalid
}

func (c *Catalog) validateSystemJump(ctx context.Context, alias string) error {
	effective, err := c.effective.Resolve(ctx, c.systemPath, alias)
	if err != nil {
		return fmt.Errorf("系统跳板机配置无法解析: %w", err)
	}
	if activeOption(effective.ProxyJump) || activeOption(effective.ProxyCommand) {
		return errors.New("系统跳板机已经使用其他跳板或代理命令")
	}
	return nil
}

func activeOption(value string) bool {
	return value != "" && value != "none"
}

func systemContains(config sshconfig.Config, alias string) bool {
	for _, host := range config.Hosts {
		if host.Alias == alias {
			return true
		}
	}
	return false
}

func managedHost(profile Profile, issue string) Host {
	return Host{
		Alias:        profile.Alias,
		Source:       SourceManaged,
		Editable:     true,
		HostName:     profile.HostName,
		Port:         profile.Port,
		Username:     profile.Username,
		IdentityFile: profile.IdentityFile,
		JumpHost:     profile.JumpHost,
		Valid:        issue == "",
		Issue:        issue,
	}
}
