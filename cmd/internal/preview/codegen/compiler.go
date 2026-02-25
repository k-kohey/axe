package codegen

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/k-kohey/axe/internal/preview/buildlock"
)

// ReplacementModuleName generates the module name for a thunk dylib, matching
// Xcode's convention: {Module}_PreviewReplacement_{FileName}_{N}.
func ReplacementModuleName(moduleName, sourceFileName string, counter int) string {
	base := strings.TrimSuffix(filepath.Base(sourceFileName), filepath.Ext(sourceFileName))
	return fmt.Sprintf("%s_PreviewReplacement_%s_%d", moduleName, base, counter)
}

// CompileThunk compiles multiple thunk Swift files into a signed dylib.
// The compile and link steps are unified into a single swiftc invocation
// that takes multiple .swift files and produces a .dylib directly.
func CompileThunk(ctx context.Context, thunkPaths []string, cfg CompileConfig, thunkDir, buildDir string, reloadCounter int, sourceFileName string, tc ToolchainRunner) (string, error) {
	sdk, err := tc.SDKPath(ctx, "iphonesimulator")
	if err != nil {
		return "", fmt.Errorf("getting simulator SDK path: %w", err)
	}
	target := fmt.Sprintf("arm64-apple-ios%s-simulator", cfg.DeploymentTarget)

	replacementModule := ReplacementModuleName(cfg.ModuleName, sourceFileName, reloadCounter)

	dylibPath := filepath.Join(thunkDir, fmt.Sprintf("thunk_%d.dylib", reloadCounter))

	// Compile and link under shared lock (reads BuiltProductsDir).
	if err := compileAndLink(ctx, cfg, buildDir, sdk, target, replacementModule, thunkPaths, dylibPath, tc); err != nil {
		return "", err
	}

	// codesign only touches thunkDir — no lock needed.
	if err := tc.Codesign(ctx, dylibPath); err != nil {
		return "", fmt.Errorf("codesigning dylib: %w", err)
	}

	slog.Debug("Thunk dylib ready", "path", dylibPath)
	return dylibPath, nil
}

// compileAndLink runs a single swiftc invocation that compiles multiple .swift files
// directly into a .dylib. Uses -enable-private-imports so that per-file thunks can
// access private members via @_private(sourceFile:) imports. Each per-file thunk
// scopes its @_private import to a single source file, preventing private type collisions.
func compileAndLink(ctx context.Context, cfg CompileConfig, buildDir, sdk, target, replacementModule string, thunkPaths []string, dylibPath string, tc ToolchainRunner) error {
	lock := buildlock.New(buildDir)
	if err := lock.RLock(ctx); err != nil {
		return fmt.Errorf("acquiring read lock: %w", err)
	}
	defer lock.RUnlock()

	// .swift files -> .dylib (unified compile+link)
	args := []string{
		"xcrun", "swiftc",
		"-emit-library",
		"-enforce-exclusivity=checked",
		"-DDEBUG",
		"-sdk", sdk,
		"-target", target,
		"-enable-testing",
		"-I", cfg.BuiltProductsDir,
		"-F", cfg.BuiltProductsDir,
		"-L", cfg.BuiltProductsDir,
		"-module-name", replacementModule,
		"-parse-as-library",
		"-Onone",
		"-gline-tables-only",
		"-Xfrontend", "-disable-previous-implementation-calls-in-dynamic-replacements",
		"-Xfrontend", "-disable-modules-validate-system-headers",
		"-Xfrontend", "-enable-private-imports",
		"-Xlinker", "-undefined",
		"-Xlinker", "suppress",
		"-Xlinker", "-flat_namespace",
		"-o", dylibPath,
	}
	args = append(args, thunkPaths...)
	for _, p := range cfg.ExtraIncludePaths {
		args = append(args, "-I", p)
	}
	for _, p := range cfg.ExtraFrameworkPaths {
		args = append(args, "-F", p)
	}
	for _, p := range cfg.ExtraModuleMapFiles {
		args = append(args, "-Xcc", "-fmodule-map-file="+p)
	}
	if cfg.SwiftVersion != "" {
		// SWIFT_VERSION may be "6.0" but -swift-version expects "6", "5", "4.2", etc.
		sv := cfg.SwiftVersion
		if strings.HasSuffix(sv, ".0") && sv != "4.0" {
			sv = strings.TrimSuffix(sv, ".0")
		}
		args = append(args, "-swift-version", sv)
	}
	slog.Debug("Compiling thunk .swift -> .dylib", "args", args)
	if out, err := tc.CompileSwift(ctx, args); err != nil {
		return fmt.Errorf("compiling thunk: %w\n%s", err, out)
	}

	return nil
}
