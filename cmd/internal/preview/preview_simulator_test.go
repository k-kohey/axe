package preview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Fake AppRunner ---

type fakeAppRunner struct {
	terminateErr error
	installErr   error
	launchErr    error

	// Captured args for assertions.
	terminateDevice    string
	terminateBundleID  string
	terminateDeviceSet string
	installDevice      string
	installAppPath     string
	installDeviceSet   string
	launchDevice       string
	launchBundleID     string
	launchDeviceSet    string
	launchEnv          map[string]string
	launchArgs         []string
}

func (f *fakeAppRunner) Terminate(_ context.Context, device, bundleID, deviceSetPath string) error {
	f.terminateDevice = device
	f.terminateBundleID = bundleID
	f.terminateDeviceSet = deviceSetPath
	return f.terminateErr
}

func (f *fakeAppRunner) Install(_ context.Context, device, appPath, deviceSetPath string) error {
	f.installDevice = device
	f.installAppPath = appPath
	f.installDeviceSet = deviceSetPath
	return f.installErr
}

func (f *fakeAppRunner) Launch(_ context.Context, device, bundleID, deviceSetPath string, env map[string]string, args []string) error {
	f.launchDevice = device
	f.launchBundleID = bundleID
	f.launchDeviceSet = deviceSetPath
	f.launchEnv = env
	f.launchArgs = args
	return f.launchErr
}

// --- Fake FileCopier ---

type fakeFileCopier struct {
	copyDirErr error
	copySrc    string
	copyDst    string
}

func (f *fakeFileCopier) CopyDir(_ context.Context, src, dst string) error {
	f.copySrc = src
	f.copyDst = dst
	if f.copyDirErr != nil {
		return f.copyDirErr
	}
	// Simulate a real copy by creating the destination directory.
	return os.MkdirAll(dst, 0o755)
}

// --- terminateApp tests ---

func TestTerminateApp_Success(t *testing.T) {
	t.Parallel()

	ar := &fakeAppRunner{}
	bs := &buildSettings{BundleID: "axe.com.example.TestModule"}

	terminateApp(context.Background(), bs, "device-uuid", "/device/set", ar)

	if ar.terminateDevice != "device-uuid" {
		t.Errorf("terminateDevice = %q, want %q", ar.terminateDevice, "device-uuid")
	}
	if ar.terminateBundleID != "axe.com.example.TestModule" {
		t.Errorf("terminateBundleID = %q, want %q", ar.terminateBundleID, "axe.com.example.TestModule")
	}
	if ar.terminateDeviceSet != "/device/set" {
		t.Errorf("terminateDeviceSet = %q, want %q", ar.terminateDeviceSet, "/device/set")
	}
}

func TestTerminateApp_ErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	// terminateApp logs errors but does not return them (app may not be running).
	ar := &fakeAppRunner{terminateErr: errors.New("app not running")}
	bs := &buildSettings{BundleID: "axe.com.example.TestModule"}

	// Should not panic or fail.
	terminateApp(context.Background(), bs, "device-uuid", "", ar)
}

// --- stageAppBundle tests ---

func TestStageAppBundle_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	productsDir := filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(productsDir, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &buildSettings{
		ModuleName:       "TestModule",
		BuiltProductsDir: productsDir,
	}
	stagingDir := filepath.Join(t.TempDir(), "staging")
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: stagingDir,
	}
	fc := &fakeFileCopier{}

	stagedPath, err := stageAppBundle(context.Background(), bs, dirs, fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify staged path is under staging dir.
	if !strings.HasPrefix(stagedPath, stagingDir) {
		t.Errorf("stagedPath = %q, want prefix %q", stagedPath, stagingDir)
	}

	// Verify the app name is preserved.
	if filepath.Base(stagedPath) != "TestModule.app" {
		t.Errorf("stagedPath base = %q, want %q", filepath.Base(stagedPath), "TestModule.app")
	}

	// Verify FileCopier was called with correct src.
	if fc.copySrc != appDir {
		t.Errorf("copySrc = %q, want %q", fc.copySrc, appDir)
	}
}

func TestStageAppBundle_AppNotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	bs := &buildSettings{
		ModuleName:       "NoSuchModule",
		BuiltProductsDir: filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator"),
	}
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: filepath.Join(t.TempDir(), "staging"),
	}
	fc := &fakeFileCopier{}

	_, err := stageAppBundle(context.Background(), bs, dirs, fc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "app bundle not found") {
		t.Errorf("error = %q, want to contain 'app bundle not found'", err.Error())
	}
}

func TestStageAppBundle_CopyError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	productsDir := filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(productsDir, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &buildSettings{
		ModuleName:       "TestModule",
		BuiltProductsDir: productsDir,
	}
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: filepath.Join(t.TempDir(), "staging"),
	}
	fc := &fakeFileCopier{copyDirErr: errors.New("disk full")}

	_, err := stageAppBundle(context.Background(), bs, dirs, fc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "copying app bundle to staging") {
		t.Errorf("error = %q, want to contain 'copying app bundle to staging'", err.Error())
	}
}

// --- installApp tests ---

func TestInstallApp_Success(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	productsDir := filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(productsDir, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &buildSettings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: productsDir,
	}
	stagingDir := filepath.Join(t.TempDir(), "staging")
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: stagingDir,
	}
	ar := &fakeAppRunner{}
	fc := &fakeFileCopier{}

	stagedAppPath, err := installApp(context.Background(), bs, dirs, "device-uuid", "/device/set", ar, fc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify install was called with the staged path.
	if ar.installAppPath != stagedAppPath {
		t.Errorf("installAppPath = %q, want %q", ar.installAppPath, stagedAppPath)
	}
	if ar.installDevice != "device-uuid" {
		t.Errorf("installDevice = %q, want %q", ar.installDevice, "device-uuid")
	}
	if ar.installDeviceSet != "/device/set" {
		t.Errorf("installDeviceSet = %q, want %q", ar.installDeviceSet, "/device/set")
	}
}

func TestInstallApp_InstallError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	productsDir := filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(productsDir, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &buildSettings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: productsDir,
	}
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: filepath.Join(t.TempDir(), "staging"),
	}
	ar := &fakeAppRunner{installErr: errors.New("simctl install failed")}
	fc := &fakeFileCopier{}

	_, err := installApp(context.Background(), bs, dirs, "device-uuid", "", ar, fc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "install") {
		t.Errorf("error = %q, want to contain 'install'", err.Error())
	}
}

func TestInstallApp_StageError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	// No app bundle exists -> stageAppBundle will fail.
	bs := &buildSettings{
		ModuleName:       "MissingModule",
		BuiltProductsDir: filepath.Join(tmpDir, "Build", "Products", "Debug-iphonesimulator"),
	}
	dirs := previewDirs{
		Build:   tmpDir,
		Staging: filepath.Join(t.TempDir(), "staging"),
	}
	ar := &fakeAppRunner{}
	fc := &fakeFileCopier{}

	_, err := installApp(context.Background(), bs, dirs, "device-uuid", "", ar, fc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Install should not have been called since staging failed.
	if ar.installAppPath != "" {
		t.Error("Install should not be called when staging fails")
	}
}

// --- launchWithHotReload tests ---

func TestLaunchWithHotReload_Success(t *testing.T) {
	t.Parallel()

	ar := &fakeAppRunner{}
	bs := &buildSettings{BundleID: "axe.com.example.TestModule"}

	err := launchWithHotReload(
		context.Background(), bs,
		"/path/to/loader.dylib", "/path/to/thunk.dylib", "/path/to/socket.sock",
		"device-uuid", "/device/set",
		ar,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify device and bundle ID.
	if ar.launchDevice != "device-uuid" {
		t.Errorf("launchDevice = %q, want %q", ar.launchDevice, "device-uuid")
	}
	if ar.launchBundleID != "axe.com.example.TestModule" {
		t.Errorf("launchBundleID = %q, want %q", ar.launchBundleID, "axe.com.example.TestModule")
	}
	if ar.launchDeviceSet != "/device/set" {
		t.Errorf("launchDeviceSet = %q, want %q", ar.launchDeviceSet, "/device/set")
	}

	// Verify DYLD_INSERT_LIBRARIES contains both loader and thunk.
	insertLibs := ar.launchEnv["SIMCTL_CHILD_DYLD_INSERT_LIBRARIES"]
	if !strings.Contains(insertLibs, "/path/to/loader.dylib") {
		t.Errorf("DYLD_INSERT_LIBRARIES = %q, want to contain loader path", insertLibs)
	}
	if !strings.Contains(insertLibs, "/path/to/thunk.dylib") {
		t.Errorf("DYLD_INSERT_LIBRARIES = %q, want to contain thunk path", insertLibs)
	}

	// Verify socket path env var.
	socketPath := ar.launchEnv["SIMCTL_CHILD_AXE_PREVIEW_SOCKET_PATH"]
	if socketPath != "/path/to/socket.sock" {
		t.Errorf("socket path = %q, want %q", socketPath, "/path/to/socket.sock")
	}

	// Verify SwiftUI debug flag.
	debugFlag := ar.launchEnv["SIMCTL_CHILD_SWIFTUI_VIEW_DEBUG"]
	if debugFlag != "287" {
		t.Errorf("SWIFTUI_VIEW_DEBUG = %q, want %q", debugFlag, "287")
	}
}

func TestLaunchWithHotReload_Error(t *testing.T) {
	t.Parallel()

	ar := &fakeAppRunner{launchErr: errors.New("simctl launch failed")}
	bs := &buildSettings{BundleID: "axe.com.example.TestModule"}

	err := launchWithHotReload(
		context.Background(), bs,
		"/path/to/loader.dylib", "/path/to/thunk.dylib", "/path/to/socket.sock",
		"device-uuid", "",
		ar,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLaunchWithHotReload_InsertLibsFormat(t *testing.T) {
	t.Parallel()

	ar := &fakeAppRunner{}
	bs := &buildSettings{BundleID: "axe.com.example.TestModule"}

	err := launchWithHotReload(
		context.Background(), bs,
		"/loader.dylib", "/thunk.dylib", "/socket.sock",
		"device-uuid", "",
		ar,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DYLD_INSERT_LIBRARIES should be "loader:thunk" format.
	insertLibs := ar.launchEnv["SIMCTL_CHILD_DYLD_INSERT_LIBRARIES"]
	if insertLibs != "/loader.dylib:/thunk.dylib" {
		t.Errorf("DYLD_INSERT_LIBRARIES = %q, want %q", insertLibs, "/loader.dylib:/thunk.dylib")
	}
}

// --- rewriteInfoPlist tests ---

func TestRewriteInfoPlist_OverwritesBundleFields(t *testing.T) {
	t.Parallel()

	// plist.Marshal is used in production; we test with a minimal XML plist.
	tmpDir := t.TempDir()
	plistPath := filepath.Join(tmpDir, "Info.plist")

	// Write a minimal valid plist.
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.example.original</string>
	<key>CFBundleDisplayName</key>
	<string>OriginalApp</string>
	<key>CFBundleVersion</key>
	<string>1.0</string>
</dict>
</plist>`
	if err := os.WriteFile(plistPath, []byte(plistContent), 0o600); err != nil {
		t.Fatal(err)
	}

	rewriteInfoPlist(plistPath, "axe.com.example.TestModule", "axe TestModule")

	// Read back and verify.
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("failed to read plist: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "axe.com.example.TestModule") {
		t.Error("plist should contain rewritten bundle ID")
	}
	if !strings.Contains(content, "axe TestModule") {
		t.Error("plist should contain rewritten display name")
	}
	// Original bundle version should be preserved.
	if !strings.Contains(content, "1.0") {
		t.Error("plist should preserve other keys like CFBundleVersion")
	}
}

func TestRewriteInfoPlist_MissingFile(t *testing.T) {
	t.Parallel()

	// rewriteInfoPlist logs warnings but does not panic on missing file.
	plistPath := filepath.Join(t.TempDir(), "nonexistent.plist")
	rewriteInfoPlist(plistPath, "axe.com.example.TestModule", "axe TestModule")
	// No assertion needed -- we just verify it doesn't panic.
}

func TestRewriteInfoPlist_InvalidPlist(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	plistPath := filepath.Join(tmpDir, "Info.plist")
	if err := os.WriteFile(plistPath, []byte("not a valid plist"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Should not panic on invalid plist.
	rewriteInfoPlist(plistPath, "axe.com.example.TestModule", "axe TestModule")
}
