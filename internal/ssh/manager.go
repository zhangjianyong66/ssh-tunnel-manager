// Package ssh manages long-lived system OpenSSH ControlMaster processes.
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/credential"
	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/sshconfig"
)

// Status is the lifecycle state of a server connection.
type Status string

const (
	StatusDisconnected  Status = "disconnected"
	StatusConnecting    Status = "connecting"
	StatusConnected     Status = "connected"
	StatusDisconnecting Status = "disconnecting"
	StatusFailed        Status = "failed"
)

const controlMasterReadyTimeout = 15 * time.Second

const commandOutputLimit = 1 << 20

const processCleanupTimeout = 2 * time.Second

// ErrorCode is a stable classification for API consumers.
type ErrorCode string

const (
	ErrorConfiguration       ErrorCode = "configuration"
	ErrorAuthentication      ErrorCode = "authentication_failed"
	ErrorHostKey             ErrorCode = "host_key_verification"
	ErrorHostKeyConfirmation ErrorCode = "host_key_confirmation_required"
	ErrorHostKeyChanged      ErrorCode = "host_key_changed"
	ErrorNetwork             ErrorCode = "network_unreachable"
	ErrorTimeout             ErrorCode = "timeout"
	ErrorCredentialRequired  ErrorCode = "credential_required"
	ErrorDependency          ErrorCode = "local_dependency_missing"
	ErrorCancelled           ErrorCode = "user_cancelled"
	ErrorProcess             ErrorCode = "process_failed"
	ErrorNotConnected        ErrorCode = "server_not_connected"
	ErrorHostInUse           ErrorCode = "host_in_use"
)

// Error is a user-safe SSH failure with an optional redacted diagnostic.
type Error struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Diagnostic  string    `json:"diagnostic,omitempty"`
	StageHost   string    `json:"stageHost,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// CommandSpec describes one executable invocation without shell expansion.
type CommandSpec struct {
	Binary     string
	Args       []string
	Env        []string
	Dir        string
	ExtraFiles []*os.File
}

// Process is the precise child process handle owned by a session.
type Process interface {
	Wait() error
	Signal(os.Signal) error
	Diagnostics() string
}

// Runner starts SSH child processes. It is injectable for deterministic tests.
type Runner interface {
	Start(context.Context, CommandSpec) (Process, error)
}

type oneShotRunner interface {
	Run(context.Context, CommandSpec) error
}

type outputRunner interface {
	Output(context.Context, CommandSpec) (CommandOutput, error)
}

// CommandOutput contains bounded output from one SSH command.
type CommandOutput struct {
	Stdout string
	Stderr string
}

// RealRunner starts the system ssh binary.
type RealRunner struct {
	Binary string
}

func (r RealRunner) Start(_ context.Context, spec CommandSpec) (Process, error) {
	binary := r.Binary
	if binary == "" {
		binary = spec.Binary
	}
	if binary == "" {
		binary = "ssh"
	}
	cmd := exec.Command(binary, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Dir = spec.Dir
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	stderr := &limitedBuffer{limit: commandOutputLimit}
	cmd.Stderr = stderr
	cmd.ExtraFiles = spec.ExtraFiles
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &realProcess{cmd: cmd, stderr: stderr}, nil
}

func (r RealRunner) Run(ctx context.Context, spec CommandSpec) error {
	binary := r.Binary
	if binary == "" {
		binary = spec.Binary
	}
	if binary == "" {
		binary = "ssh"
	}
	cmd := exec.CommandContext(ctx, binary, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Dir = spec.Dir
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.ExtraFiles = spec.ExtraFiles
	return cmd.Run()
}

// Output runs one command and captures at most commandOutputLimit bytes from
// each output stream.
func (r RealRunner) Output(ctx context.Context, spec CommandSpec) (CommandOutput, error) {
	binary := r.Binary
	if binary == "" {
		binary = spec.Binary
	}
	if binary == "" {
		binary = "ssh"
	}
	cmd := exec.CommandContext(ctx, binary, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Dir = spec.Dir
	cmd.Stdin = nil
	stdout := &limitedBuffer{limit: commandOutputLimit}
	stderr := &limitedBuffer{limit: commandOutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = spec.ExtraFiles
	err := cmd.Run()
	return CommandOutput{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type limitedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return written, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type realProcess struct {
	cmd     *exec.Cmd
	stderr  *limitedBuffer
	secrets []string
}

func (p *realProcess) Wait() error                { return p.cmd.Wait() }
func (p *realProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *realProcess) Diagnostics() string {
	values := append([]string{p.stderr.String()}, p.secrets...)
	return sanitizeDiagnostic(values...)
}

// ConnectOptions supplies one-shot authentication input. Values are kept only
// in this call and are never copied into command arguments or environment.
type ConnectOptions struct {
	Username           string
	Password           string
	Passphrase         string
	SavePassword       bool
	SavePassphrase     bool
	StageHost          string
	ConfirmFingerprint string
}

// ConfigSource renders a complete, read-only OpenSSH config for one session.
// The returned bytes are written to a private 0600 file by Manager.
type ConfigSource interface {
	Render() ([]byte, error)
}

// JumpResolver returns the direct jump alias for a managed Host. An empty
// result means OpenSSH system configuration is responsible for the chain.
type JumpResolver interface {
	JumpHost(context.Context, string) (string, error)
}

// UsernameResolver supplies the effective project username for credential
// lookup when the request deliberately omits it.
type UsernameResolver interface {
	Username(context.Context, string) (string, error)
}

// Snapshot is a lock-free representation of a server connection.
type Snapshot struct {
	Host        string    `json:"host"`
	Status      Status    `json:"status"`
	ControlPath string    `json:"-"`
	ConnectedAt time.Time `json:"connectedAt,omitempty"`
	LastError   *Error    `json:"lastError,omitempty"`
	Diagnostic  string    `json:"diagnostic,omitempty"`
}

type session struct {
	opMu               sync.Mutex
	mu                 sync.Mutex
	host               string
	status             Status
	controlPath        string
	configPath         string
	runDir             string
	process            Process
	askpass            *askpass
	done               chan struct{}
	connectedAt        time.Time
	lastError          *Error
	diagnostic         string
	username           string
	pendingFingerprint string
}

// Manager owns all SSH processes started by the application.
type Manager struct {
	mu          sync.RWMutex
	sessions    map[string]*session
	runner      Runner
	credentials credential.Store
	runtimeDir  string
	config      ConfigSource
	resolver    JumpResolver
	usernames   UsernameResolver
}

// NewManager creates a manager. runtimeDir is created with restrictive mode.
func NewManager(runner Runner, store credential.Store, runtimeDir string, sources ...any) (*Manager, error) {
	if runner == nil {
		runner = RealRunner{}
	}
	if runtimeDir == "" {
		runtimeDir = filepath.Join(os.TempDir(), "ssh-tunnel-manager")
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建 SSH 运行目录: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return nil, fmt.Errorf("收紧 SSH 运行目录权限: %w", err)
	}
	manager := &Manager{sessions: make(map[string]*session), runner: runner, credentials: store, runtimeDir: runtimeDir}
	for _, source := range sources {
		if value, ok := source.(ConfigSource); ok {
			manager.config = value
		}
		if value, ok := source.(JumpResolver); ok {
			manager.resolver = value
		}
		if value, ok := source.(UsernameResolver); ok {
			manager.usernames = value
		}
	}
	return manager, nil
}

// Snapshot returns the current state for host, or a disconnected snapshot.
func (m *Manager) Snapshot(host string) Snapshot {
	m.mu.RLock()
	s := m.sessions[host]
	m.mu.RUnlock()
	if s == nil {
		return Snapshot{Host: host, Status: StatusDisconnected}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// Snapshots returns a stable copy of all known sessions.
func (m *Manager) Snapshots() []Snapshot {
	m.mu.RLock()
	list := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.RUnlock()
	result := make([]Snapshot, 0, len(list))
	for _, s := range list {
		s.mu.Lock()
		result = append(result, s.snapshot())
		s.mu.Unlock()
	}
	return result
}

// Connect starts an idempotent ControlMaster process for host. When the Host
// has a managed jump, the jump is connected first and its credentials are
// never reused for the target stage.
func (m *Manager) Connect(ctx context.Context, host string, opts ConnectOptions) (Snapshot, error) {
	if err := validateHost(host); err != nil {
		return Snapshot{Host: host, Status: StatusFailed}, &Error{Code: ErrorConfiguration, Message: err.Error()}
	}
	jump := ""
	if m.resolver != nil {
		var err error
		jump, err = m.resolver.JumpHost(ctx, host)
		if err != nil {
			return m.failUnknown(host, &Error{Code: ErrorConfiguration, Message: "SSH 跳板配置无效", Diagnostic: sanitizeDiagnostic(err.Error())})
		}
	}
	if jump != "" && jump != host && m.Snapshot(jump).Status != StatusConnected {
		jumpOptions := ConnectOptions{}
		if opts.StageHost == "" || opts.StageHost == jump {
			jumpOptions = opts
			jumpOptions.StageHost = ""
		}
		if _, err := m.connectSingle(ctx, jump, jumpOptions, ""); err != nil {
			return m.Snapshot(host), withStage(err, jump)
		}
		if opts.StageHost == jump || opts.StageHost == "" {
			opts.Password = ""
			opts.Passphrase = ""
			opts.Username = ""
			opts.ConfirmFingerprint = ""
			opts.SavePassword = false
			opts.SavePassphrase = false
		}
	}
	if opts.StageHost != "" && opts.StageHost != host {
		opts.Password = ""
		opts.Passphrase = ""
		opts.Username = ""
		opts.ConfirmFingerprint = ""
		opts.SavePassword = false
		opts.SavePassphrase = false
	}
	return m.connectSingle(ctx, host, opts, jump)
}

func (m *Manager) failUnknown(host string, err *Error) (Snapshot, error) {
	return Snapshot{Host: host, Status: StatusFailed, LastError: err}, err
}

func withStage(err error, stage string) error {
	var sshErr *Error
	if errors.As(err, &sshErr) {
		copy := *sshErr
		copy.StageHost = stage
		return &copy
	}
	return &Error{Code: ErrorProcess, Message: "SSH 连接失败", StageHost: stage, Diagnostic: sanitizeDiagnostic(err.Error())}
}

func (m *Manager) connectSingle(ctx context.Context, host string, opts ConnectOptions, jump string) (Snapshot, error) {
	if err := validateHost(host); err != nil {
		return Snapshot{Host: host, Status: StatusFailed}, &Error{Code: ErrorConfiguration, Message: err.Error()}
	}
	if opts.Username == "" && m.usernames != nil {
		if username, err := m.usernames.Username(ctx, host); err == nil {
			opts.Username = username
		}
	}
	m.mu.Lock()
	s := m.sessions[host]
	if s == nil {
		s = &session{host: host, status: StatusDisconnected}
		m.sessions[host] = s
	}
	m.mu.Unlock()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if opts.Username == "" {
		opts.Username = s.username
	}
	if opts.Username != "" {
		s.username = opts.Username
	}
	if s.status == StatusConnected || s.status == StatusConnecting {
		snap := s.snapshot()
		s.mu.Unlock()
		return snap, nil
	}
	if opts.ConfirmFingerprint != "" {
		if !fingerprintPattern.MatchString(opts.ConfirmFingerprint) || s.pendingFingerprint == "" || s.pendingFingerprint != opts.ConfirmFingerprint {
			s.mu.Unlock()
			return m.fail(s, &Error{Code: ErrorHostKeyChanged, Message: "确认的 SSH 主机指纹与待确认指纹不一致"}, "主机指纹确认不匹配")
		}
	}
	s.status = StatusConnecting
	s.lastError = nil
	s.diagnostic = ""
	s.mu.Unlock()
	if m.credentials != nil {
		if opts.Password == "" {
			if value, lookupErr := m.credentials.Lookup(ctx, credential.Ref{Host: host, Username: opts.Username, Purpose: "password"}); lookupErr == nil {
				opts.Password = value
			}
		}
		if opts.Passphrase == "" {
			if value, lookupErr := m.credentials.Lookup(ctx, credential.Ref{Host: host, Username: opts.Username, Purpose: "passphrase"}); lookupErr == nil {
				opts.Passphrase = value
			}
		}
	}
	if opts.SavePassword || opts.SavePassphrase {
		if m.credentials == nil {
			return m.fail(s, &Error{Code: ErrorDependency, Message: "Secret Service 不可用，凭据未保存"}, "未配置凭据存储")
		}
		if opts.SavePassword && opts.Password != "" {
			if err := m.credentials.Save(ctx, credential.Ref{Host: host, Username: opts.Username, Purpose: "password"}, opts.Password); err != nil {
				return m.fail(s, &Error{Code: ErrorDependency, Message: "Secret Service 不可用，密码未保存"}, sanitizeDiagnostic(err.Error(), opts.Password))
			}
		}
		if opts.SavePassphrase && opts.Passphrase != "" {
			if err := m.credentials.Save(ctx, credential.Ref{Host: host, Username: opts.Username, Purpose: "passphrase"}, opts.Passphrase); err != nil {
				return m.fail(s, &Error{Code: ErrorDependency, Message: "Secret Service 不可用，私钥口令未保存"}, sanitizeDiagnostic(err.Error(), opts.Passphrase))
			}
		}
	}

	runDir, err := os.MkdirTemp(m.runtimeDir, "session-")
	if err != nil {
		return m.fail(s, &Error{Code: ErrorDependency, Message: "创建 SSH 运行目录失败"}, err.Error())
	}
	if err := os.Chmod(runDir, 0o700); err != nil {
		_ = os.RemoveAll(runDir)
		return m.fail(s, &Error{Code: ErrorDependency, Message: "设置 SSH 运行目录权限失败"}, err.Error())
	}
	controlPath := filepath.Join(runDir, "c")
	if len(controlPath) > 100 {
		_ = os.RemoveAll(runDir)
		return m.fail(s, &Error{Code: ErrorDependency, Message: "SSH ControlPath 路径过长"}, "运行目录路径过长")
	}
	configPath, err := m.writeSessionConfig(runDir, jump, func() string {
		if jump == "" {
			return ""
		}
		return m.Snapshot(jump).ControlPath
	}())
	if err != nil {
		_ = os.RemoveAll(runDir)
		return m.fail(s, &Error{Code: ErrorDependency, Message: "创建 SSH 配置失败"}, sanitizeDiagnostic(err.Error()))
	}
	askpass, err := newAskpass(opts.Password, opts.Passphrase, opts.ConfirmFingerprint)
	if err != nil {
		_ = os.RemoveAll(runDir)
		return m.fail(s, &Error{Code: ErrorDependency, Message: "创建 SSH 认证交互通道失败"}, err.Error())
	}
	args := m.withConfig(configPath, "-M", "-N", "-T", "-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath="+controlPath, host)
	spec := CommandSpec{Binary: "ssh", Args: args, Env: askpass.Env(), ExtraFiles: askpass.ExtraFiles()}
	process, err := m.runner.Start(ctx, spec)
	if err != nil {
		_ = askpass.Close()
		_ = os.RemoveAll(runDir)
		return m.fail(s, classifyError(err.Error(), "启动 SSH 失败"), sanitizeDiagnostic(err.Error(), opts.Password, opts.Passphrase))
	}
	if real, ok := process.(*realProcess); ok {
		real.secrets = []string{controlPath, configPath, opts.Password, opts.Passphrase}
	}
	s.mu.Lock()
	s.runDir = runDir
	s.controlPath = controlPath
	s.configPath = configPath
	s.process = process
	s.askpass = askpass
	s.done = make(chan struct{})
	s.mu.Unlock()
	go m.monitor(s, opts.Password, opts.Passphrase)
	if readyErr := m.waitForControlMaster(ctx, s, host, controlPath); readyErr != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		m.stopSession(cleanupContext, s)
		cancel()
		return m.fail(s, readyErr, readyErr.Diagnostic)
	}
	s.mu.Lock()
	if s.status == StatusConnecting {
		s.status = StatusConnected
		s.connectedAt = time.Now()
		s.pendingFingerprint = ""
	}
	snapshot := s.snapshot()
	s.mu.Unlock()
	if snapshot.LastError != nil {
		return snapshot, snapshot.LastError
	}
	return m.Snapshot(host), nil
}

// Disconnect gracefully stops host's process and removes its private runtime files.
func (m *Manager) Disconnect(ctx context.Context, host string) (Snapshot, error) {
	if m.hasConnectedDependents(ctx, host) {
		return m.Snapshot(host), &Error{Code: ErrorHostInUse, Message: "SSH 跳板机仍被已连接目标使用", StageHost: host}
	}
	m.mu.RLock()
	s := m.sessions[host]
	m.mu.RUnlock()
	if s == nil {
		return Snapshot{Host: host, Status: StatusDisconnected}, nil
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	m.stopSession(ctx, s)
	return m.Snapshot(host), nil
}

// Execute runs a remote command through host's connected ControlMaster. The
// command is passed as an argument vector and is cancelled with ctx.
func (m *Manager) Execute(ctx context.Context, host string, command []string) (CommandOutput, error) {
	if err := validateHost(host); err != nil {
		return CommandOutput{}, &Error{Code: ErrorConfiguration, Message: err.Error()}
	}
	if err := validateRemoteCommand(command); err != nil {
		return CommandOutput{}, &Error{Code: ErrorConfiguration, Message: err.Error()}
	}
	m.mu.RLock()
	s := m.sessions[host]
	m.mu.RUnlock()
	if s == nil {
		return CommandOutput{}, &Error{Code: ErrorNotConnected, Message: "SSH 服务器尚未连接"}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	status, controlPath, configPath := s.status, s.controlPath, s.configPath
	s.mu.Unlock()
	if status != StatusConnected || controlPath == "" {
		return CommandOutput{}, &Error{Code: ErrorNotConnected, Message: "SSH 服务器尚未连接"}
	}
	runner, ok := m.runner.(outputRunner)
	if !ok {
		return CommandOutput{}, &Error{Code: ErrorDependency, Message: "SSH 执行器不支持远程命令输出"}
	}
	args := m.withConfig(configPath, "-S", controlPath, "-T", "-o", "BatchMode=yes", "--", host)
	args = append(args, command...)
	result, err := runner.Output(ctx, CommandSpec{Binary: "ssh", Args: args})
	if err == nil {
		return result, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return result, &Error{Code: ErrorCancelled, Message: "用户取消了远程命令"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, &Error{Code: ErrorTimeout, Message: "远程命令执行超时"}
	}
	return result, classifyError(result.Stderr, "远程 SSH 命令执行失败")
}

// StartLocalForward starts a long-lived local forward through host's existing
// ControlMaster. The caller owns the returned process and must call Wait once.
func (m *Manager) StartLocalForward(ctx context.Context, host string, localPort, remotePort uint16) (Process, error) {
	if err := validateHost(host); err != nil {
		return nil, &Error{Code: ErrorConfiguration, Message: err.Error()}
	}
	if localPort == 0 || remotePort == 0 {
		return nil, &Error{Code: ErrorConfiguration, Message: "SSH 转发端口无效"}
	}
	m.mu.RLock()
	s := m.sessions[host]
	m.mu.RUnlock()
	if s == nil {
		return nil, &Error{Code: ErrorNotConnected, Message: "SSH 服务器尚未连接"}
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	status, controlPath, configPath := s.status, s.controlPath, s.configPath
	s.mu.Unlock()
	if status != StatusConnected || controlPath == "" {
		return nil, &Error{Code: ErrorNotConnected, Message: "SSH 服务器尚未连接"}
	}
	runner, ok := m.runner.(oneShotRunner)
	if !ok {
		return nil, &Error{Code: ErrorDependency, Message: "SSH 执行器不支持主连接检查"}
	}
	check := CommandSpec{Binary: "ssh", Args: m.withConfig(configPath, "-S", controlPath, "-O", "check", host)}
	if err := runner.Run(ctx, check); err != nil {
		return nil, controlCheckError(ctx)
	}

	localAddress := fmt.Sprintf("127.0.0.1:%d", localPort)
	remoteAddress := fmt.Sprintf("127.0.0.1:%d", remotePort)
	args := []string{
		"-S", controlPath,
		"-N", "-T",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-L", localAddress + ":" + remoteAddress,
		"--", host,
	}
	args = m.withConfig(configPath, args...)
	process, err := m.runner.Start(ctx, CommandSpec{Binary: "ssh", Args: args})
	if err != nil {
		if ctx.Err() != nil {
			return nil, controlCheckError(ctx)
		}
		return nil, classifyError(err.Error(), "启动 SSH 本地转发失败")
	}
	if err := runner.Run(ctx, check); err != nil {
		stopUnownedProcess(process)
		return nil, controlCheckError(ctx)
	}
	s.mu.Lock()
	stillConnected := s.status == StatusConnected && s.controlPath == controlPath
	s.mu.Unlock()
	if !stillConnected {
		stopUnownedProcess(process)
		return nil, &Error{Code: ErrorNotConnected, Message: "SSH 服务器主连接已断开"}
	}
	if real, ok := process.(*realProcess); ok {
		real.secrets = []string{controlPath}
	}
	return process, nil
}

func controlCheckError(ctx context.Context) *Error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &Error{Code: ErrorCancelled, Message: "用户取消了 SSH 本地转发"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Message: "SSH 本地转发启动超时"}
	}
	return &Error{Code: ErrorNotConnected, Message: "SSH 服务器主连接已断开"}
}

func stopUnownedProcess(process Process) {
	if process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = process.Wait()
		close(done)
	}()
	_ = process.Signal(os.Interrupt)
	timer := time.NewTimer(processCleanupTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		_ = process.Signal(os.Kill)
	}
}

func (m *Manager) stopSession(ctx context.Context, s *session) {
	s.mu.Lock()
	if s.status == StatusDisconnected {
		s.mu.Unlock()
		return
	}
	s.status = StatusDisconnecting
	process, controlPath, configPath, runDir, done, helper := s.process, s.controlPath, s.configPath, s.runDir, s.done, s.askpass
	s.mu.Unlock()
	if controlPath != "" {
		spec := CommandSpec{Binary: "ssh", Args: m.withConfig(configPath, "-S", controlPath, "-O", "exit", s.host)}
		if r, ok := m.runner.(oneShotRunner); ok {
			_ = r.Run(ctx, spec)
		}
	}
	if process != nil {
		_ = process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-ctx.Done():
			_ = process.Signal(os.Kill)
		}
	}
	if runDir != "" {
		_ = os.RemoveAll(runDir)
	}
	_ = helper.Close()
	s.mu.Lock()
	s.status = StatusDisconnected
	s.process = nil
	s.controlPath = ""
	s.configPath = ""
	s.runDir = ""
	s.askpass = nil
	s.connectedAt = time.Time{}
	s.mu.Unlock()
}

// Close disconnects all sessions with a bounded context.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.RLock()
	hosts := make([]string, 0, len(m.sessions))
	for host := range m.sessions {
		hosts = append(hosts, host)
	}
	m.mu.RUnlock()
	hosts = m.closeOrder(ctx, hosts)
	var first error
	for _, host := range hosts {
		if _, err := m.Disconnect(ctx, host); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) hasConnectedDependents(ctx context.Context, host string) bool {
	if m.resolver == nil {
		return false
	}
	m.mu.RLock()
	list := make([]*session, 0, len(m.sessions))
	for _, current := range m.sessions {
		list = append(list, current)
	}
	m.mu.RUnlock()
	for _, current := range list {
		if current.host == host {
			continue
		}
		jump, err := m.resolver.JumpHost(ctx, current.host)
		if err != nil || jump != host {
			continue
		}
		current.mu.Lock()
		status := current.status
		current.mu.Unlock()
		if status == StatusConnecting || status == StatusConnected || status == StatusDisconnecting {
			return true
		}
	}
	return false
}

func (m *Manager) closeOrder(ctx context.Context, hosts []string) []string {
	if m.resolver == nil {
		return hosts
	}
	depth := make(map[string]int, len(hosts))
	var visit func(string, map[string]bool) int
	visit = func(host string, stack map[string]bool) int {
		if value, ok := depth[host]; ok {
			return value
		}
		if stack[host] {
			return 0
		}
		stack[host] = true
		value := 0
		if jump, err := m.resolver.JumpHost(ctx, host); err == nil && jump != "" {
			value = visit(jump, stack) + 1
		}
		delete(stack, host)
		depth[host] = value
		return value
	}
	for _, host := range hosts {
		visit(host, make(map[string]bool))
	}
	sort.SliceStable(hosts, func(i, j int) bool { return depth[hosts[i]] > depth[hosts[j]] })
	return hosts
}

func (m *Manager) monitor(s *session, password, passphrase string) {
	s.mu.Lock()
	process := s.process
	s.mu.Unlock()
	err := process.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		defer close(s.done)
	}
	if s.status == StatusDisconnecting || s.status == StatusDisconnected {
		_ = s.askpass.Close()
		s.askpass = nil
		return
	}
	diagnostic := sanitizeDiagnostic(process.Diagnostics(), password, passphrase, s.controlPath, s.configPath)
	if err == nil {
		s.status = StatusFailed
		s.diagnostic = diagnostic
		s.lastError = &Error{Code: ErrorProcess, Message: "SSH 连接意外结束", Diagnostic: diagnostic}
		_ = os.RemoveAll(s.runDir)
		_ = s.askpass.Close()
		s.askpass = nil
		return
	}
	s.status = StatusFailed
	s.diagnostic = diagnostic
	s.lastError = classifyError(diagnostic, "SSH 连接已断开")
	s.lastError.StageHost = s.host
	if s.lastError.Code == ErrorHostKeyConfirmation {
		s.pendingFingerprint = s.lastError.Fingerprint
	} else {
		s.pendingFingerprint = ""
	}
	if s.lastError.Code == ErrorAuthentication && password == "" && passphrase == "" {
		s.lastError.Code = ErrorCredentialRequired
		s.lastError.Message = "SSH 需要密码或私钥口令"
	}
	if s.lastError.Diagnostic == "" {
		s.lastError.Diagnostic = diagnostic
	}
	_ = os.RemoveAll(s.runDir)
	_ = s.askpass.Close()
	s.askpass = nil
}

func (m *Manager) waitForControlMaster(ctx context.Context, s *session, host, controlPath string) *Error {
	runner, ok := m.runner.(oneShotRunner)
	if !ok {
		return nil
	}
	readyContext, cancel := context.WithTimeout(ctx, controlMasterReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		configPath := s.configPath
		s.mu.Unlock()
		spec := CommandSpec{Binary: "ssh", Args: m.withConfig(configPath, "-S", controlPath, "-O", "check", host)}
		if err := runner.Run(readyContext, spec); err == nil {
			return nil
		}
		s.mu.Lock()
		status, lastError := s.status, s.lastError
		s.mu.Unlock()
		if status == StatusFailed && lastError != nil {
			copy := *lastError
			return &copy
		}
		select {
		case <-readyContext.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return &Error{Code: ErrorCancelled, Message: "用户取消了 SSH 连接"}
			}
			return &Error{Code: ErrorTimeout, Message: "等待 SSH 主连接超时"}
		case <-ticker.C:
		}
	}
}

func (m *Manager) fail(s *session, err *Error, diagnostic string) (Snapshot, error) {
	if err.StageHost == "" {
		err.StageHost = s.host
	}
	if err.Fingerprint == "" {
		err.Fingerprint = extractFingerprint(diagnostic)
	}
	s.mu.Lock()
	s.status = StatusFailed
	s.lastError = err
	s.diagnostic = diagnostic
	s.mu.Unlock()
	return m.Snapshot(s.host), err
}

func (s *session) snapshot() Snapshot {
	var last *Error
	if s.lastError != nil {
		copy := *s.lastError
		last = &copy
	}
	return Snapshot{Host: s.host, Status: s.status, ControlPath: s.controlPath, ConnectedAt: s.connectedAt, LastError: last, Diagnostic: s.diagnostic}
}

func (m *Manager) withConfig(configPath string, args ...string) []string {
	if configPath == "" {
		return args
	}
	result := make([]string, 0, len(args)+2)
	result = append(result, "-F", configPath)
	result = append(result, args...)
	return result
}

func (m *Manager) writeSessionConfig(runDir, jump, jumpControlPath string) (string, error) {
	if m.config == nil {
		return "", nil
	}
	value, err := m.config.Render()
	if err != nil {
		return "", err
	}
	if jump != "" && jumpControlPath != "" {
		if err := validateHost(jump); err != nil {
			return "", err
		}
		override := fmt.Sprintf("Host %s\n    ControlMaster auto\n    ControlPath \"%s\"\n\n", jump, strings.ReplaceAll(strings.ReplaceAll(jumpControlPath, "\\", "\\\\"), "\"", "\\\""))
		value = append([]byte(override), value...)
	}
	path := filepath.Join(runDir, "ssh_config")
	if err := os.WriteFile(path, value, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func validateHost(host string) error {
	return sshconfig.ValidateAlias(host)
}

func validateRemoteCommand(command []string) error {
	if len(command) == 0 || command[0] == "" {
		return errors.New("远程命令不能为空")
	}
	for _, argument := range command {
		if argument == "" || strings.IndexByte(argument, 0) >= 0 || strings.ContainsAny(argument, " \t\r\n;&|`$(){}[]<>*?!\\\"'") {
			return errors.New("远程命令参数无效")
		}
	}
	return nil
}

func classifyError(diagnostic, fallback string) *Error {
	lower := strings.ToLower(diagnostic)
	code := ErrorProcess
	message := fallback
	switch {
	case strings.Contains(lower, "remote host identification has changed"), strings.Contains(lower, "offending"):
		code, message = ErrorHostKeyChanged, "SSH 主机密钥已变化，已拒绝连接"
	case strings.Contains(lower, "authenticity of host"), strings.Contains(lower, "are you sure you want to continue connecting"), strings.Contains(lower, "fingerprint"):
		code, message = ErrorHostKeyConfirmation, "首次连接需要确认 SSH 主机指纹"
	case strings.Contains(lower, "host key verification failed"), strings.Contains(lower, "remote host identification"):
		code, message = ErrorHostKey, "主机密钥校验失败，请使用系统 SSH 核验指纹"
	case strings.Contains(lower, "permission denied"):
		code, message = ErrorAuthentication, "SSH 认证失败"
	case strings.Contains(lower, "could not resolve hostname"), strings.Contains(lower, "connection refused"), strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		code, message = ErrorNetwork, "无法连接到 SSH 服务器"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		code, message = ErrorTimeout, "SSH 连接超时"
	case strings.Contains(lower, "cancel"):
		code, message = ErrorCancelled, "用户取消了 SSH 连接"
	case strings.Contains(lower, "ssh: not found"), strings.Contains(lower, "executable file not found"):
		code, message = ErrorDependency, "本机缺少 ssh 依赖"
	}
	return &Error{Code: code, Message: message, Diagnostic: sanitizeDiagnostic(diagnostic), Fingerprint: extractFingerprint(diagnostic)}
}

var fingerprintPattern = regexp.MustCompile(`SHA256:[A-Za-z0-9+/=]+`)

func extractFingerprint(diagnostic string) string {
	return fingerprintPattern.FindString(diagnostic)
}

func sanitizeDiagnostic(value ...string) string {
	if len(value) == 0 {
		return ""
	}
	diagnostic := value[0]
	for _, secret := range value[1:] {
		if secret != "" {
			diagnostic = strings.ReplaceAll(diagnostic, secret, "[已隐藏]")
		}
	}
	return strings.TrimSpace(diagnostic)
}
