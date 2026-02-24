package platform

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/k-kohey/axe/internal/procgroup"
)

// LLDBRunner abstracts LLDB execution for testability.
type LLDBRunner interface {
	Run(pid int, commands []string) (string, error)
}

// RealLLDBRunner executes real lldb commands.
type RealLLDBRunner struct{}

func (r *RealLLDBRunner) Run(pid int, commands []string) (string, error) {
	return runLLDBCommand(pid, commands)
}

// runLLDBCommand executes lldb in batch mode, attaching to the given PID and running the specified commands.
func runLLDBCommand(pid int, commands []string) (string, error) {
	args := []string{"-p", strconv.Itoa(pid), "--batch"}
	for _, c := range commands {
		args = append(args, "-o", c)
	}
	args = append(args, "-o", "detach", "-o", "quit")

	cmd := exec.Command("lldb", args...)
	procgroup.Setup(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("lldb failed: %w", err)
	}
	return string(out), nil
}

// RunLLDB executes lldb in batch mode using the default runner.
// Each command is passed as a separate -o argument.
// Returns the combined stdout/stderr output and any error.
func RunLLDB(pid int, commands []string) (string, error) {
	return (&RealLLDBRunner{}).Run(pid, commands)
}
