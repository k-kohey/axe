package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/buildlock"
	"howett.net/plist"
)

func terminateApp(ctx context.Context, bs *build.Settings, device, deviceSetPath string, ar AppRunner) {
	if err := ar.Terminate(ctx, device, bs.BundleID, deviceSetPath); err != nil {
		slog.Debug("terminate app (may not be running)", "err", err)
	}
}

// resolveAppBundle locates the .app bundle in the build products directory.
// It first checks BuiltProductsDir (configuration-specific), then falls back
// to a glob across all configuration directories.
func resolveAppBundle(bs *build.Settings, dirs previewDirs) (string, error) {
	appName := bs.ModuleName + ".app"
	srcAppPath := filepath.Join(bs.BuiltProductsDir, appName)

	if _, err := os.Stat(srcAppPath); err != nil {
		pattern := filepath.Join(dirs.Build, "Build", "Products", "*", appName)
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			return "", fmt.Errorf("app bundle not found: %s", srcAppPath)
		}
		srcAppPath = matches[0]
		slog.Debug("Found app bundle via glob", "path", srcAppPath)
	}
	return srcAppPath, nil
}

// stageAppBundle copies the .app bundle from dirs.Build to dirs.Staging
// under LOCK_SH to protect against concurrent xcodebuild writes.
func stageAppBundle(ctx context.Context, bs *build.Settings, dirs previewDirs, fc FileCopier) (string, error) {
	lock := buildlock.New(dirs.Build)
	if err := lock.RLock(ctx); err != nil {
		return "", fmt.Errorf("acquiring read lock: %w", err)
	}
	defer lock.RUnlock()

	srcAppPath, err := resolveAppBundle(bs, dirs)
	if err != nil {
		return "", err
	}

	// Copy the app bundle to the session-specific staging directory so we
	// can modify Info.plist without touching the original build artifacts
	// and without colliding with other preview sessions.
	if err := os.MkdirAll(dirs.Staging, 0o755); err != nil {
		return "", fmt.Errorf("creating staging directory: %w", err)
	}
	stagedAppPath := filepath.Join(dirs.Staging, filepath.Base(srcAppPath))
	_ = os.RemoveAll(stagedAppPath)
	if err := fc.CopyDir(ctx, srcAppPath, stagedAppPath); err != nil {
		return "", fmt.Errorf("copying app bundle to staging: %w", err)
	}

	return stagedAppPath, nil
}

func installApp(ctx context.Context, bs *build.Settings, dirs previewDirs, device, deviceSetPath string, ar AppRunner, fc FileCopier) (string, error) {
	// Stage the app bundle under shared lock (reads dirs.Build).
	stagedAppPath, err := stageAppBundle(ctx, bs, dirs, fc)
	if err != nil {
		return "", err
	}

	// Remaining operations only touch dirs.Staging — no lock needed.
	// Rewrite BundleID and display name so the preview app doesn't
	// overwrite the original app on the simulator.
	rewriteInfoPlist(
		filepath.Join(stagedAppPath, "Info.plist"),
		bs.BundleID,
		"axe "+bs.ModuleName,
	)

	if err := ar.Install(ctx, device, stagedAppPath, deviceSetPath); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}

	return stagedAppPath, nil
}

// rewriteInfoPlist overwrites CFBundleIdentifier and CFBundleDisplayName
// in the given Info.plist file. Errors are logged as warnings without
// failing the build — the subsequent simctl install/launch will simply
// use the original values.
func rewriteInfoPlist(plistPath, bundleID, displayName string) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		slog.Warn("Failed to read Info.plist", "path", plistPath, "err", err)
		return
	}

	var info map[string]any
	if _, err := plist.Unmarshal(data, &info); err != nil {
		slog.Warn("Failed to decode Info.plist", "path", plistPath, "err", err)
		return
	}

	info["CFBundleIdentifier"] = bundleID
	info["CFBundleDisplayName"] = displayName

	out, err := plist.Marshal(info, plist.XMLFormat)
	if err != nil {
		slog.Warn("Failed to encode Info.plist", "err", err)
		return
	}

	if err := os.WriteFile(plistPath, out, 0o600); err != nil {
		slog.Warn("Failed to write Info.plist", "path", plistPath, "err", err)
	}
}

// launchWithHotReload launches the app with both the loader dylib and the
// initial thunk dylib injected, plus the socket path for hot-reload communication.
func launchWithHotReload(ctx context.Context, bs *build.Settings, loaderPath, thunkPath, socketPath string, device, deviceSetPath string, ar AppRunner) error {
	insertLibs := loaderPath + ":" + thunkPath

	env := map[string]string{
		"SIMCTL_CHILD_DYLD_INSERT_LIBRARIES":   insertLibs,
		"SIMCTL_CHILD_AXE_PREVIEW_SOCKET_PATH": socketPath,
		"SIMCTL_CHILD_SWIFTUI_VIEW_DEBUG":      "287",
	}

	return ar.Launch(ctx, device, bs.BundleID, deviceSetPath, env, nil)
}
