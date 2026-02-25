// Package procgroup provides helpers for launching external processes in their
// own process group. This ensures that when a process is killed, all of its
// children are killed too — preventing zombie process accumulation.
//
// Go's exec.CommandContext sends SIGKILL only to the top-level process,
// leaving grandchildren alive. By setting Setpgid and killing the negative
// PGID, we terminate the entire process tree.
package procgroup

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Command creates an *exec.Cmd bound to the given context with process-group
// isolation. When the context is cancelled the entire process group (not just
// the leader) receives SIGKILL.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Setup(cmd)
	cmd.Cancel = func() error {
		return killProcessGroup(cmd.Process)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// Setup configures an existing *exec.Cmd to start in its own process group.
// Use this for commands that are not context-based (no Cancel override).
// The caller is responsible for calling KillProcess when done.
func Setup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillProcess sends SIGKILL to the entire process group led by p.
func KillProcess(p *os.Process) error {
	return killProcessGroup(p)
}

// SignalProcess sends the given signal to the entire process group led by p.
func SignalProcess(p *os.Process, sig syscall.Signal) error {
	if p == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.Pid)
	if err != nil {
		return p.Signal(sig)
	}
	return syscall.Kill(-pgid, sig)
}

func killProcessGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.Pid)
	if err != nil {
		// Process may have already exited; fall back to direct kill.
		return p.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
