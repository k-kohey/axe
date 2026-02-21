package idb

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCmd implements CmdRunner for testing.
type fakeCmd struct {
	started     bool
	waited      bool
	stdoutPR    *os.File
	stdoutPW    *os.File
	onPipeReady func()
	waitCh      chan error // if set, Wait blocks until a value is sent
}

func (f *fakeCmd) StdoutPipe() (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	f.stdoutPR = pr
	f.stdoutPW = pw
	if f.onPipeReady != nil {
		f.onPipeReady()
	}
	return pr, nil
}

func (f *fakeCmd) Start() error {
	f.started = true
	return nil
}

func (f *fakeCmd) Process() *os.Process {
	// Return nil; tests that need a real process handle this differently.
	return nil
}

func (f *fakeCmd) Wait() error {
	f.waited = true
	if f.waitCh != nil {
		return <-f.waitCh
	}
	return nil
}

// fakeCommander produces fakeCmd instances.
type fakeCommander struct {
	mu        sync.Mutex
	lastCmd   *fakeCmd
	lastArgs  []string
	pipeReady chan struct{}
	commandFn func(name string, args ...string) CmdRunner // override for custom behavior
}

func newFakeCommander() *fakeCommander {
	return &fakeCommander{pipeReady: make(chan struct{})}
}

func (fc *fakeCommander) Command(name string, args ...string) CmdRunner {
	if fc.commandFn != nil {
		return fc.commandFn(name, args...)
	}
	cmd := &fakeCmd{
		onPipeReady: func() {
			close(fc.pipeReady)
		},
	}
	fc.mu.Lock()
	fc.lastCmd = cmd
	fc.lastArgs = append([]string{name}, args...)
	fc.mu.Unlock()
	return cmd
}

// writeToPipe waits for the fake command's stdout pipe to be ready,
// writes all lines, and closes the pipe.
func writeToPipe(cmdr *fakeCommander, lines ...string) {
	<-cmdr.pipeReady
	for _, line := range lines {
		_, _ = cmdr.lastCmd.stdoutPW.WriteString(line)
	}
	_ = cmdr.lastCmd.stdoutPW.Close()
}

func TestStartWith_Success(t *testing.T) {
	cmdr := newFakeCommander()

	go writeToPipe(cmdr, `{"grpc_swift_port":10882,"grpc_port":10882}`+"\n")

	companion, err := StartWith(cmdr, "UDID-123", "")
	if err != nil {
		t.Fatal(err)
	}

	if companion.Port() != "10882" {
		t.Errorf("expected port 10882, got %s", companion.Port())
	}
	if companion.Address() != "localhost:10882" {
		t.Errorf("expected address localhost:10882, got %s", companion.Address())
	}

	if !cmdr.lastCmd.started {
		t.Error("expected command to be started")
	}

	// Verify args.
	args := strings.Join(cmdr.lastArgs, " ")
	if !strings.Contains(args, "--udid UDID-123") {
		t.Errorf("expected --udid UDID-123 in args: %s", args)
	}
	if !strings.Contains(args, "--grpc-port 0") {
		t.Errorf("expected --grpc-port 0 in args: %s", args)
	}
}

func TestStartWith_EmptyPort(t *testing.T) {
	cmdr := newFakeCommander()

	go writeToPipe(cmdr, "\n")

	_, err := StartWith(cmdr, "UDID-123", "")
	if err == nil {
		t.Fatal("expected error for empty port")
	}
	if !strings.Contains(err.Error(), "did not output a port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseCompanionPort(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"valid JSON", `{"grpc_swift_port":10882,"grpc_port":10882}`, "10882"},
		{"only grpc_port", `{"grpc_port":9999}`, "9999"},
		{"port zero", `{"grpc_port":0}`, ""},
		{"empty JSON", `{}`, ""},
		{"not JSON", `IDB Companion Built at Aug 12 2022`, ""},
		{"empty string", ``, ""},
		{"log line", `Providing targets across Simulator and Device sets.`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCompanionPort(tc.line)
			if got != tc.want {
				t.Errorf("parseCompanionPort(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestStartWith_LogLinesBeforePort(t *testing.T) {
	cmdr := newFakeCommander()

	// Simulate real idb_companion output: log lines before JSON port line.
	go writeToPipe(cmdr,
		"IDB Companion Built at Aug 12 2022 08:41:50\n",
		"Providing targets across Simulator and Device sets.\n",
		`{"grpc_swift_port":12345,"grpc_port":12345}`+"\n",
	)

	companion, err := StartWith(cmdr, "UDID-456", "")
	if err != nil {
		t.Fatal(err)
	}
	if companion.Port() != "12345" {
		t.Errorf("expected port 12345, got %s", companion.Port())
	}
}

func TestStartWith_NoPortJSON(t *testing.T) {
	cmdr := newFakeCommander()

	// Only log lines, no JSON port — then EOF.
	go writeToPipe(cmdr, "some log line\n", "another log line\n")

	_, err := StartWith(cmdr, "UDID-789", "")
	if err == nil {
		t.Fatal("expected error when no port JSON is output")
	}
	if !strings.Contains(err.Error(), "did not output a port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompanion_Stop_NilProcess(t *testing.T) {
	c := &Companion{process: nil}
	if err := c.Stop(); err != nil {
		t.Errorf("Stop with nil process should not error: %v", err)
	}
}

func TestStartWith_DeviceSetPath(t *testing.T) {
	cmdr := newFakeCommander()

	go writeToPipe(cmdr, `{"grpc_swift_port":10882,"grpc_port":10882}`+"\n")

	_, err := StartWith(cmdr, "UDID-123", "/tmp/axe-devices")
	if err != nil {
		t.Fatal(err)
	}

	args := strings.Join(cmdr.lastArgs, " ")
	if !strings.Contains(args, "--device-set-path /tmp/axe-devices") {
		t.Errorf("expected --device-set-path in args: %s", args)
	}
}

func TestBootHeadlessWith_Success(t *testing.T) {
	cmdr := newFakeCommander()

	go writeToPipe(cmdr, `{"state":"Booted","udid":"ABCD-1234"}`+"\n")

	companion, err := BootHeadlessWith(cmdr, "ABCD-1234", "/tmp/axe-devices")
	if err != nil {
		t.Fatal(err)
	}
	if companion == nil {
		t.Fatal("expected non-nil companion")
	}

	args := strings.Join(cmdr.lastArgs, " ")
	if !strings.Contains(args, "--boot ABCD-1234") {
		t.Errorf("expected --boot ABCD-1234 in args: %s", args)
	}
	if !strings.Contains(args, "--headless 1") {
		t.Errorf("expected --headless 1 in args: %s", args)
	}
	if !strings.Contains(args, "--device-set-path /tmp/axe-devices") {
		t.Errorf("expected --device-set-path in args: %s", args)
	}
}

func TestBootHeadlessWith_NoBootedState(t *testing.T) {
	cmdr := newFakeCommander()

	// JSON without "state":"Booted" — then EOF.
	go writeToPipe(cmdr, `{"state":"Creating"}`+"\n")

	_, err := BootHeadlessWith(cmdr, "ABCD-1234", "")
	if err == nil {
		t.Fatal("expected error when no Booted state is reported")
	}
	if !strings.Contains(err.Error(), "did not report Booted state") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBootHeadlessWith_EmptyDeviceSetPath(t *testing.T) {
	cmdr := newFakeCommander()

	go writeToPipe(cmdr, `{"state":"Booted","udid":"XYZ"}`+"\n")

	_, err := BootHeadlessWith(cmdr, "XYZ", "")
	if err != nil {
		t.Fatal(err)
	}

	args := strings.Join(cmdr.lastArgs, " ")
	if strings.Contains(args, "--device-set-path") {
		t.Errorf("should not include --device-set-path when empty: %s", args)
	}
}

// newBlockingFakeCommander creates a fakeCommander whose Wait blocks
// until the caller sends a value on the returned channel.
func newBlockingFakeCommander() (*fakeCommander, chan error) {
	waitCh := make(chan error, 1)
	cmdr := &fakeCommander{pipeReady: make(chan struct{})}
	cmdr.commandFn = func(name string, args ...string) CmdRunner {
		cmd := &fakeCmd{
			waitCh: waitCh,
			onPipeReady: func() {
				close(cmdr.pipeReady)
			},
		}
		cmdr.mu.Lock()
		cmdr.lastCmd = cmd
		cmdr.lastArgs = append([]string{name}, args...)
		cmdr.mu.Unlock()
		return cmd
	}
	return cmdr, waitCh
}

func TestCompanionDone_ClosesOnExit(t *testing.T) {
	cmdr, waitCh := newBlockingFakeCommander()

	go writeToPipe(cmdr, `{"state":"Booted","udid":"TEST"}`+"\n")

	companion, err := BootHeadlessWith(cmdr, "TEST", "")
	if err != nil {
		t.Fatal(err)
	}

	// Done should not be closed yet (process still "running").
	select {
	case <-companion.Done():
		t.Fatal("Done() should not be closed while process is running")
	default:
	}

	// Simulate process exit.
	waitCh <- nil

	// Done should now close.
	select {
	case <-companion.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() did not close after process exited")
	}

	if companion.Err() != nil {
		t.Errorf("expected nil error, got %v", companion.Err())
	}
}

func TestCompanionDone_ReportsExitError(t *testing.T) {
	cmdr, waitCh := newBlockingFakeCommander()

	go writeToPipe(cmdr, `{"state":"Booted","udid":"TEST"}`+"\n")

	companion, err := BootHeadlessWith(cmdr, "TEST", "")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate crash.
	crashErr := fmt.Errorf("signal: killed")
	waitCh <- crashErr

	<-companion.Done()
	if companion.Err() == nil || companion.Err().Error() != "signal: killed" {
		t.Errorf("expected crash error, got %v", companion.Err())
	}
}

func TestStartWith_DoneClosesOnExit(t *testing.T) {
	cmdr, waitCh := newBlockingFakeCommander()

	go writeToPipe(cmdr, `{"grpc_swift_port":10882,"grpc_port":10882}`+"\n")

	companion, err := StartWith(cmdr, "UDID-123", "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-companion.Done():
		t.Fatal("Done() should not be closed while process is running")
	default:
	}

	waitCh <- nil

	select {
	case <-companion.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() did not close after process exited")
	}
}
