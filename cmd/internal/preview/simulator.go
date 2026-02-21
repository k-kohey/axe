package preview

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"howett.net/plist"
)

// simctlCmd builds an exec.Cmd for "xcrun simctl" with optional --set for
// custom device sets. When deviceSetPath is non-empty, "--set <path>" is
// inserted right after "simctl".
func simctlCmd(deviceSetPath string, args ...string) *exec.Cmd {
	base := []string{"simctl"}
	if deviceSetPath != "" {
		base = append(base, "--set", deviceSetPath)
	}
	return exec.Command("xcrun", append(base, args...)...)
}

func terminateApp(bs *buildSettings, device, deviceSetPath string) {
	out, err := simctlCmd(deviceSetPath, "terminate", device, bs.BundleID).CombinedOutput()
	if err != nil {
		slog.Debug("terminate app (may not be running)", "err", err, "out", string(out))
	}
}

func installApp(bs *buildSettings, dirs previewDirs, device, deviceSetPath string) (string, error) {

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

	// Copy the app bundle to the staging directory so we can modify
	// Info.plist without touching the original build artifacts.
	stagedAppPath := filepath.Join(dirs.Root, appName)
	_ = os.RemoveAll(stagedAppPath)
	if out, err := exec.Command("cp", "-a", srcAppPath, stagedAppPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copying app bundle to staging: %w\n%s", err, out)
	}

	// Rewrite BundleID and display name so the preview app doesn't
	// overwrite the original app on the simulator.
	rewriteInfoPlist(
		filepath.Join(stagedAppPath, "Info.plist"),
		bs.BundleID,
		"axe "+bs.ModuleName,
	)

	if out, err := simctlCmd(deviceSetPath, "install", device, stagedAppPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("simctl install failed: %w\n%s", err, out)
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

	var info map[string]interface{}
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

	if err := os.WriteFile(plistPath, out, 0o644); err != nil {
		slog.Warn("Failed to write Info.plist", "path", plistPath, "err", err)
	}
}

// launchWithHotReload launches the app with both the loader dylib and the
// initial thunk dylib injected, plus the socket path for hot-reload communication.
func launchWithHotReload(bs *buildSettings, loaderPath, thunkPath, socketPath string, device, deviceSetPath string) error {

	insertLibs := loaderPath + ":" + thunkPath

	launchCmd := simctlCmd(deviceSetPath, "launch", device, bs.BundleID)
	launchCmd.Env = append(os.Environ(),
		"SIMCTL_CHILD_DYLD_INSERT_LIBRARIES="+insertLibs,
		"SIMCTL_CHILD_AXE_PREVIEW_SOCKET_PATH="+socketPath,
		"SIMCTL_CHILD_SWIFTUI_VIEW_DEBUG=287",
	)
	launchCmd.Stdout = os.Stdout
	launchCmd.Stderr = os.Stderr

	if err := launchCmd.Run(); err != nil {
		return fmt.Errorf("simctl launch failed: %w", err)
	}

	return nil
}
