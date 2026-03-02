package preview

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/k-kohey/axe/internal/preview/build"
)

// sessionFakeCompanion implements companionProcess for session testing.
type sessionFakeCompanion struct {
	doneCh  chan struct{}
	stopped atomic.Bool
}

func newSessionFakeCompanion() *sessionFakeCompanion {
	return &sessionFakeCompanion{doneCh: make(chan struct{})}
}

func (f *sessionFakeCompanion) Done() <-chan struct{} { return f.doneCh }
func (f *sessionFakeCompanion) Err() error            { return nil }
func (f *sessionFakeCompanion) Stop() error {
	f.stopped.Store(true)
	return nil
}

// sessionToolchainRunner creates output files at the -o path for CompileSwift/CompileC.
type sessionToolchainRunner struct {
	sdkPathResult string
}

func (f *sessionToolchainRunner) SDKPath(_ context.Context, _ string) (string, error) {
	return f.sdkPathResult, nil
}

func (f *sessionToolchainRunner) CompileSwift(_ context.Context, args []string) ([]byte, error) {
	return nil, touchOutputFile(args)
}

func (f *sessionToolchainRunner) CompileC(_ context.Context, args []string) ([]byte, error) {
	return nil, touchOutputFile(args)
}

func (f *sessionToolchainRunner) Codesign(_ context.Context, _ string) error {
	return nil
}

// touchOutputFile finds -o in args and creates an empty file at that path.
func touchOutputFile(args []string) error {
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			if err := os.MkdirAll(filepath.Dir(args[i+1]), 0o755); err != nil {
				return err
			}
			return os.WriteFile(args[i+1], []byte("fake"), 0o644)
		}
	}
	return nil
}

// setupSessionTest creates a temporary directory structure mimicking a build
// output and returns a SessionConfig ready for testing.
func setupSessionTest(t *testing.T) SessionConfig {
	t.Helper()

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a minimal Info.plist so installApp can rewrite it.
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a minimal project file so ProjectConfig is valid.
	projPath := filepath.Join(tmpDir, "TestModule.xcodeproj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}

	pc, err := build.NewProjectConfig(projPath, "", "TestModule", "Debug")
	if err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}

	// Create a Preparer with cached result so it returns immediately.
	dirs := build.ProjectDirs{Root: tmpDir, Build: buildDir}
	br := &fakeBuildRunner{}
	preparer := build.NewPreparer(pc, dirs, true, br)

	// Pre-populate the cache by calling Prepare once with the right setup.
	// We need a way to seed the preparer's cache. Since Preparer calls
	// build.Prepare internally (which needs xcodebuild), we use an alternative
	// approach: create a SessionConfig that doesn't use Preparer at all,
	// and instead injects a BuildRunner that returns the right settings.
	// But actually, for session tests we can set the Preparer field and
	// not call Preparer.Prepare() directly — the issue is Preparer calls real
	// build.Prepare(). Let's use Preparer=nil and inject a BuildRunner that works.

	_ = preparer // not usable without real xcodebuild

	return SessionConfig{
		PC:            pc,
		DeviceUDID:    "FAKE-SESSION-DEVICE",
		DeviceSetPath: filepath.Join(tmpDir, "device-set"),
		BuildRunner:   br,
		Toolchain:     &sessionToolchainRunner{sdkPathResult: "/fake/sdk"},
		AppRunner:     &fakeAppRunner{},
		Copier: &sessionFileCopier{
			bs:  bs,
			src: appDir,
		},
		BootFunc: func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
			return newSessionFakeCompanion(), nil
		},
	}
}

// sessionFileCopier copies the fake .app and creates the right structure.
type sessionFileCopier struct {
	bs  *build.Settings
	src string
}

func (f *sessionFileCopier) CopyDir(_ context.Context, _, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	// Copy Info.plist from src to dst for installApp's plist rewrite.
	data, err := os.ReadFile(filepath.Join(f.src, "Info.plist"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "Info.plist"), data, 0o644)
}

// sessionPreparer creates a *build.Preparer with a pre-seeded cache via
// a fakeBuildRunner that returns realistic settings output.
func sessionPreparer(t *testing.T, pc build.ProjectConfig, buildDir string, bs *build.Settings) *build.Preparer {
	t.Helper()

	// build.Prepare calls FetchBuildSettings then Build. We need FetchBuildSettings
	// to return parseable output. Build can no-op if HasPreviousBuild returns true.
	fetchOutput := fmt.Sprintf(
		"BUILD_DIR = %s\n"+
			"PRODUCT_MODULE_NAME = %s\n"+
			"PRODUCT_BUNDLE_IDENTIFIER = %s\n"+
			"BUILT_PRODUCTS_DIR = %s\n"+
			"IPHONEOS_DEPLOYMENT_TARGET = %s\n"+
			"SWIFT_VERSION = %s\n",
		filepath.Join(buildDir, "Build", "Products"),
		bs.ModuleName,
		bs.BundleID,
		bs.BuiltProductsDir,
		bs.DeploymentTarget,
		bs.SwiftVersion,
	)

	br := &fakeBuildRunner{
		fetchOutput: []byte(fetchOutput),
	}

	dirs := build.ProjectDirs{Root: filepath.Dir(buildDir), Build: buildDir}
	return build.NewPreparer(pc, dirs, true, br)
}

func TestNewPreviewSession_Success(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}

	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}
	defer sess.Close()

	if sess.bs == nil {
		t.Fatal("session.bs is nil")
	}
	if sess.loaderPath == "" {
		t.Error("session.loaderPath is empty")
	}
	if sess.bootCompanion == nil {
		t.Error("session.bootCompanion is nil for axe-managed device")
	}
}

func TestNewPreviewSession_BuildFailure(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)
	companion := newSessionFakeCompanion()
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		return companion, nil
	}
	// Use a preparer with a build runner that fails.
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	br := &fakeBuildRunner{
		fetchErr: fmt.Errorf("xcodebuild failed"),
	}
	dirs := build.ProjectDirs{Root: tmpDir, Build: buildDir}
	cfg.Preparer = build.NewPreparer(cfg.PC, dirs, false, br)

	_, err := NewPreviewSession(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected error from build failure")
	}

	// Boot companion should be stopped on build failure.
	if !companion.stopped.Load() {
		t.Error("boot companion was not stopped after build failure")
	}
}

func TestNewPreviewSession_BootFailure(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		return nil, fmt.Errorf("boot failed")
	}

	// Need a valid preparer that won't fail.
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	if err := os.MkdirAll(builtProducts, 0o755); err != nil {
		t.Fatal(err)
	}
	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	_, err := NewPreviewSession(t.Context(), cfg)
	if err == nil {
		t.Fatal("expected error from boot failure")
	}
}

func TestCapturePreview_SingleCapture(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}
	defer sess.Close()

	// Create a fake Swift source file with a #Preview block.
	sourceFile := filepath.Join(tmpDir, "HogeView.swift")
	swiftSource := `import SwiftUI
struct HogeView: View {
    var body: some View { Text("Hello") }
}
#Preview { HogeView() }
`
	if err := os.WriteFile(sourceFile, []byte(swiftSource), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a fake Unix socket listener so WaitForReady succeeds.
	startFakeSocket(t, sess.dirs.Socket)

	var onReadyCalled bool
	err = sess.CapturePreview(t.Context(), CaptureRequest{
		SourceFile:      sourceFile,
		PreviewSelector: "0",
		OnReady: func(_ context.Context, device, setPath string) error {
			onReadyCalled = true
			if device != cfg.DeviceUDID {
				t.Errorf("OnReady device = %q, want %q", device, cfg.DeviceUDID)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CapturePreview() error: %v", err)
	}
	if !onReadyCalled {
		t.Error("OnReady was not called")
	}
}

func TestCapturePreview_MultipleCaptures_UsesHotReload(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	// Track boot calls to verify boot only happens once.
	var bootCount atomic.Int32
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		bootCount.Add(1)
		return newSessionFakeCompanion(), nil
	}

	// Track launch calls to verify cold start only happens on 1st capture.
	var launchCount atomic.Int32
	cfg.AppRunner = &fakeAppRunner{
		onLaunch: func() { launchCount.Add(1) },
	}

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}
	defer sess.Close()

	sourceFile := filepath.Join(tmpDir, "HogeView.swift")
	swiftSource := `import SwiftUI
struct HogeView: View {
    var body: some View { Text("Hello") }
}
#Preview { HogeView() }
`
	if err := os.WriteFile(sourceFile, []byte(swiftSource), 0o644); err != nil {
		t.Fatal(err)
	}

	// Track hot-reload commands received by the fake socket.
	var reloadCount atomic.Int32
	startFakeSocket(t, sess.dirs.Socket, func(_ string) {
		reloadCount.Add(1)
	})

	// Call CapturePreview 3 times.
	// 1st: cold start (launch), 2nd+3rd: hot-reload (SendReloadCommand).
	const n = 3
	for i := range n {
		err := sess.CapturePreview(t.Context(), CaptureRequest{
			SourceFile:      sourceFile,
			PreviewSelector: "0",
			OnReady: func(_ context.Context, _, _ string) error {
				return nil
			},
		})
		if err != nil {
			t.Fatalf("CapturePreview(%d) error: %v", i, err)
		}
	}

	// Boot should have been called exactly once (during NewPreviewSession).
	if got := bootCount.Load(); got != 1 {
		t.Errorf("boot was called %d times, want 1", got)
	}

	// Launch should have been called once (cold start on 1st capture).
	if got := launchCount.Load(); got != 1 {
		t.Errorf("launch was called %d times, want 1", got)
	}

	// Hot-reload should have been called twice (2nd and 3rd captures).
	if got := reloadCount.Load(); got != 2 {
		t.Errorf("reload was called %d times, want 2", got)
	}

	// reloadCounter should have advanced to n.
	if sess.reloadCounter != n {
		t.Errorf("reloadCounter = %d, want %d", sess.reloadCounter, n)
	}
}

func TestCapturePreview_HotReloadFailure_FallsBackToColdStart(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	var launchCount atomic.Int32
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		return newSessionFakeCompanion(), nil
	}

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}
	defer sess.Close()

	sourceFile := filepath.Join(tmpDir, "HogeView.swift")
	swiftSource := `import SwiftUI
struct HogeView: View {
    var body: some View { Text("Hello") }
}
#Preview { HogeView() }
`
	if err := os.WriteFile(sourceFile, []byte(swiftSource), 0o644); err != nil {
		t.Fatal(err)
	}

	// Socket that responds with ERR: on reload to simulate hot-reload failure.
	// connCount tracks connections: 1st = WaitForReady (cold start),
	// 2nd = SendReloadCommand (returns ERR), 3rd = WaitForReady (fallback cold start).
	var connCount atomic.Int32
	startFakeSocketCustom(t, sess.dirs.Socket, func(conn net.Conn) {
		n := connCount.Add(1)
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			// SendReloadCommand: client sent a dylib path.
			if n == 2 {
				// 2nd connection: respond with ERR to trigger fallback.
				_, _ = fmt.Fprintf(conn, "ERR:dlopen failed (test)\n")
			} else {
				_, _ = fmt.Fprintf(conn, "OK\n")
			}
		}
		// WaitForReady: client connected without sending data; just close.
	})

	// Use AppRunner that tracks launches via onLaunch.
	cfg.AppRunner = &fakeAppRunner{
		onLaunch: func() { launchCount.Add(1) },
	}
	sess.cfg.AppRunner = cfg.AppRunner

	// 1st capture: cold start (WaitForReady).
	err = sess.CapturePreview(t.Context(), CaptureRequest{
		SourceFile:      sourceFile,
		PreviewSelector: "0",
	})
	if err != nil {
		t.Fatalf("CapturePreview(0) error: %v", err)
	}
	if got := launchCount.Load(); got != 1 {
		t.Fatalf("launch count after 1st capture = %d, want 1", got)
	}

	// 2nd capture: hot-reload fails (ERR:) → falls back to cold start.
	err = sess.CapturePreview(t.Context(), CaptureRequest{
		SourceFile:      sourceFile,
		PreviewSelector: "0",
	})
	if err != nil {
		t.Fatalf("CapturePreview(1) error: %v", err)
	}

	// Launch should have been called twice (1st cold start + fallback cold start).
	if got := launchCount.Load(); got != 2 {
		t.Errorf("launch count = %d, want 2", got)
	}
}

func TestCapturePreview_ContextCanceled_NoFallback(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	var launchCount atomic.Int32
	cfg.AppRunner = &fakeAppRunner{
		onLaunch: func() { launchCount.Add(1) },
	}
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		return newSessionFakeCompanion(), nil
	}

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}
	defer sess.Close()

	sourceFile := filepath.Join(tmpDir, "HogeView.swift")
	swiftSource := `import SwiftUI
struct HogeView: View {
    var body: some View { Text("Hello") }
}
#Preview { HogeView() }
`
	if err := os.WriteFile(sourceFile, []byte(swiftSource), 0o644); err != nil {
		t.Fatal(err)
	}

	startFakeSocket(t, sess.dirs.Socket)

	// 1st capture: cold start succeeds.
	err = sess.CapturePreview(t.Context(), CaptureRequest{
		SourceFile:      sourceFile,
		PreviewSelector: "0",
	})
	if err != nil {
		t.Fatalf("CapturePreview(0) error: %v", err)
	}

	// Remove socket so SendReloadCommand's dialWithRetry fails on first dial,
	// then checks ctx.Done() between retries.
	_ = os.Remove(sess.dirs.Socket)

	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	// 2nd capture with canceled context + no socket: should return error,
	// NOT fall back to cold start.
	err = sess.CapturePreview(cancelCtx, CaptureRequest{
		SourceFile:      sourceFile,
		PreviewSelector: "0",
	})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}

	// Launch should have been called only once (initial cold start), not twice.
	if got := launchCount.Load(); got != 1 {
		t.Errorf("launch count = %d, want 1 (fallback should NOT have run)", got)
	}
}

func TestClose_StopsCompanion(t *testing.T) {
	t.Parallel()

	cfg := setupSessionTest(t)
	companion := newSessionFakeCompanion()
	cfg.BootFunc = func(_ context.Context, _, _ string, _ bool) (companionProcess, error) {
		return companion, nil
	}

	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "axe.com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	cfg.Copier = &sessionFileCopier{bs: bs, src: appDir}
	cfg.Preparer = sessionPreparer(t, cfg.PC, buildDir, bs)

	sess, err := NewPreviewSession(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewPreviewSession() error: %v", err)
	}

	// Create the socket file so Close can remove it.
	if err := os.MkdirAll(filepath.Dir(sess.dirs.Socket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sess.dirs.Socket, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	sess.Close()

	if !companion.stopped.Load() {
		t.Error("boot companion was not stopped by Close()")
	}

	// Socket should be removed.
	if _, err := os.Stat(sess.dirs.Socket); !os.IsNotExist(err) {
		t.Error("socket was not removed by Close()")
	}
}

// startFakeSocketCustom creates a Unix domain socket listener with a custom
// per-connection handler. The handler receives each accepted connection and
// is responsible for reading/writing and closing it.
func startFakeSocketCustom(t *testing.T, socketPath string, handler func(net.Conn)) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			handler(conn)
			_ = conn.Close()
		}
	}()
}

// startFakeSocket creates a Unix domain socket listener that simulates the
// loader socket. It handles both WaitForReady (empty request → close) and
// SendReloadCommand (reads dylib path → responds "OK\n").
// The optional onReload callback is invoked with each received dylib path.
func startFakeSocket(t *testing.T, socketPath string, onReload ...func(string)) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Remove any existing socket file.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			// Try to read a dylib path. If the client sends data, respond
			// with "OK\n" (hot-reload protocol). If the client disconnects
			// without sending, this is a WaitForReady probe.
			scanner := bufio.NewScanner(conn)
			if scanner.Scan() {
				path := scanner.Text()
				if len(onReload) > 0 && onReload[0] != nil {
					onReload[0](path)
				}
				_, _ = fmt.Fprintf(conn, "OK\n")
			}
			_ = conn.Close()
		}
	}()
}
