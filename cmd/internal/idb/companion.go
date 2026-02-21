package idb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Commander abstracts exec.Command for testing.
type Commander interface {
	Command(name string, args ...string) CmdRunner
}

// CmdRunner abstracts *exec.Cmd methods used by Companion.
type CmdRunner interface {
	StdoutPipe() (*os.File, error)
	Start() error
	Process() *os.Process
	Wait() error
}

// Companion manages an idb_companion process.
type Companion struct {
	cmd     CmdRunner
	port    string
	process *os.Process
}

type defaultCommander struct{}

func (defaultCommander) Command(name string, args ...string) CmdRunner {
	cmd := exec.Command(name, args...)
	// Suppress idb_companion's stderr noise (objc warnings, startup logs).
	if devNull, err := os.Open(os.DevNull); err == nil {
		cmd.Stderr = devNull
	}
	return &execCmdRunner{cmd: cmd}
}

type execCmdRunner struct {
	cmd    *exec.Cmd
	stdout *os.File
}

func (r *execCmdRunner) StdoutPipe() (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	r.cmd.Stdout = pw
	r.stdout = pr
	return pr, nil
}

func (r *execCmdRunner) Start() error {
	err := r.cmd.Start()
	// Close the write end of the pipe after starting so reads see EOF when process exits.
	if r.stdout != nil {
		if pw, ok := r.cmd.Stdout.(*os.File); ok {
			_ = pw.Close()
		}
	}
	return err
}

func (r *execCmdRunner) Process() *os.Process { return r.cmd.Process }
func (r *execCmdRunner) Wait() error          { return r.cmd.Wait() }

// DefaultCommander returns the standard Commander using exec.Command.
func DefaultCommander() Commander {
	return defaultCommander{}
}

// Start launches idb_companion for the given device UDID and returns a Companion.
// It reads the assigned gRPC port from companion stdout.
// If deviceSetPath is non-empty, --device-set-path is added.
func Start(udid, deviceSetPath string) (*Companion, error) {
	return StartWith(DefaultCommander(), udid, deviceSetPath)
}

// StartWith launches idb_companion using the given Commander.
func StartWith(cmdr Commander, udid, deviceSetPath string) (*Companion, error) {
	args := []string{"--udid", udid, "--grpc-port", "0"}
	if deviceSetPath != "" {
		args = append(args, "--device-set-path", deviceSetPath)
	}
	cmd := cmdr.Command("idb_companion", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting idb_companion: %w", err)
	}

	// Read first line of stdout to get the assigned port.
	// idb_companion outputs JSON: {"grpc_swift_port":N,"grpc_port":N}
	scanner := bufio.NewScanner(stdout)
	portCh := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if port := parseCompanionPort(line); port != "" {
				portCh <- port
				return
			}
		}
		close(portCh)
	}()

	select {
	case port, ok := <-portCh:
		if !ok || port == "" {
			if proc := cmd.Process(); proc != nil {
				_ = proc.Kill()
			}
			return nil, fmt.Errorf("idb_companion did not output a port")
		}
		return &Companion{
			cmd:     cmd,
			port:    port,
			process: cmd.Process(),
		}, nil
	case <-time.After(10 * time.Second):
		if proc := cmd.Process(); proc != nil {
			_ = proc.Kill()
		}
		return nil, fmt.Errorf("timed out waiting for idb_companion port")
	}
}

// parseCompanionPort extracts the gRPC port from an idb_companion stdout line.
// The line is typically JSON like {"grpc_swift_port":N,"grpc_port":N}.
func parseCompanionPort(line string) string {
	var info struct {
		GRPCPort int `json:"grpc_port"`
	}
	if err := json.Unmarshal([]byte(line), &info); err == nil && info.GRPCPort > 0 {
		return strconv.Itoa(info.GRPCPort)
	}
	return ""
}

// Port returns the gRPC port string reported by idb_companion.
func (c *Companion) Port() string {
	return c.port
}

// Address returns the gRPC address (localhost:port) for connecting.
func (c *Companion) Address() string {
	return "localhost:" + c.port
}

// Stop gracefully stops the idb_companion process.
// Sends SIGTERM first, then SIGKILL after timeout.
func (c *Companion) Stop() error {
	if c.process == nil {
		return nil
	}

	// Send SIGTERM.
	if err := c.process.Signal(syscall.SIGTERM); err != nil {
		slog.Debug("SIGTERM failed, trying SIGKILL", "err", err)
		_ = c.process.Kill()
		return nil
	}

	// Wait up to 5 seconds for graceful exit.
	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		slog.Debug("idb_companion did not exit after SIGTERM, sending SIGKILL")
		_ = c.process.Kill()
		return nil
	}
}

// BootHeadless boots a simulator headlessly via idb_companion.
// The returned Companion's Stop() will terminate idb_companion and shut down
// the simulator automatically.
func BootHeadless(udid, deviceSetPath string) (*Companion, error) {
	return BootHeadlessWith(DefaultCommander(), udid, deviceSetPath)
}

// BootHeadlessWith boots a simulator headlessly using the given Commander.
func BootHeadlessWith(cmdr Commander, udid, deviceSetPath string) (*Companion, error) {
	args := []string{"--boot", udid, "--headless", "1"}
	if deviceSetPath != "" {
		args = append(args, "--device-set-path", deviceSetPath)
	}
	cmd := cmdr.Command("idb_companion", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting idb_companion boot: %w", err)
	}

	// Wait for JSON output confirming boot (e.g. {"state":"Booted",...}).
	scanner := bufio.NewScanner(stdout)
	bootCh := make(chan struct{}, 1)
	go func() {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var info map[string]interface{}
			if err := json.Unmarshal([]byte(line), &info); err == nil {
				if state, ok := info["state"].(string); ok && state == "Booted" {
					bootCh <- struct{}{}
					return
				}
			}
		}
		close(bootCh)
	}()

	select {
	case _, ok := <-bootCh:
		if !ok {
			if proc := cmd.Process(); proc != nil {
				_ = proc.Kill()
			}
			return nil, fmt.Errorf("idb_companion boot did not report Booted state")
		}
		return &Companion{
			cmd:     cmd,
			process: cmd.Process(),
		}, nil
	case <-time.After(120 * time.Second):
		if proc := cmd.Process(); proc != nil {
			_ = proc.Kill()
		}
		return nil, fmt.Errorf("timed out waiting for simulator boot (120s)")
	}
}
