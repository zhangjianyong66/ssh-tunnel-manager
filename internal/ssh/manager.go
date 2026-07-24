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
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/credential"
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

// ErrorCode is a stable classification for API consumers.
type ErrorCode string

const (
	ErrorConfiguration      ErrorCode = "configuration"
	ErrorAuthentication     ErrorCode = "authentication_failed"
	ErrorHostKey            ErrorCode = "host_key_verification"
	ErrorNetwork            ErrorCode = "network_unreachable"
	ErrorTimeout            ErrorCode = "timeout"
	ErrorCredentialRequired ErrorCode = "credential_required"
	ErrorDependency         ErrorCode = "local_dependency_missing"
	ErrorCancelled          ErrorCode = "user_cancelled"
	ErrorProcess            ErrorCode = "process_failed"
	ErrorNotConnected       ErrorCode = "server_not_connected"
)

// Error is a user-safe SSH failure with an optional redacted diagnostic.
type Error struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Diagnostic string    `json:"diagnostic,omitempty"`
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.ExtraFiles = spec.ExtraFiles
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &realProcess{cmd: cmd, stderr: &stderr}, nil
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
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
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

func (b *limitedBuffer) String() string { return b.buffer.String() }

type realProcess struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func (p *realProcess) Wait() error                { return p.cmd.Wait() }
func (p *realProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *realProcess) Diagnostics() string        { return p.stderr.String() }

// ConnectOptions supplies one-shot authentication input. Values are kept only
// in this call and are never copied into command arguments or environment.
type ConnectOptions struct {
	Username       string
	Password       string
	Passphrase     string
	SavePassword   bool
	SavePassphrase bool
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
	opMu        sync.Mutex
	mu          sync.Mutex
	host        string
	status      Status
	controlPath string
	runDir      string
	process     Process
	askpass     *askpass
	done        chan struct{}
	connectedAt time.Time
	lastError   *Error
	diagnostic  string
}

// Manager owns all SSH processes started by the application.
type Manager struct {
	mu          sync.RWMutex
	sessions    map[string]*session
	runner      Runner
	credentials credential.Store
	runtimeDir  string
}

// NewManager creates a manager. runtimeDir is created with restrictive mode.
func NewManager(runner Runner, store credential.Store, runtimeDir string) (*Manager, error) {
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
	return &Manager{sessions: make(map[string]*session), runner: runner, credentials: store, runtimeDir: runtimeDir}, nil
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

// Connect starts an idempotent ControlMaster process for host.
func (m *Manager) Connect(ctx context.Context, host string, opts ConnectOptions) (Snapshot, error) {
	if err := validateHost(host); err != nil {
		return Snapshot{Host: host, Status: StatusFailed}, &Error{Code: ErrorConfiguration, Message: err.Error()}
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
	if s.status == StatusConnected || s.status == StatusConnecting {
		snap := s.snapshot()
		s.mu.Unlock()
		return snap, nil
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
	askpass, err := newAskpass(opts.Password, opts.Passphrase)
	if err != nil {
		_ = os.RemoveAll(runDir)
		return m.fail(s, &Error{Code: ErrorDependency, Message: "创建 SSH 认证交互通道失败"}, err.Error())
	}
	args := []string{"-M", "-N", "-T", "-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath=" + controlPath, host}
	spec := CommandSpec{Binary: "ssh", Args: args, Env: askpass.Env(), ExtraFiles: askpass.ExtraFiles()}
	process, err := m.runner.Start(ctx, spec)
	if err != nil {
		_ = askpass.Close()
		_ = os.RemoveAll(runDir)
		return m.fail(s, classifyError(err.Error(), "启动 SSH 失败"), sanitizeDiagnostic(err.Error(), opts.Password, opts.Passphrase))
	}
	s.mu.Lock()
	s.runDir = runDir
	s.controlPath = controlPath
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
	status, controlPath := s.status, s.controlPath
	s.mu.Unlock()
	if status != StatusConnected || controlPath == "" {
		return CommandOutput{}, &Error{Code: ErrorNotConnected, Message: "SSH 服务器尚未连接"}
	}
	runner, ok := m.runner.(outputRunner)
	if !ok {
		return CommandOutput{}, &Error{Code: ErrorDependency, Message: "SSH 执行器不支持远程命令输出"}
	}
	args := []string{"-S", controlPath, "-T", "-o", "BatchMode=yes", "--", host}
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

func (m *Manager) stopSession(ctx context.Context, s *session) {
	s.mu.Lock()
	if s.status == StatusDisconnected {
		s.mu.Unlock()
		return
	}
	s.status = StatusDisconnecting
	process, controlPath, runDir, done, helper := s.process, s.controlPath, s.runDir, s.done, s.askpass
	s.mu.Unlock()
	if controlPath != "" {
		spec := CommandSpec{Binary: "ssh", Args: []string{"-S", controlPath, "-O", "exit", s.host}}
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
	var first error
	for _, host := range hosts {
		if _, err := m.Disconnect(ctx, host); err != nil && first == nil {
			first = err
		}
	}
	return first
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
	diagnostic := sanitizeDiagnostic(process.Diagnostics(), password, passphrase)
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
		spec := CommandSpec{Binary: "ssh", Args: []string{"-S", controlPath, "-O", "check", host}}
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

func validateHost(host string) error {
	if host == "" || strings.HasPrefix(host, "-") {
		return errors.New("SSH Host 别名无效")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("*?!", r) {
			return errors.New("SSH Host 别名无效")
		}
	}
	return nil
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
	return &Error{Code: code, Message: message, Diagnostic: sanitizeDiagnostic(diagnostic)}
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
