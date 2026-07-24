package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

type fakeProcess struct {
	mu          sync.Mutex
	done        chan struct{}
	once        sync.Once
	err         error
	diagnostic  string
	waitCalls   int
	interrupts  int
	kills       int
	stopSignals bool
	stopOnKill  bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{done: make(chan struct{})}
}

func (p *fakeProcess) Wait() error {
	p.mu.Lock()
	p.waitCalls++
	p.mu.Unlock()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *fakeProcess) Signal(signal os.Signal) error {
	p.mu.Lock()
	if signal == os.Interrupt {
		p.interrupts++
	} else if signal == os.Kill {
		p.kills++
	}
	stop := p.stopSignals || (signal == os.Kill && p.stopOnKill)
	p.mu.Unlock()
	if stop {
		p.finish(nil, "")
	}
	return nil
}

func (p *fakeProcess) Diagnostics() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.diagnostic
}

func (p *fakeProcess) finish(err error, diagnostic string) {
	p.mu.Lock()
	p.err = err
	p.diagnostic = diagnostic
	p.mu.Unlock()
	p.once.Do(func() { close(p.done) })
}

type startCall struct {
	host       string
	localPort  uint16
	remotePort uint16
	process    *fakeProcess
}

type fakeStarter struct {
	mu          sync.Mutex
	calls       []startCall
	startErr    error
	diagnostics []string
	ready       bool
	block       chan struct{}
}

type fakeConnector struct {
	mu           sync.Mutex
	status       sshmanager.Status
	connectErr   error
	connectCalls int
	connectBlock chan struct{}
}

func (c *fakeConnector) Snapshot(host string) sshmanager.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return sshmanager.Snapshot{Host: host, Status: c.status}
}

func (c *fakeConnector) Connect(ctx context.Context, host string, _ sshmanager.ConnectOptions) (sshmanager.Snapshot, error) {
	c.mu.Lock()
	c.connectCalls++
	block := c.connectBlock
	err := c.connectErr
	c.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return sshmanager.Snapshot{Host: host, Status: sshmanager.StatusFailed}, ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return sshmanager.Snapshot{Host: host, Status: sshmanager.StatusFailed}, err
	}
	c.status = sshmanager.StatusConnected
	return sshmanager.Snapshot{Host: host, Status: c.status}, nil
}

func (s *fakeStarter) StartLocalForward(ctx context.Context, host string, localPort, remotePort uint16) (sshmanager.Process, error) {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return nil, s.startErr
	}
	process := newFakeProcess()
	call := startCall{host: host, localPort: localPort, remotePort: remotePort, process: process}
	s.calls = append(s.calls, call)
	index := len(s.calls) - 1
	if index < len(s.diagnostics) && s.diagnostics[index] != "" {
		process.finish(errors.New("exit status 255"), s.diagnostics[index])
	}
	return process, nil
}

func testManager(starter *fakeStarter) *Manager {
	manager := NewManager(starter)
	manager.startTimeout = 100 * time.Millisecond
	manager.stopTimeout = 50 * time.Millisecond
	manager.pollInterval = time.Millisecond
	manager.probe = func(context.Context, uint16) bool { return starter.ready }
	return manager
}

func TestCreateUsesPreferredPortAndReturnsLoopbackAddress(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusRunning || snapshot.LocalPort != 8080 || snapshot.Address != "127.0.0.1:8080" || snapshot.RunningSince == nil || snapshot.NextRetryAt != nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	starter.mu.Lock()
	call := starter.calls[0]
	starter.mu.Unlock()
	if call.host != "server-a" || call.localPort != 8080 || call.remotePort != 8080 {
		t.Fatalf("start call = %#v", call)
	}
	call.process.stopSignals = true
	if err := manager.Stop(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFallsBackWhenPreferredPortIsOccupied(t *testing.T) {
	listener, err := netListenLoopback(8080)
	if err != nil {
		t.Skipf("cannot occupy preferred port: %v", err)
	}
	defer listener.Close()
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LocalPort == 8080 || snapshot.LocalPort == 0 {
		t.Fatalf("fallback local port = %d", snapshot.LocalPort)
	}
	starter.mu.Lock()
	starter.calls[0].process.stopSignals = true
	starter.mu.Unlock()
	_ = manager.Stop(context.Background(), snapshot.ID)
}

func TestConcurrentCreateStartsOneProcess(t *testing.T) {
	starter := &fakeStarter{ready: true, block: make(chan struct{})}
	manager := testManager(starter)
	const count = 12
	results := make(chan Snapshot, count)
	errorsFound := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snapshot, err := manager.Create(context.Background(), "server-a", 8080)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- snapshot
		}()
	}
	close(starter.block)
	wg.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var id string
	for snapshot := range results {
		if id == "" {
			id = snapshot.ID
		}
		if snapshot.ID != id || snapshot.Status != StatusRunning {
			t.Fatalf("non-idempotent snapshot = %#v, id = %q", snapshot, id)
		}
	}
	starter.mu.Lock()
	if len(starter.calls) != 1 {
		starter.mu.Unlock()
		t.Fatalf("start count = %d, want 1", len(starter.calls))
	}
	process := starter.calls[0].process
	starter.mu.Unlock()
	process.stopSignals = true
	_ = manager.Stop(context.Background(), id)
}

func TestCreateRetriesOnlyLocalBindFailure(t *testing.T) {
	starter := &fakeStarter{ready: false, diagnostics: []string{"bind [127.0.0.1]:8080: Address already in use", ""}}
	manager := testManager(starter)
	manager.probe = func(_ context.Context, _ uint16) bool {
		starter.mu.Lock()
		defer starter.mu.Unlock()
		return len(starter.calls) >= 2
	}
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	if len(starter.calls) != 2 {
		starter.mu.Unlock()
		t.Fatalf("start count = %d, want 2", len(starter.calls))
	}
	firstPort := starter.calls[0].localPort
	secondPort := starter.calls[1].localPort
	process := starter.calls[1].process
	starter.mu.Unlock()
	if firstPort != 8080 || secondPort == firstPort {
		t.Fatalf("retry ports = %d then %d", firstPort, secondPort)
	}
	process.stopSignals = true
	_ = manager.Stop(context.Background(), snapshot.ID)
}

func TestUnexpectedExitAutomaticallyRebuildsEntry(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	manager.retryDelays = []time.Duration{time.Millisecond}
	first, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	firstProcess := starter.calls[0].process
	starter.mu.Unlock()
	firstProcess.finish(errors.New("exit status 255"), "secret raw output")
	waitForReconnectCount(t, manager, first.ID, 1)
	second := manager.List()[0]
	if second.ID != first.ID || second.Status != StatusRunning || second.ReconnectCount != 1 {
		t.Fatalf("automatically rebuilt snapshot = %#v, first = %#v", second, first)
	}
	starter.mu.Lock()
	secondProcess := starter.calls[1].process
	starter.mu.Unlock()
	secondProcess.stopSignals = true
	_ = manager.Stop(context.Background(), second.ID)
}

func TestReconnectSharesOneHostConnectionAcrossTunnels(t *testing.T) {
	starter := &fakeStarter{ready: true}
	connector := &fakeConnector{status: sshmanager.StatusConnected, connectBlock: make(chan struct{})}
	manager := NewManager(starter, connector)
	manager.startTimeout = 100 * time.Millisecond
	manager.pollInterval = time.Millisecond
	manager.probe = func(context.Context, uint16) bool { return true }
	manager.retryDelays = []time.Duration{time.Millisecond}
	first, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(context.Background(), "server-a", 9090)
	if err != nil {
		t.Fatal(err)
	}
	connector.mu.Lock()
	connector.status = sshmanager.StatusFailed
	connector.mu.Unlock()
	starter.mu.Lock()
	firstProcess := starter.calls[0].process
	secondProcess := starter.calls[1].process
	starter.mu.Unlock()
	firstProcess.finish(errors.New("connection lost"), "connection reset")
	secondProcess.finish(errors.New("connection lost"), "connection reset")
	waitForConnectWaiters(t, manager, "server-a", 2)
	close(connector.connectBlock)
	waitForReconnectCount(t, manager, first.ID, 1)
	waitForReconnectCount(t, manager, second.ID, 1)
	connector.mu.Lock()
	connectCalls := connector.connectCalls
	connector.mu.Unlock()
	if connectCalls != 1 {
		t.Fatalf("host connect calls = %d, want 1", connectCalls)
	}
	starter.mu.Lock()
	for _, call := range starter.calls[2:] {
		call.process.stopSignals = true
	}
	starter.mu.Unlock()
	_ = manager.Close(context.Background())
}

func TestReconnectStopsImmediatelyForAuthenticationFailure(t *testing.T) {
	starter := &fakeStarter{ready: true}
	connector := &fakeConnector{
		status:     sshmanager.StatusConnected,
		connectErr: &sshmanager.Error{Code: sshmanager.ErrorAuthentication, Message: "SSH 认证失败", Diagnostic: "permission denied"},
	}
	manager := NewManager(starter, connector)
	manager.startTimeout = 100 * time.Millisecond
	manager.pollInterval = time.Millisecond
	manager.probe = func(context.Context, uint16) bool { return true }
	manager.retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	connector.mu.Lock()
	connector.status = sshmanager.StatusFailed
	connector.mu.Unlock()
	starter.mu.Lock()
	starter.calls[0].process.finish(errors.New("connection lost"), "connection reset")
	starter.mu.Unlock()
	waitForStatus(t, manager, snapshot.ID, StatusFailed)
	failed := manager.List()[0]
	if failed.ReconnectCount != 1 || failed.LastError == nil || !strings.Contains(failed.LastError.Message, "人工") {
		t.Fatalf("failed snapshot = %#v", failed)
	}
	connector.mu.Lock()
	connectCalls := connector.connectCalls
	connector.mu.Unlock()
	if connectCalls != 1 {
		t.Fatalf("connect calls = %d", connectCalls)
	}
}

func TestReconnectExhaustsBoundedAttempts(t *testing.T) {
	starter := &fakeStarter{ready: true}
	connector := &fakeConnector{
		status:     sshmanager.StatusConnected,
		connectErr: &sshmanager.Error{Code: sshmanager.ErrorNetwork, Message: "无法连接到 SSH 服务器"},
	}
	manager := NewManager(starter, connector)
	manager.startTimeout = 100 * time.Millisecond
	manager.pollInterval = time.Millisecond
	manager.probe = func(context.Context, uint16) bool { return true }
	manager.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	connector.mu.Lock()
	connector.status = sshmanager.StatusFailed
	connector.mu.Unlock()
	starter.mu.Lock()
	starter.calls[0].process.finish(errors.New("connection lost"), "connection reset")
	starter.mu.Unlock()
	waitForStatus(t, manager, snapshot.ID, StatusFailed)
	failed := manager.List()[0]
	if failed.ReconnectCount != 5 || failed.LastError == nil || !strings.Contains(failed.LastError.Message, "次数") {
		t.Fatalf("failed snapshot = %#v", failed)
	}
}

func TestStopCancelsPendingReconnectAndClearsLogs(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	manager.retryDelays = []time.Duration{time.Hour}
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	starter.calls[0].process.finish(errors.New("connection lost"), "connection reset")
	starter.mu.Unlock()
	waitForStatus(t, manager, snapshot.ID, StatusWaitingReconnect)
	if logs, ok := manager.Logs(snapshot.ID); !ok || len(logs) == 0 {
		t.Fatalf("logs before stop = %#v, ok = %v", logs, ok)
	}
	if err := manager.Stop(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Logs(snapshot.ID); ok {
		t.Fatal("logs remained after stop")
	}
	starter.mu.Lock()
	callCount := len(starter.calls)
	starter.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("start calls after cancelled retry = %d", callCount)
	}
}

func TestTunnelLogsAreBoundedByCountAndBytes(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	current := manager.byID[snapshot.ID]
	manager.mu.Unlock()
	current.mu.Lock()
	for index := range 130 {
		current.addLog(time.Unix(int64(index), 0), "info", fmt.Sprintf("event-%03d", index), strings.Repeat("x", 800))
	}
	current.mu.Unlock()
	logs, ok := manager.Logs(snapshot.ID)
	if !ok || len(logs) > maxLogEntries || len(logs) == 0 {
		t.Fatalf("bounded logs count = %d, ok = %v", len(logs), ok)
	}
	total := 0
	for _, entry := range logs {
		total += logEntrySize(entry)
	}
	if total > maxLogBytes || logs[len(logs)-1].Message != "event-129" {
		t.Fatalf("bounded logs bytes = %d, last = %#v", total, logs[len(logs)-1])
	}
	starter.mu.Lock()
	starter.calls[0].process.stopSignals = true
	starter.mu.Unlock()
	_ = manager.Stop(context.Background(), snapshot.ID)
}

func TestStableRunRestoresReconnectBudget(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	manager.retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	manager.stableWindow = time.Minute
	var clockMu sync.Mutex
	clock := time.Unix(1_700_000_000, 0)
	manager.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	starter.calls[0].process.finish(errors.New("connection lost"), "connection reset")
	starter.mu.Unlock()
	waitForReconnectCount(t, manager, snapshot.ID, 1)
	clockMu.Lock()
	clock = clock.Add(time.Minute + time.Second)
	clockMu.Unlock()
	starter.mu.Lock()
	starter.calls[1].process.finish(errors.New("connection lost"), "connection reset")
	starter.mu.Unlock()
	waitForReconnectCount(t, manager, snapshot.ID, 2)
	manager.mu.Lock()
	current := manager.byID[snapshot.ID]
	manager.mu.Unlock()
	current.mu.Lock()
	failureAttempts := current.failureAttempts
	current.mu.Unlock()
	if failureAttempts != 1 {
		t.Fatalf("failure attempts after stable run = %d, want 1", failureAttempts)
	}
	starter.mu.Lock()
	starter.calls[2].process.stopSignals = true
	starter.mu.Unlock()
	_ = manager.Stop(context.Background(), snapshot.ID)
}

func TestStopHostAndCloseUsePreciseProcessesOnce(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	first, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create(context.Background(), "server-b", 9090)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	firstProcess := starter.calls[0].process
	secondProcess := starter.calls[1].process
	starter.mu.Unlock()
	firstProcess.stopSignals = true
	secondProcess.stopSignals = true
	if err := manager.StopHost(context.Background(), "server-a"); err != nil {
		t.Fatal(err)
	}
	list := manager.List()
	if len(list) != 1 || list[0].Host != "server-b" || list[0].ID == first.ID {
		t.Fatalf("list after StopHost = %#v", list)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("list after Close = %#v", manager.List())
	}
	for _, process := range []*fakeProcess{firstProcess, secondProcess} {
		process.mu.Lock()
		waitCalls, interrupts := process.waitCalls, process.interrupts
		process.mu.Unlock()
		if waitCalls != 1 || interrupts != 1 {
			t.Fatalf("wait calls = %d, interrupts = %d", waitCalls, interrupts)
		}
	}
	_, err = manager.Create(context.Background(), "server-c", 7070)
	var tunnelErr *Error
	if !errors.As(err, &tunnelErr) || tunnelErr.Code != ErrorServiceClosed {
		t.Fatalf("create after close error = %#v", err)
	}
}

func TestStopEscalatesAfterContextCancellation(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	snapshot, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	process := starter.calls[0].process
	starter.mu.Unlock()
	process.stopOnKill = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Stop(ctx, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	process.mu.Lock()
	waitCalls, interrupts, kills := process.waitCalls, process.interrupts, process.kills
	process.mu.Unlock()
	if waitCalls != 1 || interrupts != 1 || kills != 1 {
		t.Fatalf("wait calls = %d, interrupts = %d, kills = %d", waitCalls, interrupts, kills)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("list after stop = %#v", manager.List())
	}
}

func TestCreateMapsCancellationAndStartErrors(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		starter := &fakeStarter{ready: false}
		manager := testManager(starter)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		snapshot, err := manager.Create(ctx, "server-a", 8080)
		assertTunnelError(t, err, ErrorCancelled)
		if snapshot.Status != StatusFailed {
			t.Fatalf("snapshot = %#v", snapshot)
		}
	})
	t.Run("not connected", func(t *testing.T) {
		starter := &fakeStarter{startErr: &sshmanager.Error{Code: sshmanager.ErrorNotConnected, Message: "not connected"}}
		manager := testManager(starter)
		_, err := manager.Create(context.Background(), "server-a", 8080)
		assertTunnelError(t, err, ErrorServerNotConnected)
	})
}

func waitForStatus(t *testing.T, manager *Manager, id string, status Status) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, snapshot := range manager.List() {
			if snapshot.ID == id && snapshot.Status == status {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tunnel %s did not reach %s: %#v", id, status, manager.List())
}

func waitForReconnectCount(t *testing.T, manager *Manager, id string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, snapshot := range manager.List() {
			if snapshot.ID == id && snapshot.Status == StatusRunning && snapshot.ReconnectCount == count {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tunnel %s did not reach reconnect count %d: %#v", id, count, manager.List())
}

func waitForConnectWaiters(t *testing.T, manager *Manager, host string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		flight := manager.connects[host]
		waiters := 0
		if flight != nil {
			waiters = flight.waiters
		}
		manager.mu.Unlock()
		if waiters == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("host %s did not reach %d connection waiters", host, count)
}

func assertTunnelError(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var tunnelErr *Error
	if !errors.As(err, &tunnelErr) || tunnelErr.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
}

func netListenLoopback(port uint16) (interface {
	Close() error
}, error) {
	return net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
}
