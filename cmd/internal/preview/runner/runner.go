// Package runner provides production implementations of the runner interfaces
// defined in the parent preview package. Each type wraps external CLI tools
// (xcodebuild, swiftc, simctl, git, etc.) via os/exec.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// --- Build ---

// Build executes real xcodebuild commands.
type Build struct{}

func (r *Build) FetchBuildSettings(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *Build) Build(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

// --- Toolchain ---

// Toolchain executes real toolchain commands (swiftc, clang, codesign, xcrun).
type Toolchain struct{}

func (r *Toolchain) SDKPath(ctx context.Context, sdk string) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "--sdk", sdk, "--show-sdk-path").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Toolchain) CompileSwift(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *Toolchain) LinkDylib(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *Toolchain) CompileC(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *Toolchain) Codesign(ctx context.Context, path string) error {
	out, err := exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// --- App ---

// App executes real simctl app commands.
type App struct{}

// simctlCmd builds an exec.Cmd for "xcrun simctl" with optional --set for
// custom device sets. When deviceSetPath is non-empty, "--set <path>" is
// inserted right after "simctl".
func simctlCmd(ctx context.Context, deviceSetPath string, args ...string) *exec.Cmd {
	base := []string{"simctl"}
	if deviceSetPath != "" {
		base = append(base, "--set", deviceSetPath)
	}
	return exec.CommandContext(ctx, "xcrun", append(base, args...)...)
}

func (r *App) Terminate(ctx context.Context, device, bundleID, deviceSetPath string) error {
	out, err := simctlCmd(ctx, deviceSetPath, "terminate", device, bundleID).CombinedOutput()
	if err != nil {
		// terminate may fail if app is not running — this is acceptable.
		// Include output for diagnostic purposes (callers log, not propagate).
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func (r *App) Install(ctx context.Context, device, appPath, deviceSetPath string) error {
	out, err := simctlCmd(ctx, deviceSetPath, "install", device, appPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func (r *App) Launch(ctx context.Context, device, bundleID, deviceSetPath string, env map[string]string, args []string) error {
	launchCmd := simctlCmd(ctx, deviceSetPath, append([]string{"launch", device, bundleID}, args...)...)
	launchCmd.Env = os.Environ()
	for k, v := range env {
		launchCmd.Env = append(launchCmd.Env, k+"="+v)
	}
	launchCmd.Stdout = os.Stdout
	launchCmd.Stderr = os.Stderr
	if err := launchCmd.Run(); err != nil {
		return fmt.Errorf("simctl launch failed: %w", err)
	}
	return nil
}

// --- FileCopy ---

// FileCopy executes real file copy commands.
type FileCopy struct{}

func (r *FileCopy) CopyDir(ctx context.Context, src, dst string) error {
	out, err := exec.CommandContext(ctx, "cp", "-a", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copying directory: %w\n%s", err, out)
	}
	return nil
}

// --- SourceList ---

// SourceList uses git to list source files.
type SourceList struct{}

func (r *SourceList) SwiftFiles(ctx context.Context, root string) ([]string, error) {
	out, err := exec.CommandContext(ctx,
		"git", "-C", root, "ls-files",
		"--cached", "--others", "--exclude-standard",
		"*.swift",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		abs := filepath.Join(root, line)
		files = append(files, abs)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Swift files found")
	}
	return files, nil
}

func (r *SourceList) SwiftDirs(ctx context.Context, root string) ([]string, error) {
	out, err := exec.CommandContext(ctx,
		"git", "-C", root, "ls-files",
		"--cached", "--others", "--exclude-standard",
		"*.swift",
	).Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var dirs []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		dir := filepath.Dir(filepath.Join(root, line))
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no Swift files found")
	}
	return dirs, nil
}
