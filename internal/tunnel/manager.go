package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

const (
	defaultStartTimeout   = 5 * time.Second
	defaultStopTimeout    = 2 * time.Second
	defaultPollInterval   = 25 * time.Millisecond
	defaultStartAttempts  = 5
	defaultStableWindow   = 60 * time.Second
	defaultConnectTimeout = 20 * time.Second
	maxLogEntries         = 100
	maxLogBytes           = 64 << 10
	portSearchAttempts    = 32
)

var defaultRetryDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
}

type target struct {
	host       string
	remotePort uint16
}

type entry struct {
	mu              sync.Mutex
	id              string
	host            string
	remotePort      uint16
	localPort       uint16
	status          Status
	runningSince    time.Time
	reconnectCount  int
	failureAttempts int
	nextRetryAt     time.Time
	lastError       *Error
	process         sshmanager.Process
	watch           *processWatch
	ctx             context.Context
	cancel          context.CancelFunc
	recovering      bool
	logs            []LogEntry
	logBytes        int
}

type processWatch struct {
	done       chan struct{}
	err        error
	diagnostic string
}

type connectFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	err     error
	waiters int
}

func watchProcess(process sshmanager.Process) *processWatch {
	watch := &processWatch{done: make(chan struct{})}
	go func() {
		watch.err = process.Wait()
		watch.diagnostic = process.Diagnostics()
		close(watch.done)
	}()
	return watch
}

// Manager owns tunnel identities, local port reservations and precise process
// handles. A Manager must not be reused after Close.
type Manager struct {
	mu        sync.Mutex
	byID      map[string]*entry
	byTarget  map[target]*entry
	reserved  map[uint16]struct{}
	closed    bool
	starter   Starter
	connector Connector
	connects  map[string]*connectFlight
	ctx       context.Context
	cancel    context.CancelFunc

	startTimeout   time.Duration
	stopTimeout    time.Duration
	pollInterval   time.Duration
	maxAttempts    int
	retryDelays    []time.Duration
	stableWindow   time.Duration
	connectTimeout time.Duration
	listen         func(string, string) (net.Listener, error)
	probe          func(context.Context, uint16) bool
	newID          func() (string, error)
	now            func() time.Time
}

// NewManager creates an in-memory tunnel manager.

func NewManager(starter Starter, connectors ...Connector) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		byID:           make(map[string]*entry),
		byTarget:       make(map[target]*entry),
		reserved:       make(map[uint16]struct{}),
		connects:       make(map[string]*connectFlight),
		starter:        starter,
		ctx:            ctx,
		cancel:         cancel,
		startTimeout:   defaultStartTimeout,
		stopTimeout:    defaultStopTimeout,
		pollInterval:   defaultPollInterval,
		maxAttempts:    defaultStartAttempts,
		retryDelays:    append([]time.Duration(nil), defaultRetryDelays...),
		stableWindow:   defaultStableWindow,
		connectTimeout: defaultConnectTimeout,
		listen:         net.Listen,
		newID:          randomID,
		now:            time.Now,
	}
	if len(connectors) > 0 {
		manager.connector = connectors[0]
	}
	manager.probe = manager.probePort
	return manager
}

// Create starts or returns the idempotent tunnel for host and remotePort.
// A failed tunnel keeps its ID and is rebuilt by a later Create call.
func (m *Manager) Create(ctx context.Context, host string, remotePort uint16) (Snapshot, error) {
	if host == "" || remotePort == 0 {
		return Snapshot{}, &Error{Code: ErrorInvalid, Message: "隧道目标无效"}
	}
	key := target{host: host, remotePort: remotePort}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return Snapshot{}, serviceClosedError()
		}
		current := m.byTarget[key]
		if current == nil {
			id, err := m.newID()
			if err != nil {
				m.mu.Unlock()
				return Snapshot{}, &Error{Code: ErrorStartFailed, Message: "生成隧道标识失败"}
			}
			entryContext, cancel := context.WithCancel(m.ctx)
			current = &entry{
				id:         id,
				host:       host,
				remotePort: remotePort,
				status:     StatusStarting,
				ctx:        entryContext,
				cancel:     cancel,
				logs:       make([]LogEntry, 0),
			}
			current.mu.Lock()
			m.byID[id] = current
			m.byTarget[key] = current
			m.mu.Unlock()
			current.addLog(m.now(), "info", "开始创建 SSH 隧道", "")
			return m.createLocked(ctx, current)
		}
		m.mu.Unlock()

		current.mu.Lock()
		m.mu.Lock()
		indexed := m.byTarget[key] == current
		closed := m.closed
		m.mu.Unlock()
		if !indexed {
			current.mu.Unlock()
			continue
		}
		if closed {
			current.mu.Unlock()
			return Snapshot{}, serviceClosedError()
		}
		if current.status == StatusRunning {
			snapshot := current.snapshot()
			current.mu.Unlock()
			return snapshot, nil
		}
		if current.status == StatusWaitingReconnect || current.status == StatusReconnecting {
			snapshot := current.snapshot()
			current.mu.Unlock()
			return snapshot, nil
		}
		if current.process != nil || current.watch != nil {
			snapshot := current.snapshot()
			tunnelErr := current.lastError
			if tunnelErr == nil {
				tunnelErr = &Error{Code: ErrorStartFailed, Message: "旧隧道进程仍在清理中"}
			}
			current.mu.Unlock()
			return snapshot, tunnelErr
		}
		current.status = StatusStarting
		if current.ctx.Err() != nil {
			current.ctx, current.cancel = context.WithCancel(m.ctx)
		}
		current.localPort = 0
		current.runningSince = time.Time{}
		current.nextRetryAt = time.Time{}
		current.failureAttempts = 0
		current.lastError = nil
		current.process = nil
		current.watch = nil
		current.addLog(m.now(), "info", "用户手动重试 SSH 隧道", "")
		return m.createLocked(ctx, current)
	}
}

func (m *Manager) createLocked(ctx context.Context, current *entry) (Snapshot, error) {
	defer current.mu.Unlock()
	reconnecting := current.status == StatusReconnecting
	for attempt := 0; attempt < m.maxAttempts; attempt++ {
		if err := m.createContextError(ctx); err != nil {
			return m.failLocked(current, err)
		}
		preferredPort := current.remotePort
		if attempt > 0 {
			preferredPort = 0
		}
		localPort, err := m.reservePort(preferredPort)
		if err != nil {
			var tunnelErr *Error
			if errors.As(err, &tunnelErr) {
				return m.failLocked(current, tunnelErr)
			}
			return m.failLocked(current, &Error{Code: ErrorLocalPortUnavailable, Message: "没有可用的本地回环端口"})
		}
		current.localPort = localPort
		if m.starter == nil {
			m.releasePort(localPort)
			current.localPort = 0
			return m.failLocked(current, &Error{Code: ErrorStartFailed, Message: "SSH 转发服务不可用"})
		}
		process, startErr := m.starter.StartLocalForward(ctx, current.host, localPort, current.remotePort)
		if startErr != nil {
			m.releasePort(localPort)
			current.localPort = 0
			return m.failLocked(current, mapStartError(ctx, startErr))
		}
		watch := watchProcess(process)
		current.process = process
		current.watch = watch

		waitErr := m.waitUntilReady(ctx, localPort, watch)
		if waitErr == nil && !m.isClosed() {
			current.status = StatusRunning
			current.runningSince = m.now()
			current.nextRetryAt = time.Time{}
			current.lastError = nil
			message := "SSH 隧道已运行"
			if reconnecting {
				message = "自动重连成功"
			}
			current.addLog(m.now(), "info", message, "")
			snapshot := current.snapshot()
			go m.monitor(current, watch, localPort)
			return snapshot, nil
		}
		m.terminate(process, watch, context.Background())
		current.process = nil
		current.watch = nil
		m.releasePort(localPort)
		current.localPort = 0
		if waitErr == nil {
			return m.failLocked(current, serviceClosedError())
		}
		if waitErr.Code == ErrorLocalPortUnavailable && attempt+1 < m.maxAttempts {
			continue
		}
		current.addLog(m.now(), "error", waitErr.Message, watch.diagnostic)
		return m.failLocked(current, waitErr)
	}
	return m.failLocked(current, &Error{Code: ErrorLocalPortUnavailable, Message: "本地端口在启动时被占用"})
}

func (m *Manager) waitUntilReady(ctx context.Context, port uint16, watch *processWatch) *Error {
	readyContext, cancel := context.WithTimeout(ctx, m.startTimeout)
	defer cancel()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		if m.probe(readyContext, port) {
			select {
			case <-watch.done:
				return processExitError(watch)
			default:
				return nil
			}
		}
		select {
		case <-watch.done:
			return processExitError(watch)
		case <-readyContext.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return &Error{Code: ErrorCancelled, Message: "用户取消了隧道创建"}
			}
			return &Error{Code: ErrorTimeout, Message: "等待本地隧道监听超时"}
		case <-ticker.C:
		}
	}
}

func (m *Manager) monitor(current *entry, watch *processWatch, localPort uint16) {
	<-watch.done
	current.mu.Lock()
	if current.watch != watch {
		current.mu.Unlock()
		return
	}
	current.process = nil
	current.watch = nil
	m.releasePort(localPort)
	if current.status == StatusRunning {
		now := m.now()
		if !current.runningSince.IsZero() && now.Sub(current.runningSince) >= m.stableWindow {
			current.failureAttempts = 0
		}
		current.runningSince = time.Time{}
		current.status = StatusWaitingReconnect
		current.lastError = &Error{Code: ErrorStartFailed, Message: "SSH 隧道意外结束，正在等待自动重连"}
		current.addLog(now, "error", "SSH 隧道意外结束", watch.diagnostic)
		if !current.recovering {
			current.recovering = true
			current.mu.Unlock()
			go m.reconnect(current)
			return
		}
	}
	current.mu.Unlock()
}

func (m *Manager) reconnect(current *entry) {
	defer func() {
		current.mu.Lock()
		current.recovering = false
		current.nextRetryAt = time.Time{}
		current.mu.Unlock()
	}()
	for {
		current.mu.Lock()
		if current.ctx.Err() != nil {
			current.mu.Unlock()
			return
		}
		if current.failureAttempts >= len(m.retryDelays) {
			current.status = StatusFailed
			current.lastError = &Error{Code: ErrorStartFailed, Message: "自动重连次数已用尽，请手动重试"}
			current.addLog(m.now(), "error", "自动重连次数已用尽，需要人工处理", "")
			current.mu.Unlock()
			return
		}
		delay := m.retryDelays[current.failureAttempts]
		current.status = StatusWaitingReconnect
		current.nextRetryAt = m.now().Add(delay)
		current.addLog(m.now(), "warning", fmt.Sprintf("将在 %s 后尝试自动重连", delay), "")
		ctx := current.ctx
		host := current.host
		current.mu.Unlock()

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-timer.C:
		}

		current.mu.Lock()
		if ctx.Err() != nil || current.status != StatusWaitingReconnect {
			current.mu.Unlock()
			return
		}
		current.failureAttempts++
		current.reconnectCount++
		attempt := current.failureAttempts
		current.status = StatusReconnecting
		current.nextRetryAt = time.Time{}
		current.addLog(m.now(), "info", fmt.Sprintf("开始第 %d 次自动重连", attempt), "")
		current.mu.Unlock()

		if err := m.ensureHostConnected(ctx, host); err != nil {
			tunnelErr, retryable, diagnostic := reconnectError(ctx, err)
			current.mu.Lock()
			if ctx.Err() != nil {
				current.mu.Unlock()
				return
			}
			current.lastError = tunnelErr
			current.addLog(m.now(), "error", tunnelErr.Message, diagnostic)
			if !retryable {
				current.status = StatusFailed
				current.addLog(m.now(), "error", "自动重连需要人工处理", "")
				current.mu.Unlock()
				return
			}
			current.mu.Unlock()
			continue
		}

		current.mu.Lock()
		if ctx.Err() != nil || current.status != StatusReconnecting {
			current.mu.Unlock()
			return
		}
		_, err := m.createLocked(ctx, current)
		if err == nil {
			return
		}
		var tunnelErr *Error
		if !errors.As(err, &tunnelErr) {
			tunnelErr = &Error{Code: ErrorStartFailed, Message: "SSH 隧道自动重连失败"}
		}
		current.mu.Lock()
		if ctx.Err() != nil {
			current.mu.Unlock()
			return
		}
		current.lastError = tunnelErr
		current.addLog(m.now(), "error", tunnelErr.Message, "")
		if !retryableTunnelError(tunnelErr) {
			current.status = StatusFailed
			current.addLog(m.now(), "error", "自动重连需要人工处理", "")
			current.mu.Unlock()
			return
		}
		current.mu.Unlock()
	}
}

func (m *Manager) ensureHostConnected(ctx context.Context, host string) error {
	if m.connector == nil || m.connector.Snapshot(host).Status == sshmanager.StatusConnected {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return serviceClosedError()
	}
	flight := m.connects[host]
	if flight == nil {
		connectContext, cancel := context.WithTimeout(m.ctx, m.connectTimeout)
		flight = &connectFlight{done: make(chan struct{}), cancel: cancel}
		m.connects[host] = flight
		go m.runConnect(host, flight, connectContext)
	}
	flight.waiters++
	m.mu.Unlock()
	defer m.releaseConnect(host, flight)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		return flight.err
	}
}

func (m *Manager) runConnect(host string, flight *connectFlight, ctx context.Context) {
	_, err := m.connector.Connect(ctx, host, sshmanager.ConnectOptions{})
	flight.cancel()
	m.mu.Lock()
	flight.err = err
	close(flight.done)
	if flight.waiters == 0 && m.connects[host] == flight {
		delete(m.connects, host)
	}
	m.mu.Unlock()
}

func (m *Manager) releaseConnect(host string, flight *connectFlight) {
	m.mu.Lock()
	flight.waiters--
	if flight.waiters == 0 {
		select {
		case <-flight.done:
			if m.connects[host] == flight {
				delete(m.connects, host)
			}
		default:
			flight.cancel()
		}
	}
	m.mu.Unlock()
}

func stopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

// List returns a stable, sorted snapshot. The empty result is never nil.
func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	entries := make([]*entry, 0, len(m.byID))
	for _, current := range m.byID {
		entries = append(entries, current)
	}
	m.mu.Unlock()
	result := make([]Snapshot, 0, len(entries))
	for _, current := range entries {
		current.mu.Lock()
		result = append(result, current.snapshot())
		current.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Host != result[j].Host {
			return result[i].Host < result[j].Host
		}
		if result[i].RemotePort != result[j].RemotePort {
			return result[i].RemotePort < result[j].RemotePort
		}
		return result[i].LocalPort < result[j].LocalPort
	})
	return result
}

// Logs returns a stable copy of the bounded event log for id.
func (m *Manager) Logs(id string) ([]LogEntry, bool) {
	m.mu.Lock()
	current := m.byID[id]
	m.mu.Unlock()
	if current == nil {
		return nil, false
	}
	current.mu.Lock()
	logs := append([]LogEntry(nil), current.logs...)
	current.mu.Unlock()
	if logs == nil {
		logs = []LogEntry{}
	}
	return logs, true
}

// Stop precisely stops and removes id. Unknown IDs are treated as success.
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return serviceClosedError()
	}
	current := m.byID[id]
	m.mu.Unlock()
	if current == nil {
		return nil
	}
	current.cancel()
	return m.stopEntry(ctx, current)
}

// StopHost precisely stops all tunnels belonging to host.
func (m *Manager) StopHost(ctx context.Context, host string) error {
	m.mu.Lock()
	entries := make([]*entry, 0)
	for _, current := range m.byID {
		if current.host == host {
			entries = append(entries, current)
		}
	}
	m.mu.Unlock()
	for _, current := range entries {
		current.cancel()
	}
	m.mu.Lock()
	if flight := m.connects[host]; flight != nil {
		flight.cancel()
	}
	m.mu.Unlock()
	var first error
	for _, current := range entries {
		if err := m.stopEntry(ctx, current); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Close rejects new tunnels and stops every known tunnel.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	entries := make([]*entry, 0, len(m.byID))
	for _, current := range m.byID {
		entries = append(entries, current)
	}
	m.mu.Unlock()
	for _, current := range entries {
		current.cancel()
	}
	var first error
	for _, current := range entries {
		if err := m.stopEntry(ctx, current); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) stopEntry(ctx context.Context, current *entry) error {
	current.cancel()
	current.mu.Lock()
	defer current.mu.Unlock()
	m.mu.Lock()
	indexed := m.byID[current.id] == current
	m.mu.Unlock()
	if !indexed {
		return nil
	}
	current.status = StatusStopping
	current.runningSince = time.Time{}
	current.nextRetryAt = time.Time{}
	if current.process != nil && current.watch != nil {
		if !m.terminate(current.process, current.watch, ctx) {
			current.status = StatusFailed
			current.lastError = &Error{Code: ErrorTimeout, Message: "停止 SSH 隧道超时"}
			return current.lastError
		}
	}
	m.releasePort(current.localPort)
	m.removeEntry(current)
	current.process = nil
	current.watch = nil
	current.localPort = 0
	return nil
}

func (m *Manager) terminate(process sshmanager.Process, watch *processWatch, ctx context.Context) bool {
	_ = process.Signal(os.Interrupt)
	timer := time.NewTimer(m.stopTimeout)
	defer timer.Stop()
	select {
	case <-watch.done:
		return true
	case <-ctx.Done():
	case <-timer.C:
	}
	_ = process.Signal(os.Kill)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(m.stopTimeout)
	select {
	case <-watch.done:
		return true
	case <-timer.C:
		return false
	}
}

func (m *Manager) reservePort(preferred uint16) (uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, serviceClosedError()
	}
	if preferred != 0 {
		if _, exists := m.reserved[preferred]; !exists && m.portAvailable(preferred) {
			m.reserved[preferred] = struct{}{}
			return preferred, nil
		}
	}
	for range portSearchAttempts {
		listener, err := m.listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		address, ok := listener.Addr().(*net.TCPAddr)
		port := address.Port
		closeErr := listener.Close()
		if !ok || port < 1 || port > 65535 || closeErr != nil {
			continue
		}
		candidate := uint16(port)
		if _, exists := m.reserved[candidate]; exists {
			continue
		}
		m.reserved[candidate] = struct{}{}
		return candidate, nil
	}
	return 0, errors.New("未找到可预留的本地端口")
}

func (m *Manager) portAvailable(port uint16) bool {
	listener, err := m.listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	return listener.Close() == nil
}

func (m *Manager) probePort(ctx context.Context, port uint16) bool {
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func (m *Manager) releasePort(port uint16) {
	if port == 0 {
		return
	}
	m.mu.Lock()
	delete(m.reserved, port)
	m.mu.Unlock()
}

func (m *Manager) removeEntry(current *entry) {
	m.mu.Lock()
	if m.byID[current.id] == current {
		delete(m.byID, current.id)
	}
	key := target{host: current.host, remotePort: current.remotePort}
	if m.byTarget[key] == current {
		delete(m.byTarget, key)
	}
	m.mu.Unlock()
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *Manager) createContextError(ctx context.Context) *Error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &Error{Code: ErrorCancelled, Message: "用户取消了隧道创建"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Message: "隧道创建超时"}
	}
	if m.isClosed() {
		return serviceClosedError()
	}
	return nil
}

func (m *Manager) failLocked(current *entry, tunnelErr *Error) (Snapshot, error) {
	current.status = StatusFailed
	current.runningSince = time.Time{}
	current.nextRetryAt = time.Time{}
	current.lastError = tunnelErr
	if len(current.logs) == 0 || current.logs[len(current.logs)-1].Message != tunnelErr.Message {
		current.addLog(m.now(), "error", tunnelErr.Message, "")
	}
	return current.snapshot(), tunnelErr
}

func (current *entry) snapshot() Snapshot {
	var lastError *Error
	if current.lastError != nil {
		copy := *current.lastError
		lastError = &copy
	}
	address := ""
	if current.localPort != 0 {
		address = fmt.Sprintf("127.0.0.1:%d", current.localPort)
	}
	return Snapshot{
		ID:             current.id,
		Host:           current.host,
		RemotePort:     current.remotePort,
		LocalPort:      current.localPort,
		Address:        address,
		Status:         current.status,
		RunningSince:   optionalTime(current.runningSince),
		ReconnectCount: current.reconnectCount,
		NextRetryAt:    optionalTime(current.nextRetryAt),
		LastError:      lastError,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func (current *entry) addLog(at time.Time, level, message, diagnostic string) {
	diagnostic = strings.ToValidUTF8(strings.TrimSpace(diagnostic), "�")
	baseSize := len(level) + len(message)
	if maxDiagnostic := maxLogBytes - baseSize; maxDiagnostic < len(diagnostic) {
		if maxDiagnostic < 0 {
			maxDiagnostic = 0
		}
		diagnostic = truncateUTF8(diagnostic, maxDiagnostic)
	}
	entry := LogEntry{Time: at, Level: level, Message: message, Diagnostic: diagnostic}
	size := logEntrySize(entry)
	current.logs = append(current.logs, entry)
	current.logBytes += size
	for len(current.logs) > maxLogEntries || current.logBytes > maxLogBytes {
		current.logBytes -= logEntrySize(current.logs[0])
		current.logs = current.logs[1:]
	}
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	for len(value) > limit {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func logEntrySize(entry LogEntry) int {
	return len(entry.Level) + len(entry.Message) + len(entry.Diagnostic)
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func serviceClosedError() *Error {
	return &Error{Code: ErrorServiceClosed, Message: "隧道服务已关闭"}
}

func mapStartError(ctx context.Context, err error) *Error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &Error{Code: ErrorCancelled, Message: "用户取消了隧道创建"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Message: "隧道创建超时"}
	}
	var sshErr *sshmanager.Error
	if errors.As(err, &sshErr) {
		switch sshErr.Code {
		case sshmanager.ErrorNotConnected:
			return &Error{Code: ErrorServerNotConnected, Message: "SSH 服务器尚未连接"}
		case sshmanager.ErrorCancelled:
			return &Error{Code: ErrorCancelled, Message: "用户取消了隧道创建"}
		case sshmanager.ErrorTimeout:
			return &Error{Code: ErrorTimeout, Message: "SSH 隧道启动超时"}
		}
	}
	return &Error{Code: ErrorStartFailed, Message: "SSH 隧道启动失败"}
}

func reconnectError(ctx context.Context, err error) (*Error, bool, string) {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return &Error{Code: ErrorCancelled, Message: "自动重连已取消"}, false, ""
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: ErrorTimeout, Message: "SSH 自动重连超时"}, true, ""
	}
	var tunnelErr *Error
	if errors.As(err, &tunnelErr) {
		return tunnelErr, false, ""
	}
	var sshErr *sshmanager.Error
	if !errors.As(err, &sshErr) {
		return &Error{Code: ErrorStartFailed, Message: "SSH 自动重连失败"}, true, ""
	}
	retryable := true
	message := sshErr.Message
	switch sshErr.Code {
	case sshmanager.ErrorAuthentication, sshmanager.ErrorCredentialRequired:
		retryable = false
		message = "SSH 认证需要人工处理，请手动连接"
	case sshmanager.ErrorHostKey:
		retryable = false
		message = "主机密钥校验失败，请先使用系统 SSH 核验指纹"
	case sshmanager.ErrorConfiguration:
		retryable = false
		message = "SSH 配置错误，请检查 Host 配置"
	case sshmanager.ErrorDependency:
		retryable = false
		message = "本机 SSH 依赖不可用，请人工处理"
	case sshmanager.ErrorCancelled:
		retryable = false
		message = "自动重连已取消"
	}
	return &Error{Code: ErrorServerNotConnected, Message: message}, retryable, sshErr.Diagnostic
}

func retryableTunnelError(err *Error) bool {
	if err == nil {
		return true
	}
	switch err.Code {
	case ErrorServerNotConnected, ErrorTimeout, ErrorStartFailed:
		return true
	default:
		return false
	}
}

func processExitError(watch *processWatch) *Error {
	diagnostic := strings.ToLower(watch.diagnostic)
	if strings.Contains(diagnostic, "address already in use") || strings.Contains(diagnostic, "cannot listen to port") {
		return &Error{Code: ErrorLocalPortUnavailable, Message: "本地端口在启动时被占用"}
	}
	return &Error{Code: ErrorStartFailed, Message: "SSH 隧道启动失败"}
}
