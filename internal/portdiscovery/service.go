// Package portdiscovery discovers TCP listeners through an existing SSH
// ControlMaster connection.
package portdiscovery

import (
	"context"
	"errors"
	"sync"
	"time"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

const (
	defaultRefreshInterval = 10 * time.Second
	defaultCommandTimeout  = 15 * time.Second
)

// ErrorCode is a stable classification for port discovery API consumers.
type ErrorCode string

const (
	ErrorServerNotConnected ErrorCode = "server_not_connected"
	ErrorTimeout            ErrorCode = "discovery_timeout"
	ErrorCancelled          ErrorCode = "discovery_cancelled"
	ErrorFailed             ErrorCode = "discovery_failed"
	ErrorClosed             ErrorCode = "service_closed"
)

// Error is a user-safe port discovery failure.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Executor runs a validated remote command through a connected SSH session.
type Executor interface {
	Execute(context.Context, string, []string) (sshmanager.CommandOutput, error)
}

// Snapshot is the current in-memory discovery state for one Host.
type Snapshot struct {
	Host        string    `json:"host"`
	Ports       []Port    `json:"ports"`
	RefreshedAt time.Time `json:"refreshedAt,omitempty"`
	AutoRefresh bool      `json:"autoRefresh"`
	Refreshing  bool      `json:"refreshing"`
	Diagnostics []string  `json:"diagnostics,omitempty"`
	LastError   *Error    `json:"lastError,omitempty"`
}

// Service owns discovery snapshots and auto-refresh goroutines.
type Service struct {
	mu        sync.Mutex
	states    map[string]*hostState
	executor  Executor
	interval  time.Duration
	timeout   time.Duration
	rootCtx   context.Context
	cancel    context.CancelFunc
	closed    bool
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeDone chan struct{}
}

type hostState struct {
	mu         sync.Mutex
	snapshot   Snapshot
	flight     *refreshFlight
	autoID     uint64
	autoCancel context.CancelFunc
}

type refreshFlight struct {
	done     chan struct{}
	snapshot Snapshot
	err      error
}

// NewService creates a discovery service with a 10-second auto-refresh
// interval and a bounded remote command timeout.
func NewService(executor Executor) (*Service, error) {
	if executor == nil {
		return nil, errors.New("端口发现执行器不能为空")
	}
	return newService(executor, defaultRefreshInterval, defaultCommandTimeout), nil
}

func newService(executor Executor, interval, timeout time.Duration) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		states:    make(map[string]*hostState),
		executor:  executor,
		interval:  interval,
		timeout:   timeout,
		rootCtx:   ctx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}
}

// Snapshot returns a copy of the current state for host.
func (s *Service) Snapshot(host string) Snapshot {
	s.mu.Lock()
	state := s.states[host]
	s.mu.Unlock()
	if state == nil {
		return Snapshot{Host: host, Ports: []Port{}}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return cloneSnapshot(state.snapshot)
}

// Refresh performs one discovery. Concurrent refreshes for one Host share the
// same in-flight result; different Hosts may run in parallel.
func (s *Service) Refresh(ctx context.Context, host string) (Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := s.stateFor(host)
	if err != nil {
		return Snapshot{Host: host, Ports: []Port{}}, err
	}
	state.mu.Lock()
	if state.flight != nil {
		flight := state.flight
		state.mu.Unlock()
		select {
		case <-flight.done:
			return cloneSnapshot(flight.snapshot), flight.err
		case <-ctx.Done():
			return Snapshot{Host: host, Ports: []Port{}}, contextError(ctx)
		}
	}
	flight := &refreshFlight{done: make(chan struct{})}
	state.flight = flight
	state.snapshot.Refreshing = true
	state.mu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closedErr := &Error{Code: ErrorClosed, Message: "端口发现服务已关闭"}
		snapshot, operationErr := s.finishRefresh(state, flight, nil, closedErr)
		return snapshot, operationErr
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	ports, diagnostics, discoverErr := s.discover(ctx, host)
	snapshot, operationErr := s.finishRefresh(state, flight, &refreshResult{ports: ports, diagnostics: diagnostics}, discoverErr)
	return snapshot, operationErr
}

// SetAutoRefresh enables or disables the server-side 10-second refresh loop.
func (s *Service) SetAutoRefresh(host string, enabled bool) (Snapshot, error) {
	state, err := s.stateFor(host)
	if err != nil {
		return Snapshot{Host: host, Ports: []Port{}}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Snapshot{Host: host, Ports: []Port{}}, &Error{Code: ErrorClosed, Message: "端口发现服务已关闭"}
	}
	s.mu.Unlock()
	state.mu.Lock()
	if !enabled {
		cancel := state.autoCancel
		state.autoCancel = nil
		state.snapshot.AutoRefresh = false
		state.autoID++
		snapshot := cloneSnapshot(state.snapshot)
		state.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return snapshot, nil
	}
	if state.autoCancel != nil {
		snapshot := cloneSnapshot(state.snapshot)
		state.mu.Unlock()
		return snapshot, nil
	}
	autoCtx, cancel := context.WithCancel(s.rootCtx)
	state.autoID++
	autoID := state.autoID
	state.autoCancel = cancel
	state.snapshot.AutoRefresh = true
	snapshot := cloneSnapshot(state.snapshot)
	state.mu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		state.mu.Lock()
		if state.autoID == autoID {
			state.autoCancel = nil
			state.snapshot.AutoRefresh = false
		}
		state.mu.Unlock()
		return Snapshot{Host: host, Ports: []Port{}}, &Error{Code: ErrorClosed, Message: "端口发现服务已关闭"}
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go s.autoRefresh(autoCtx, host, state, autoID)
	return snapshot, nil
}

// Close cancels auto-refresh and any in-flight discovery operation.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		states := make([]*hostState, 0, len(s.states))
		for _, state := range s.states {
			states = append(states, state)
		}
		s.mu.Unlock()
		for _, state := range states {
			state.mu.Lock()
			cancel := state.autoCancel
			state.autoCancel = nil
			state.snapshot.AutoRefresh = false
			state.autoID++
			state.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		s.wg.Wait()
		close(s.closeDone)
	})
	<-s.closeDone
	return nil
}

type refreshResult struct {
	ports       []Port
	diagnostics []string
}

func (s *Service) stateFor(host string) (*hostState, error) {
	if host == "" {
		return nil, &Error{Code: ErrorFailed, Message: "SSH Host 不能为空"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: ErrorClosed, Message: "端口发现服务已关闭"}
	}
	state := s.states[host]
	if state == nil {
		state = &hostState{snapshot: Snapshot{Host: host, Ports: []Port{}}}
		s.states[host] = state
	}
	return state, nil
}

func (s *Service) finishRefresh(state *hostState, flight *refreshFlight, result *refreshResult, operationErr error) (Snapshot, error) {
	state.mu.Lock()
	if operationErr == nil && result != nil {
		state.snapshot.Ports = clonePorts(result.ports)
		state.snapshot.RefreshedAt = time.Now()
		state.snapshot.Diagnostics = append([]string(nil), result.diagnostics...)
		state.snapshot.LastError = nil
	} else if operationErr != nil && shouldRecordError(operationErr) {
		state.snapshot.LastError = publicError(operationErr)
	}
	state.snapshot.Refreshing = false
	snapshot := cloneSnapshot(state.snapshot)
	if state.flight == flight {
		state.flight = nil
	}
	flight.snapshot = snapshot
	flight.err = operationErr
	close(flight.done)
	state.mu.Unlock()
	return snapshot, operationErr
}

func (s *Service) discover(ctx context.Context, host string) ([]Port, []string, error) {
	operationCtx, cancel := context.WithTimeout(ctx, s.timeout)
	stop := context.AfterFunc(s.rootCtx, cancel)
	defer stop()
	defer cancel()
	primary, primaryErr := s.executor.Execute(operationCtx, host, []string{"ss", "-ltnp"})
	if primaryErr == nil {
		ports, diagnostics := Parse(primary.Stdout)
		if len(ports) == 0 || hasProcess(ports) {
			return ports, diagnostics, nil
		}
	}
	if primaryErr != nil && shouldSkipFallback(primaryErr) {
		return nil, nil, publicError(primaryErr)
	}
	fallback, fallbackErr := s.executor.Execute(operationCtx, host, []string{"ss", "-ltn"})
	if fallbackErr != nil {
		return nil, nil, publicError(fallbackErr)
	}
	ports, diagnostics := Parse(fallback.Stdout)
	return ports, diagnostics, nil
}

func (s *Service) autoRefresh(ctx context.Context, host string, state *hostState, autoID uint64) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := s.Refresh(ctx, host)
			if errorCode(err) == ErrorServerNotConnected {
				s.stopAutoRefresh(state, autoID)
				return
			}
		}
	}
}

func (s *Service) stopAutoRefresh(state *hostState, autoID uint64) {
	state.mu.Lock()
	if state.autoID != autoID {
		state.mu.Unlock()
		return
	}
	state.snapshot.AutoRefresh = false
	state.autoCancel = nil
	state.mu.Unlock()
}

func hasProcess(ports []Port) bool {
	for _, port := range ports {
		if port.Process != "" {
			return true
		}
	}
	return false
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Ports = clonePorts(snapshot.Ports)
	snapshot.Diagnostics = append([]string(nil), snapshot.Diagnostics...)
	if snapshot.LastError != nil {
		lastError := *snapshot.LastError
		snapshot.LastError = &lastError
	}
	return snapshot
}

func clonePorts(ports []Port) []Port {
	if len(ports) == 0 {
		return []Port{}
	}
	return append([]Port(nil), ports...)
}

func contextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Message: "远程端口探测超时"}
	}
	return &Error{Code: ErrorCancelled, Message: "用户取消了远程端口探测"}
}

func shouldSkipFallback(err error) bool {
	code := errorCode(err)
	return code == ErrorTimeout || code == ErrorCancelled || code == ErrorClosed
}

func shouldRecordError(err error) bool {
	code := errorCode(err)
	return code != ErrorCancelled && code != ErrorClosed
}

func errorCode(err error) ErrorCode {
	var discoveryErr *Error
	if errors.As(err, &discoveryErr) {
		return discoveryErr.Code
	}
	var sshErr *sshmanager.Error
	if errors.As(err, &sshErr) {
		switch sshErr.Code {
		case sshmanager.ErrorNotConnected:
			return ErrorServerNotConnected
		case sshmanager.ErrorTimeout:
			return ErrorTimeout
		case sshmanager.ErrorCancelled:
			return ErrorCancelled
		default:
			return ErrorFailed
		}
	}
	return ""
}

func publicError(err error) *Error {
	if err == nil {
		return nil
	}
	switch errorCode(err) {
	case ErrorServerNotConnected:
		return &Error{Code: ErrorServerNotConnected, Message: "SSH 服务器尚未连接"}
	case ErrorTimeout:
		return &Error{Code: ErrorTimeout, Message: "远程端口探测超时"}
	case ErrorCancelled:
		return &Error{Code: ErrorCancelled, Message: "用户取消了远程端口探测"}
	case ErrorClosed:
		return &Error{Code: ErrorClosed, Message: "端口发现服务已关闭"}
	default:
		return &Error{Code: ErrorFailed, Message: "远程端口探测失败"}
	}
}
