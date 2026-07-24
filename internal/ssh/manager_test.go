package ssh

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangjianyong66/ssh-tunnel-manager/internal/credential"
)

type fakeProcess struct {
	done chan struct{}
	once sync.Once
	err  error
	diag string
}

func newFakeProcess() *fakeProcess            { return &fakeProcess{done: make(chan struct{})} }
func (p *fakeProcess) Wait() error            { <-p.done; return p.err }
func (p *fakeProcess) Signal(os.Signal) error { p.once.Do(func() { close(p.done) }); return nil }
func (p *fakeProcess) Diagnostics() string    { return p.diag }
func (p *fakeProcess) finish(err error, diagnostic string) {
	p.err, p.diag = err, diagnostic
	p.once.Do(func() { close(p.done) })
}

type fakeRunner struct {
	mu        sync.Mutex
	specs     []CommandSpec
	runs      []CommandSpec
	outputs   []CommandSpec
	process   *fakeProcess
	startErr  error
	runErr    error
	result    CommandOutput
	outputErr error
}

type forwardRunner struct {
	mu             sync.Mutex
	specs          []CommandSpec
	runs           []CommandSpec
	master         *fakeProcess
	forward        *fakeProcess
	checkFailures  map[int]error
	checkStarted   chan struct{}
	releaseCheck   chan struct{}
	checkCallCount int
}

func (r *forwardRunner) Start(_ context.Context, spec CommandSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, spec)
	if containsArg(spec.Args, "-L") {
		if r.forward == nil {
			r.forward = newFakeProcess()
		}
		return r.forward, nil
	}
	if r.master == nil {
		r.master = newFakeProcess()
	}
	return r.master, nil
}

func (r *forwardRunner) Run(_ context.Context, spec CommandSpec) error {
	r.mu.Lock()
	r.runs = append(r.runs, spec)
	isCheck := containsArg(spec.Args, "check")
	if !isCheck {
		r.mu.Unlock()
		return nil
	}
	r.checkCallCount++
	call := r.checkCallCount
	err := r.checkFailures[call]
	started, release := r.checkStarted, r.releaseCheck
	r.mu.Unlock()
	if started != nil && call == 3 {
		close(started)
		<-release
	}
	return err
}

func containsArg(args []string, wanted string) bool {
	for _, argument := range args {
		if argument == wanted {
			return true
		}
	}
	return false
}

type multiProcessRunner struct {
	mu        sync.Mutex
	processes map[string]*fakeProcess
	starts    map[string]int
}

func newMultiProcessRunner() *multiProcessRunner {
	return &multiProcessRunner{processes: make(map[string]*fakeProcess), starts: make(map[string]int)}
}

func (r *multiProcessRunner) Start(_ context.Context, spec CommandSpec) (Process, error) {
	host := spec.Args[len(spec.Args)-1]
	r.mu.Lock()
	defer r.mu.Unlock()
	process := newFakeProcess()
	r.processes[host] = process
	r.starts[host]++
	return process, nil
}

func (r *multiProcessRunner) Run(_ context.Context, _ CommandSpec) error { return nil }

type unavailableCredentialStore struct{}

func (unavailableCredentialStore) Lookup(context.Context, credential.Ref) (string, error) {
	return "", credential.ErrUnavailable
}
func (unavailableCredentialStore) Save(context.Context, credential.Ref, string) error {
	return credential.ErrUnavailable
}
func (unavailableCredentialStore) Delete(context.Context, credential.Ref) error {
	return credential.ErrUnavailable
}

func (r *fakeRunner) Start(_ context.Context, spec CommandSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, spec)
	if r.startErr != nil {
		return nil, r.startErr
	}
	if r.process == nil {
		r.process = newFakeProcess()
	}
	return r.process, nil
}

func (r *fakeRunner) Run(_ context.Context, spec CommandSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, spec)
	return r.runErr
}

func (r *fakeRunner) Output(_ context.Context, spec CommandSpec) (CommandOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputs = append(r.outputs, spec)
	return r.result, r.outputErr
}

func TestManagerConnectDisconnectAndSecretIsolation(t *testing.T) {
	runner := &fakeRunner{process: newFakeProcess()}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := "do-not-leak"
	snapshot, err := manager.Connect(context.Background(), "server-a", ConnectOptions{Password: secret})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusConnected {
		t.Fatalf("status = %q", snapshot.Status)
	}
	joined := strings.Join(append(append([]string{}, runner.specs[0].Args...), runner.specs[0].Env...), " ")
	if strings.Contains(joined, secret) {
		t.Fatal("secret leaked into command args or environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err = manager.Disconnect(ctx, "server-a")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusDisconnected {
		t.Fatalf("status after disconnect = %q", snapshot.Status)
	}
	foundExit := false
	for _, spec := range runner.runs {
		if strings.Contains(strings.Join(spec.Args, " "), "-O exit") {
			foundExit = true
		}
	}
	if !foundExit {
		t.Fatalf("missing exact control exit: %#v", runner.runs)
	}
}

func TestManagerConnectIsIdempotent(t *testing.T) {
	runner := &fakeRunner{process: newFakeProcess()}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(context.Background(), "server-a", ConnectOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(context.Background(), "server-a", ConnectOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("start count = %d, want 1", len(runner.specs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = manager.Disconnect(ctx, "server-a")
}

func TestManagerConcurrentConnectStartsOneProcess(t *testing.T) {
	runner := &fakeRunner{process: newFakeProcess()}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, connectErr := manager.Connect(context.Background(), "server-a", ConnectOptions{}); connectErr != nil {
				t.Errorf("connect: %v", connectErr)
			}
		}()
	}
	wg.Wait()
	if len(runner.specs) != 1 {
		t.Fatalf("start count = %d, want 1", len(runner.specs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = manager.Disconnect(ctx, "server-a")
}

func TestManagerManagesTwoServersIndependently(t *testing.T) {
	runner := newMultiProcessRunner()
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, host := range []string{"server-a", "server-b"} {
		host := host
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, connectErr := manager.Connect(context.Background(), host, ConnectOptions{}); connectErr != nil {
				t.Errorf("connect %s: %v", host, connectErr)
			}
		}()
	}
	wg.Wait()
	if manager.Snapshot("server-a").Status != StatusConnected || manager.Snapshot("server-b").Status != StatusConnected {
		t.Fatalf("servers not independently connected: %#v", manager.Snapshots())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := manager.Disconnect(ctx, "server-a"); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot("server-a").Status != StatusDisconnected || manager.Snapshot("server-b").Status != StatusConnected {
		t.Fatalf("disconnect affected another server: %#v", manager.Snapshots())
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.Snapshot("server-b").Status != StatusDisconnected {
		t.Fatalf("close did not stop server-b: %#v", manager.Snapshot("server-b"))
	}
}

func TestManagerExecuteUsesConnectedControlMaster(t *testing.T) {
	runner := &fakeRunner{process: newFakeProcess(), result: CommandOutput{Stdout: "LISTEN output"}}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connected, err := manager.Connect(context.Background(), "server-a", ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(context.Background(), "server-a", []string{"ss", "-ltnp"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "LISTEN output" || len(runner.outputs) != 1 {
		t.Fatalf("execute result = %#v, specs = %#v", result, runner.outputs)
	}
	want := []string{"-S", connected.ControlPath, "-T", "-o", "BatchMode=yes", "--", "server-a", "ss", "-ltnp"}
	if got := runner.outputs[0].Args; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = manager.Disconnect(ctx, "server-a")
}

func TestManagerExecuteRejectsDisconnectedAndInvalidCommand(t *testing.T) {
	manager, err := NewManager(&fakeRunner{}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"ss", "-ltn"}, nil, {"ss", "bad\x00value"}} {
		_, executeErr := manager.Execute(context.Background(), "server-a", command)
		var sshErr *Error
		if !errors.As(executeErr, &sshErr) {
			t.Fatalf("command %#v error = %#v", command, executeErr)
		}
	}
}

func TestManagerStartLocalForwardUsesConnectedControlMaster(t *testing.T) {
	runner := &forwardRunner{}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	connected, err := manager.Connect(context.Background(), "server-a", ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	process, err := manager.StartLocalForward(context.Background(), "server-a", 18080, 8080)
	if err != nil {
		t.Fatal(err)
	}
	if process != runner.forward {
		t.Fatal("returned process is not the precise forward process")
	}
	runner.mu.Lock()
	if len(runner.specs) != 2 {
		runner.mu.Unlock()
		t.Fatalf("start count = %d, want 2", len(runner.specs))
	}
	got := append([]string(nil), runner.specs[1].Args...)
	runs := append([]CommandSpec(nil), runner.runs...)
	runner.mu.Unlock()
	want := []string{
		"-S", connected.ControlPath,
		"-N", "-T",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-L", "127.0.0.1:18080:127.0.0.1:8080",
		"--", "server-a",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	checkCount := 0
	for _, spec := range runs {
		if containsArg(spec.Args, "check") {
			checkCount++
		}
	}
	if checkCount != 3 {
		t.Fatalf("ControlMaster check count = %d, want connect + before + after", checkCount)
	}
	runner.forward.finish(nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = manager.Disconnect(ctx, "server-a")
}

func TestManagerStartLocalForwardRejectsDisconnected(t *testing.T) {
	manager, err := NewManager(&forwardRunner{}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.StartLocalForward(context.Background(), "server-a", 18080, 8080)
	var sshErr *Error
	if !errors.As(err, &sshErr) || sshErr.Code != ErrorNotConnected {
		t.Fatalf("error = %#v", err)
	}
}

func TestManagerStartLocalForwardCleansProcessWhenPostCheckFails(t *testing.T) {
	runner := &forwardRunner{checkFailures: map[int]error{3: errors.New("master disappeared")}}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(context.Background(), "server-a", ConnectOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.StartLocalForward(context.Background(), "server-a", 18080, 8080)
	var sshErr *Error
	if !errors.As(err, &sshErr) || sshErr.Code != ErrorNotConnected {
		t.Fatalf("error = %#v", err)
	}
	select {
	case <-runner.forward.done:
	default:
		t.Fatal("forward process was not precisely stopped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = manager.Disconnect(ctx, "server-a")
}

func TestManagerDisconnectWaitsForForwardStartCriticalSection(t *testing.T) {
	runner := &forwardRunner{checkStarted: make(chan struct{}), releaseCheck: make(chan struct{})}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(context.Background(), "server-a", ConnectOptions{}); err != nil {
		t.Fatal(err)
	}
	forwardDone := make(chan error, 1)
	go func() {
		_, startErr := manager.StartLocalForward(context.Background(), "server-a", 18080, 8080)
		forwardDone <- startErr
	}()
	<-runner.checkStarted
	disconnectDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = manager.Disconnect(ctx, "server-a")
		close(disconnectDone)
	}()
	select {
	case <-disconnectDone:
		t.Fatal("disconnect did not wait for forward start critical section")
	case <-time.After(20 * time.Millisecond):
	}
	close(runner.releaseCheck)
	if err := <-forwardDone; err != nil {
		t.Fatal(err)
	}
	runner.forward.finish(nil, "")
	select {
	case <-disconnectDone:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not finish")
	}
}

func TestLimitedBufferKeepsBoundedPrefix(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 || buffer.String() != "abcd" {
		t.Fatalf("written = %d, value = %q, err = %v", written, buffer.String(), err)
	}
	written, err = buffer.Write([]byte("gh"))
	if err != nil || written != 2 || buffer.String() != "abcd" {
		t.Fatalf("second write = %d, value = %q, err = %v", written, buffer.String(), err)
	}
}

func TestManagerClassifiesAndRedactsProcessFailure(t *testing.T) {
	runner := &fakeRunner{process: newFakeProcess()}
	manager, err := NewManager(runner, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := "sensitive-value"
	if _, err := manager.Connect(context.Background(), "server-a", ConnectOptions{Password: secret}); err != nil {
		t.Fatal(err)
	}
	runner.process.finish(errors.New("exit status 255"), "Permission denied: "+secret)
	deadline := time.Now().Add(time.Second)
	for manager.Snapshot("server-a").Status != StatusFailed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snapshot := manager.Snapshot("server-a")
	if snapshot.LastError == nil || snapshot.LastError.Code != ErrorAuthentication {
		t.Fatalf("last error = %#v", snapshot.LastError)
	}
	if strings.Contains(snapshot.Diagnostic, secret) || !strings.Contains(snapshot.Diagnostic, "[已隐藏]") {
		t.Fatalf("diagnostic was not redacted: %q", snapshot.Diagnostic)
	}
}

func TestManagerRejectsUnsafeHost(t *testing.T) {
	manager, err := NewManager(&fakeRunner{}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Connect(context.Background(), "-oProxyCommand=bad", ConnectOptions{})
	var sshErr *Error
	if !errors.As(err, &sshErr) || sshErr.Code != ErrorConfiguration {
		t.Fatalf("error = %#v", err)
	}
}

func TestManagerFailsBeforeStartingWhenCredentialCannotBeSaved(t *testing.T) {
	runner := &fakeRunner{}
	manager, err := NewManager(runner, unavailableCredentialStore{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := "must-not-leak"
	snapshot, err := manager.Connect(context.Background(), "server-a", ConnectOptions{Password: secret, SavePassword: true})
	var sshErr *Error
	if !errors.As(err, &sshErr) || sshErr.Code != ErrorDependency {
		t.Fatalf("error = %#v", err)
	}
	if len(runner.specs) != 0 {
		t.Fatal("SSH process started after credential persistence failed")
	}
	if strings.Contains(snapshot.Diagnostic, secret) || strings.Contains(sshErr.Message, secret) {
		t.Fatal("credential persistence error leaked secret")
	}
}

func TestClassifyErrorMatrix(t *testing.T) {
	tests := []struct {
		diagnostic string
		want       ErrorCode
	}{
		{"Host key verification failed", ErrorHostKey},
		{"Permission denied (publickey,password)", ErrorAuthentication},
		{"connect to host example port 22: No route to host", ErrorNetwork},
		{"Connection timed out", ErrorTimeout},
		{"operation cancelled", ErrorCancelled},
		{"ssh: executable file not found", ErrorDependency},
	}
	for _, test := range tests {
		if got := classifyError(test.diagnostic, "fallback").Code; got != test.want {
			t.Errorf("classifyError(%q) = %q, want %q", test.diagnostic, got, test.want)
		}
	}
}
