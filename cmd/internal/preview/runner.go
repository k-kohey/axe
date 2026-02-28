package preview

import (
	"context"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/runner"
)

// BuildRunner is an alias for build.Runner, kept for backward compatibility.
type BuildRunner = build.Runner

// ToolchainRunner abstracts swiftc, clang, codesign, and xcrun operations for testability.
type ToolchainRunner interface {
	SDKPath(ctx context.Context, sdk string) (string, error)
	CompileSwift(ctx context.Context, args []string) ([]byte, error)
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
	_ build.Runner    = (*runner.Build)(nil)
	_ ToolchainRunner = (*runner.Toolchain)(nil)
	_ AppRunner       = (*runner.App)(nil)
	_ FileCopier      = (*runner.FileCopy)(nil)
	_ SourceLister    = (*runner.SourceList)(nil)
)
