package codegen

import "context"

// ToolchainRunner abstracts swiftc, clang, codesign, and xcrun operations for testability.
type ToolchainRunner interface {
	SDKPath(ctx context.Context, sdk string) (string, error)
	CompileSwift(ctx context.Context, args []string) ([]byte, error)
	LinkDylib(ctx context.Context, args []string) ([]byte, error)
	CompileC(ctx context.Context, args []string) ([]byte, error)
	Codesign(ctx context.Context, path string) error
}
