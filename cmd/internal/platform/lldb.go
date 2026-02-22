package platform

import (
	"fmt"
	"os/exec"
	"strconv"
)

// RunLLDB executes lldb in batch mode, attaching to the given PID and running the specified commands.
// Each command is passed as a separate -o argument.
// Returns the combined stdout/stderr output and any error.
func RunLLDB(pid int, commands []string) (string, error) {
	args := []string{"-p", strconv.Itoa(pid), "--batch"}
	for _, c := range commands {
		args = append(args, "-o", c)
	}
	args = append(args, "-o", "detach", "-o", "quit")

	cmd := exec.Command("lldb", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("lldb failed: %w", err)
	}
	return string(out), nil
}
