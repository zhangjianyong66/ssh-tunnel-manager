package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
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
	if snapshot.Status != StatusRunning || snapshot.LocalPort != 8080 || snapshot.Address != "127.0.0.1:8080" {
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

func TestUnexpectedExitRetainsFailedEntryAndCreateRebuildsIt(t *testing.T) {
	starter := &fakeStarter{ready: true}
	manager := testManager(starter)
	first, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	starter.mu.Lock()
	firstProcess := starter.calls[0].process
	starter.mu.Unlock()
	firstProcess.finish(errors.New("exit status 255"), "secret raw output")
	waitForStatus(t, manager, first.ID, StatusFailed)
	failed := manager.List()[0]
	if failed.LastError == nil || failed.LastError.Code != ErrorStartFailed || failed.LastError.Message == "secret raw output" {
		t.Fatalf("failed snapshot = %#v", failed)
	}
	second, err := manager.Create(context.Background(), "server-a", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Status != StatusRunning {
		t.Fatalf("rebuilt snapshot = %#v, first = %#v", second, first)
	}
	starter.mu.Lock()
	secondProcess := starter.calls[1].process
	starter.mu.Unlock()
	secondProcess.stopSignals = true
	_ = manager.Stop(context.Background(), second.ID)
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
