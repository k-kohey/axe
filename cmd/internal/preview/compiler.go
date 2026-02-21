package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
)

// replacementModuleName generates the module name for a thunk dylib, matching
// Xcode's convention: {Module}_PreviewReplacement_{FileName}_{N}.
func replacementModuleName(moduleName, sourceFileName string, counter int) string {
	base := strings.TrimSuffix(filepath.Base(sourceFileName), filepath.Ext(sourceFileName))
	return fmt.Sprintf("%s_PreviewReplacement_%s_%d", moduleName, base, counter)
}

func compileThunk(ctx context.Context, thunkPath string, bs *buildSettings, dirs previewDirs, reloadCounter int, sourceFileName string) (string, error) {
	sdkPath, err := exec.CommandContext(ctx, "xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Output()
	if err != nil {
		return "", fmt.Errorf("getting simulator SDK path: %w", err)
	}
	sdk := strings.TrimSpace(string(sdkPath))
	target := fmt.Sprintf("arm64-apple-ios%s-simulator", bs.DeploymentTarget)

	replacementModule := replacementModuleName(bs.ModuleName, sourceFileName, reloadCounter)

	objPath := filepath.Join(dirs.Thunk, fmt.Sprintf("thunk_%d.o", reloadCounter))
	dylibPath := filepath.Join(dirs.Thunk, fmt.Sprintf("thunk_%d.dylib", reloadCounter))

	// .swift -> .o
	compileArgs := []string{
		"xcrun", "swiftc",
		"-enforce-exclusivity=checked",
		"-DDEBUG",
		"-sdk", sdk,
		"-target", target,
		"-enable-testing",
		"-I", bs.BuiltProductsDir,
		"-F", bs.BuiltProductsDir,
		"-c", thunkPath,
		"-o", objPath,
		"-module-name", replacementModule,
		"-parse-as-library",
		"-Onone",
		"-gline-tables-only",
		"-Xfrontend", "-disable-previous-implementation-calls-in-dynamic-replacements",
		"-Xfrontend", "-disable-modules-validate-system-headers",
	}
	for _, p := range bs.ExtraIncludePaths {
		compileArgs = append(compileArgs, "-I", p)
	}
	for _, p := range bs.ExtraFrameworkPaths {
		compileArgs = append(compileArgs, "-F", p)
	}
	for _, p := range bs.ExtraModuleMapFiles {
		compileArgs = append(compileArgs, "-Xcc", "-fmodule-map-file="+p)
	}
	if bs.SwiftVersion != "" {
		// SWIFT_VERSION may be "6.0" but -swift-version expects "6", "5", "4.2", etc.
		sv := bs.SwiftVersion
		if strings.HasSuffix(sv, ".0") && sv != "4.0" {
			sv = strings.TrimSuffix(sv, ".0")
		}
		compileArgs = append(compileArgs, "-swift-version", sv)
	}
	slog.Debug("Compiling thunk .swift -> .o", "args", compileArgs)
	if out, err := exec.CommandContext(ctx, compileArgs[0], compileArgs[1:]...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("compiling thunk.swift -> .o: %w\n%s", err, out)
	}

	// .o -> .dylib
	linkArgs := []string{
		"xcrun", "swiftc",
		"-emit-library",
		"-sdk", sdk,
		"-target", target,
		"-I", bs.BuiltProductsDir,
		"-F", bs.BuiltProductsDir,
		"-L", bs.BuiltProductsDir,
		"-Xlinker", "-undefined",
		"-Xlinker", "suppress",
		"-Xlinker", "-flat_namespace",
		"-module-name", replacementModule,
		objPath,
		"-o", dylibPath,
	}
	slog.Debug("Linking thunk .o -> .dylib", "args", linkArgs)
	if out, err := exec.CommandContext(ctx, linkArgs[0], linkArgs[1:]...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("linking thunk.o -> .dylib: %w\n%s", err, out)
	}

	// codesign
	if out, err := exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", dylibPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("codesigning dylib: %w\n%s", err, out)
	}

	slog.Debug("Thunk dylib ready", "path", dylibPath)
	return dylibPath, nil
}
