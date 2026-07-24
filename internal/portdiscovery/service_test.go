package portdiscovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sshmanager "github.com/zhangjianyong66/ssh-tunnel-manager/internal/ssh"
)

const outputWithProcess = "LISTEN 0 128 127.0.0.1:8080 0.0.0.0:* users:((\"node\",pid=10,fd=3))\n"

type executorCall struct {
	host    string
	command []string
}

type fakeExecutor struct {
	mu    sync.Mutex
	calls []executorCall
	run   func(context.Context, string, []string) (sshmanager.CommandOutput, error)
}

func (f *fakeExecutor) Execute(ctx context.Context, host string, command []string) (sshmanager.CommandOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, executorCall{host: host, command: append([]string(nil), command...)})
	run := f.run
	f.mu.Unlock()
	return run(ctx, host, command)
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestRefreshUsesProcessOutputWithoutFallback(t *testing.T) {
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{Stdout: outputWithProcess}, nil
	}}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	snapshot, err := service.Refresh(context.Background(), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ports) != 1 || snapshot.Ports[0].Process != "node" || snapshot.RefreshedAt.IsZero() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if executor.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", executor.callCount())
	}
}

func TestRefreshFallsBackWhenProcessInformationUnavailable(t *testing.T) {
	executor := &fakeExecutor{}
	executor.run = func(_ context.Context, _ string, command []string) (sshmanager.CommandOutput, error) {
		if command[1] == "-ltnp" {
			return sshmanager.CommandOutput{Stdout: "LISTEN 0 128 *:3000 *:*\n"}, nil
		}
		return sshmanager.CommandOutput{Stdout: "LISTEN 0 128 *:3000 *:*\nLISTEN 0 128 [::]:4000 [::]:*\n"}, nil
	}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	snapshot, err := service.Refresh(context.Background(), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 2 || len(snapshot.Ports) != 2 {
		t.Fatalf("calls = %d, snapshot = %#v", executor.callCount(), snapshot)
	}
}

func TestRefreshFallsBackAfterPrimaryCommandFailure(t *testing.T) {
	executor := &fakeExecutor{}
	executor.run = func(_ context.Context, _ string, command []string) (sshmanager.CommandOutput, error) {
		if command[1] == "-ltnp" {
			return sshmanager.CommandOutput{Stderr: "Operation not permitted"}, errors.New("exit status 1")
		}
		return sshmanager.CommandOutput{Stdout: "LISTEN 0 128 127.0.0.1:9000 0.0.0.0:*\n"}, nil
	}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	snapshot, err := service.Refresh(context.Background(), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if executor.callCount() != 2 || len(snapshot.Ports) != 1 || snapshot.Ports[0].Number != 9000 {
		t.Fatalf("calls = %d, snapshot = %#v", executor.callCount(), snapshot)
	}
}

func TestNewServiceUsesTenSecondRefreshInterval(t *testing.T) {
	service, err := NewService(&fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if service.interval != 10*time.Second {
		t.Fatalf("interval = %s", service.interval)
	}
}

func TestFailedRefreshKeepsLastSuccessfulSnapshot(t *testing.T) {
	executor := &fakeExecutor{}
	executor.run = func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		if executor.callCount() == 1 {
			return sshmanager.CommandOutput{Stdout: outputWithProcess}, nil
		}
		return sshmanager.CommandOutput{Stderr: "remote failure"}, errors.New("exit status 1")
	}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	first, err := service.Refresh(context.Background(), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.Refresh(context.Background(), "server-a")
	if err == nil {
		t.Fatal("failed refresh returned nil error")
	}
	if len(failed.Ports) != 1 || !failed.RefreshedAt.Equal(first.RefreshedAt) || failed.LastError == nil || failed.LastError.Code != ErrorFailed {
		t.Fatalf("failed snapshot = %#v", failed)
	}
}

func TestConcurrentRefreshesForOneHostShareOneCommand(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return sshmanager.CommandOutput{Stdout: outputWithProcess}, nil
	}}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	results := make(chan error, 2)
	go func() { _, err := service.Refresh(context.Background(), "server-a"); results <- err }()
	<-started
	if snapshot := service.Snapshot("server-a"); !snapshot.Refreshing {
		t.Fatalf("in-flight snapshot = %#v", snapshot)
	}
	go func() { _, err := service.Refresh(context.Background(), "server-a"); results <- err }()
	time.Sleep(10 * time.Millisecond)
	if executor.callCount() != 1 {
		t.Fatalf("calls before release = %d", executor.callCount())
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if executor.callCount() != 1 {
		t.Fatalf("calls = %d, want 1", executor.callCount())
	}
}

func TestRefreshMapsDisconnectedError(t *testing.T) {
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{}, &sshmanager.Error{Code: sshmanager.ErrorNotConnected, Message: "not connected"}
	}}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	snapshot, err := service.Refresh(context.Background(), "server-a")
	var discoveryErr *Error
	if !errors.As(err, &discoveryErr) || discoveryErr.Code != ErrorServerNotConnected {
		t.Fatalf("error = %#v", err)
	}
	if snapshot.LastError == nil || snapshot.LastError.Code != ErrorServerNotConnected {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRefreshRecordsTimeoutWithoutFallback(t *testing.T) {
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{}, &sshmanager.Error{Code: sshmanager.ErrorTimeout, Message: "timeout"}
	}}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	snapshot, err := service.Refresh(context.Background(), "server-a")
	if errorCode(err) != ErrorTimeout || snapshot.LastError == nil || snapshot.LastError.Code != ErrorTimeout {
		t.Fatalf("snapshot = %#v, error = %#v", snapshot, err)
	}
	if executor.callCount() != 1 {
		t.Fatalf("timeout should not fall back, calls = %d", executor.callCount())
	}
}

func TestDifferentHostsRefreshInParallel(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	executor := &fakeExecutor{run: func(_ context.Context, host string, _ []string) (sshmanager.CommandOutput, error) {
		entered <- host
		<-release
		return sshmanager.CommandOutput{Stdout: outputWithProcess}, nil
	}}
	service := newService(executor, time.Hour, time.Second)
	defer service.Close()
	done := make(chan struct{}, 2)
	for _, host := range []string{"server-a", "server-b"} {
		go func(host string) { _, _ = service.Refresh(context.Background(), host); done <- struct{}{} }(host)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case host := <-entered:
			seen[host] = true
		case <-time.After(time.Second):
			t.Fatal("different hosts did not refresh in parallel")
		}
	}
	close(release)
	<-done
	<-done
	if !seen["server-a"] || !seen["server-b"] {
		t.Fatalf("hosts = %#v", seen)
	}
}

func TestAutoRefreshCanBeEnabledAndDisabled(t *testing.T) {
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{Stdout: outputWithProcess}, nil
	}}
	service := newService(executor, 5*time.Millisecond, time.Second)
	defer service.Close()
	snapshot, err := service.SetAutoRefresh("server-a", true)
	if err != nil || !snapshot.AutoRefresh {
		t.Fatalf("enable = %#v, %v", snapshot, err)
	}
	deadline := time.Now().Add(time.Second)
	for executor.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if executor.callCount() == 0 {
		t.Fatal("auto refresh did not run")
	}
	snapshot, err = service.SetAutoRefresh("server-a", false)
	if err != nil || snapshot.AutoRefresh {
		t.Fatalf("disable = %#v, %v", snapshot, err)
	}
	calls := executor.callCount()
	time.Sleep(20 * time.Millisecond)
	if executor.callCount() != calls {
		t.Fatalf("auto refresh continued after disable: %d -> %d", calls, executor.callCount())
	}
}

func TestAutoRefreshStopsWhenServerDisconnects(t *testing.T) {
	executor := &fakeExecutor{run: func(context.Context, string, []string) (sshmanager.CommandOutput, error) {
		return sshmanager.CommandOutput{}, &sshmanager.Error{Code: sshmanager.ErrorNotConnected, Message: "not connected"}
	}}
	service := newService(executor, 5*time.Millisecond, time.Second)
	defer service.Close()
	if _, err := service.SetAutoRefresh("server-a", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for service.Snapshot("server-a").AutoRefresh && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := service.Snapshot("server-a"); snapshot.AutoRefresh || snapshot.LastError == nil || snapshot.LastError.Code != ErrorServerNotConnected {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestCloseCancelsInFlightRefresh(t *testing.T) {
	started := make(chan struct{})
	executor := &fakeExecutor{run: func(ctx context.Context, _ string, _ []string) (sshmanager.CommandOutput, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		return sshmanager.CommandOutput{}, &sshmanager.Error{Code: sshmanager.ErrorCancelled, Message: "cancelled"}
	}}
	service := newService(executor, time.Hour, time.Hour)
	done := make(chan error, 1)
	go func() { _, err := service.Refresh(context.Background(), "server-a"); done <- err }()
	<-started
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if errorCode(err) != ErrorCancelled {
			t.Fatalf("refresh error = %#v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not stop")
	}
}
