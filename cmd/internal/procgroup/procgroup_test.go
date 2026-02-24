package procgroup_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/procgroup"
)

// TestKillProcessGroup verifies that killing a process group terminates both
// the parent process and any children it spawned.
func TestKillProcessGroup(t *testing.T) {
	// Launch a shell that spawns a long-running child (sleep).
	// "exec" is deliberately NOT used so that "sleep" is a child of "sh".
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := procgroup.Command(ctx, "sh", "-c", "sleep 300 & wait")
	cmd.SysProcAttr.Setpgid = true // already set by Command, but be explicit

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pid := cmd.Process.Pid

	// Give the child a moment to spawn.
	time.Sleep(200 * time.Millisecond)

	// Find the child sleep process in the same process group.
	childPID := findChildSleep(t, pid)
	if childPID == 0 {
		t.Fatal("could not find child sleep process")
	}

	// Kill the entire process group.
	if err := procgroup.KillProcess(cmd.Process); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}

	// Wait for the command to finish (expected to fail with signal).
	_ = cmd.Wait()

	// Verify both parent and child are gone.
	time.Sleep(100 * time.Millisecond)
	assertProcessDead(t, pid, "parent sh")
	assertProcessDead(t, childPID, "child sleep")
}

// TestSetup verifies that Setup sets Setpgid on an existing Cmd.
func TestSetup(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	procgroup.Setup(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setup did not set Setpgid")
	}
}

// TestSignalProcess verifies that SignalProcess sends a signal to the process group.
func TestSignalProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := procgroup.Command(ctx, "sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pid := cmd.Process.Pid

	if err := procgroup.SignalProcess(cmd.Process, syscall.SIGTERM); err != nil {
		t.Fatalf("SignalProcess: %v", err)
	}

	_ = cmd.Wait()

	time.Sleep(100 * time.Millisecond)
	assertProcessDead(t, pid, "sleep")
}

// TestContextCancelKillsGroup verifies that cancelling the context kills the
// entire process group, not just the leader.
func TestContextCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cmd := procgroup.Command(ctx, "sh", "-c", "sleep 300 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pid := cmd.Process.Pid
	time.Sleep(200 * time.Millisecond)

	childPID := findChildSleep(t, pid)
	if childPID == 0 {
		t.Fatal("could not find child sleep process")
	}

	// Cancel the context — this should trigger the Cancel func that kills the group.
	cancel()

	_ = cmd.Wait()
	time.Sleep(100 * time.Millisecond)

	assertProcessDead(t, pid, "parent sh")
	assertProcessDead(t, childPID, "child sleep")
}

// findChildSleep finds a "sleep" child process whose PGID matches the given parent PID.
func findChildSleep(t *testing.T, parentPID int) int {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid,pgid,comm").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}

	parentPGID, err := syscall.Getpgid(parentPID)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		pgid, _ := strconv.Atoi(fields[1])
		comm := fields[2]

		if pgid == parentPGID && pid != parentPID && strings.Contains(comm, "sleep") {
			return pid
		}
	}
	return 0
}

func assertProcessDead(t *testing.T, pid int, label string) {
	t.Helper()
	proc, err := os.FindProcess(pid)
	if err != nil {
		return // cannot find → dead
	}
	// Signal 0 checks if process exists without actually sending a signal.
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		t.Errorf("%s (pid %d) is still alive after group kill", label, pid)
	}
}
