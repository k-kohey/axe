package preview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildRunner abstracts xcodebuild operations for testability.
type BuildRunner interface {
	FetchBuildSettings(ctx context.Context, args []string) ([]byte, error)
	Build(ctx context.Context, args []string) ([]byte, error)
}

// ToolchainRunner abstracts swiftc, clang, codesign, and xcrun operations for testability.
type ToolchainRunner interface {
	SDKPath(ctx context.Context, sdk string) (string, error)
	CompileSwift(ctx context.Context, args []string) ([]byte, error)
	LinkDylib(ctx context.Context, args []string) ([]byte, error)
	CompileC(ctx context.Context, args []string) ([]byte, error)
	Codesign(ctx context.Context, path string) error
}

// AppRunner abstracts simctl app operations (terminate, install, launch) for testability.
type AppRunner interface {
	Terminate(ctx context.Context, device, bundleID, deviceSetPath string) error
	Install(ctx context.Context, device, appPath, deviceSetPath string) error
	Launch(ctx context.Context, device, bundleID, deviceSetPath string, env map[string]string, args []string) error
}

// FileCopier abstracts file copy operations for testability.
type FileCopier interface {
	CopyDir(ctx context.Context, src, dst string) error
}

// SourceLister abstracts git-based source file listing for testability.
type SourceLister interface {
	SwiftFiles(ctx context.Context, root string) ([]string, error)
	SwiftDirs(ctx context.Context, root string) ([]string, error)
}

// Compile-time interface compliance checks.
var (
	_ BuildRunner     = (*RealBuildRunner)(nil)
	_ ToolchainRunner = (*RealToolchainRunner)(nil)
	_ AppRunner       = (*RealAppRunner)(nil)
	_ FileCopier      = (*RealFileCopier)(nil)
	_ SourceLister    = (*RealSourceLister)(nil)
)

// --- Helpers ---

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

// --- Real implementations ---

// RealBuildRunner executes real xcodebuild commands.
type RealBuildRunner struct{}

func (r *RealBuildRunner) FetchBuildSettings(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *RealBuildRunner) Build(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

// RealToolchainRunner executes real toolchain commands (swiftc, clang, codesign, xcrun).
type RealToolchainRunner struct{}

func (r *RealToolchainRunner) SDKPath(ctx context.Context, sdk string) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "--sdk", sdk, "--show-sdk-path").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealToolchainRunner) CompileSwift(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *RealToolchainRunner) LinkDylib(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *RealToolchainRunner) CompileC(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}

func (r *RealToolchainRunner) Codesign(ctx context.Context, path string) error {
	out, err := exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// RealAppRunner executes real simctl app commands.
type RealAppRunner struct{}

func (r *RealAppRunner) Terminate(ctx context.Context, device, bundleID, deviceSetPath string) error {
	out, err := simctlCmd(ctx, deviceSetPath, "terminate", device, bundleID).CombinedOutput()
	if err != nil {
		// terminate may fail if app is not running — this is acceptable.
		// Include output for diagnostic purposes (callers log, not propagate).
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func (r *RealAppRunner) Install(ctx context.Context, device, appPath, deviceSetPath string) error {
	out, err := simctlCmd(ctx, deviceSetPath, "install", device, appPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func (r *RealAppRunner) Launch(ctx context.Context, device, bundleID, deviceSetPath string, env map[string]string, args []string) error {
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

// RealFileCopier executes real file copy commands.
type RealFileCopier struct{}

func (r *RealFileCopier) CopyDir(ctx context.Context, src, dst string) error {
	out, err := exec.CommandContext(ctx, "cp", "-a", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copying directory: %w\n%s", err, out)
	}
	return nil
}

// RealSourceLister uses git to list source files.
type RealSourceLister struct{}

func (r *RealSourceLister) SwiftFiles(ctx context.Context, root string) ([]string, error) {
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

func (r *RealSourceLister) SwiftDirs(ctx context.Context, root string) ([]string, error) {
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
