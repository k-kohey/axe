package platform

import (
	"fmt"
	"testing"
)

// fakeLLDBRunner records Run calls for testing.
type fakeLLDBRunner struct {
	output   string
	err      error
	lastPID  int
	lastCmds []string
}

func (f *fakeLLDBRunner) Run(pid int, commands []string) (string, error) {
	f.lastPID = pid
	f.lastCmds = commands
	return f.output, f.err
}

func TestLLDBRunner_Interface(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		runner := &fakeLLDBRunner{
			output: "Process attached\nScript loaded\nDetached",
		}

		out, err := runner.Run(12345, []string{"command script import /tmp/test.py", "fetch_hierarchy /tmp/out.bplist"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "Process attached\nScript loaded\nDetached" {
			t.Errorf("unexpected output: %s", out)
		}
		if runner.lastPID != 12345 {
			t.Errorf("expected PID 12345, got %d", runner.lastPID)
		}
		if len(runner.lastCmds) != 2 {
			t.Errorf("expected 2 commands, got %d", len(runner.lastCmds))
		}
	})

	t.Run("error", func(t *testing.T) {
		runner := &fakeLLDBRunner{
			output: "error output",
			err:    fmt.Errorf("lldb failed"),
		}

		out, err := runner.Run(99, []string{"cmd"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if out != "error output" {
			t.Errorf("expected error output preserved, got %q", out)
		}
	})
}

func TestRealLLDBRunner_ImplementsInterface(t *testing.T) {
	// Compile-time check that RealLLDBRunner implements LLDBRunner.
	var _ LLDBRunner = &RealLLDBRunner{}
}
