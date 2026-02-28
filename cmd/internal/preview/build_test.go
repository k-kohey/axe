package preview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k-kohey/axe/internal/preview/build"
)

// --- Fake BuildRunner ---

type fakeBuildRunner struct {
	fetchOutput []byte
	fetchErr    error
	buildOutput []byte
	buildErr    error

	// Captured args for assertions.
	fetchArgs []string
	buildArgs []string
}

func (f *fakeBuildRunner) FetchBuildSettings(_ context.Context, args []string) ([]byte, error) {
	f.fetchArgs = args
	return f.fetchOutput, f.fetchErr
}

func (f *fakeBuildRunner) Build(_ context.Context, args []string) ([]byte, error) {
	f.buildArgs = args
	return f.buildOutput, f.buildErr
}

// --- build.FetchSettings tests ---

func TestFetchBuildSettings_ParsesAllFields(t *testing.T) {
	t.Parallel()

	output := `Build settings for action build and target TestModule:
    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
    SWIFT_VERSION = 5.0
    OTHER_SETTING = ignored
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	bs, err := build.FetchSettings(context.Background(), pc, dirs, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bs.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", bs.ModuleName, "TestModule")
	}
	if bs.OriginalBundleID != "com.example.TestModule" {
		t.Errorf("OriginalBundleID = %q, want %q", bs.OriginalBundleID, "com.example.TestModule")
	}
	if bs.BundleID != "axe.com.example.TestModule" {
		t.Errorf("BundleID = %q, want %q", bs.BundleID, "axe.com.example.TestModule")
	}
	if bs.DeploymentTarget != "17.0" {
		t.Errorf("DeploymentTarget = %q, want %q", bs.DeploymentTarget, "17.0")
	}
	if bs.SwiftVersion != "5.0" {
		t.Errorf("SwiftVersion = %q, want %q", bs.SwiftVersion, "5.0")
	}
}

func TestFetchBuildSettings_BuiltProductsDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configuration string
		wantSuffix    string
	}{
		{
			name:          "default configuration",
			configuration: "",
			wantSuffix:    "Build/Products/Debug-iphonesimulator",
		},
		{
			name:          "explicit configuration",
			configuration: "Release",
			wantSuffix:    "Build/Products/Release-iphonesimulator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
    SWIFT_VERSION = 5.0
`
			br := &fakeBuildRunner{fetchOutput: []byte(output)}
			pc := ProjectConfig{
				Project:       "/tmp/TestProject.xcodeproj",
				Scheme:        "TestScheme",
				Configuration: tt.configuration,
			}
			dirs := build.ProjectDirs{Build: "/tmp/build"}

			bs, err := build.FetchSettings(context.Background(), pc, dirs, br)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.HasSuffix(bs.BuiltProductsDir, tt.wantSuffix) {
				t.Errorf("BuiltProductsDir = %q, want suffix %q", bs.BuiltProductsDir, tt.wantSuffix)
			}
		})
	}
}

func TestFetchBuildSettings_MissingFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		output    string
		wantError string
	}{
		{
			name: "missing module name",
			output: `    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`,
			wantError: "PRODUCT_MODULE_NAME not found",
		},
		{
			name: "missing bundle ID",
			output: `    PRODUCT_MODULE_NAME = TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`,
			wantError: "PRODUCT_BUNDLE_IDENTIFIER not found",
		},
		{
			name: "missing deployment target",
			output: `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
`,
			wantError: "IPHONEOS_DEPLOYMENT_TARGET not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			br := &fakeBuildRunner{fetchOutput: []byte(tt.output)}
			pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
			dirs := build.ProjectDirs{Build: t.TempDir()}

			_, err := build.FetchSettings(context.Background(), pc, dirs, br)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestFetchBuildSettings_SwiftVersionOptional(t *testing.T) {
	t.Parallel()

	// SwiftVersion is not required; FetchSettings should succeed without it.
	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	bs, err := build.FetchSettings(context.Background(), pc, dirs, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs.SwiftVersion != "" {
		t.Errorf("SwiftVersion = %q, want empty", bs.SwiftVersion)
	}
}

func TestFetchBuildSettings_RunnerError(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{
		fetchOutput: []byte("error output"),
		fetchErr:    errors.New("exit status 1"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	_, err := build.FetchSettings(context.Background(), pc, dirs, br)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild -showBuildSettings failed") {
		t.Errorf("error = %q, want to contain xcodebuild failure message", err.Error())
	}
}

func TestFetchBuildSettings_PassesCorrectArgs(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{
		Workspace: "/tmp/TestWorkspace.xcworkspace",
		Scheme:    "TestScheme",
	}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	_, err := build.FetchSettings(context.Background(), pc, dirs, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify args contain workspace flag.
	args := strings.Join(br.fetchArgs, " ")
	if !strings.Contains(args, "-workspace") {
		t.Errorf("fetchArgs = %v, want to contain -workspace", br.fetchArgs)
	}
	if !strings.Contains(args, "-showBuildSettings") {
		t.Errorf("fetchArgs = %v, want to contain -showBuildSettings", br.fetchArgs)
	}
}

// --- build.Prepare tests ---

func TestPrepare_Success(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{
		fetchOutput: []byte(output),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	result, err := build.Prepare(context.Background(), pc, dirs, false, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", result.Settings.ModuleName, "TestModule")
	}
	if !result.Built {
		t.Error("Built = false, want true")
	}
	if len(br.buildArgs) == 0 {
		t.Error("Build was not called")
	}
}

func TestPrepare_ReuseBuild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirs := build.ProjectDirs{Build: root}

	// Create .app so HasPreviousBuild returns true.
	appDir := filepath.Join(root, "Build", "Products", "Debug-iphonesimulator", "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}

	result, err := build.Prepare(context.Background(), pc, dirs, true, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Built {
		t.Error("Built = true, want false (should reuse)")
	}
	if len(br.buildArgs) != 0 {
		t.Error("Build should not have been called when reusing")
	}
}

func TestPrepare_FetchError(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{
		fetchOutput: []byte("error"),
		fetchErr:    errors.New("exit status 1"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	_, err := build.Prepare(context.Background(), pc, dirs, false, br)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild -showBuildSettings failed") {
		t.Errorf("error = %q, want to contain fetch failure message", err.Error())
	}
}

func TestPrepare_BuildError(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{
		fetchOutput: []byte(output),
		buildOutput: []byte("BUILD FAILED"),
		buildErr:    errors.New("exit status 65"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	_, err := build.Prepare(context.Background(), pc, dirs, false, br)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild build failed") {
		t.Errorf("error = %q, want to contain build failure message", err.Error())
	}
}

// --- build.Run tests ---

func TestBuildProject_Success(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{buildOutput: []byte("BUILD SUCCEEDED")}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	err := build.Run(context.Background(), pc, dirs, br)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify args contain derivedDataPath.
	args := strings.Join(br.buildArgs, " ")
	if !strings.Contains(args, "-derivedDataPath") {
		t.Errorf("buildArgs should contain -derivedDataPath, got %v", br.buildArgs)
	}
	if !strings.Contains(args, "OTHER_SWIFT_FLAGS") {
		t.Errorf("buildArgs should contain OTHER_SWIFT_FLAGS, got %v", br.buildArgs)
	}
}

func TestBuildProject_Failure(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{
		buildOutput: []byte("BUILD FAILED"),
		buildErr:    errors.New("exit status 65"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	err := build.Run(context.Background(), pc, dirs, br)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild build failed") {
		t.Errorf("error = %q, want to contain xcodebuild build failure message", err.Error())
	}
}

func TestExtractCompilerPaths_IncludePaths(t *testing.T) {
	bs, dirs := setupRespFile(t, `-I/path/to/headers
-I/path/to/more/headers
-I
/path/split/across/lines
`)
	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 3 {
		t.Fatalf("ExtraIncludePaths count = %d, want 3", len(bs.ExtraIncludePaths))
	}
	if bs.ExtraIncludePaths[0] != "/path/to/headers" {
		t.Errorf("ExtraIncludePaths[0] = %q", bs.ExtraIncludePaths[0])
	}
	if bs.ExtraIncludePaths[2] != "/path/split/across/lines" {
		t.Errorf("ExtraIncludePaths[2] = %q", bs.ExtraIncludePaths[2])
	}
}

func TestExtractCompilerPaths_SkipsHmapAndBuiltProducts(t *testing.T) {
	bs, dirs := setupRespFile(t, `-I/products/dir
-I/path/to/target.hmap
-I/other/headers
`)
	bs.BuiltProductsDir = "/products/dir"

	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 1 {
		t.Fatalf("ExtraIncludePaths count = %d, want 1", len(bs.ExtraIncludePaths))
	}
	if bs.ExtraIncludePaths[0] != "/other/headers" {
		t.Errorf("ExtraIncludePaths[0] = %q", bs.ExtraIncludePaths[0])
	}
}

func TestExtractCompilerPaths_FrameworkPaths(t *testing.T) {
	bs, dirs := setupRespFile(t, `-F
/products/dir
-F
/products/dir/PackageFrameworks
`)
	bs.BuiltProductsDir = "/products/dir"

	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraFrameworkPaths) != 1 {
		t.Fatalf("ExtraFrameworkPaths count = %d, want 1", len(bs.ExtraFrameworkPaths))
	}
	if bs.ExtraFrameworkPaths[0] != "/products/dir/PackageFrameworks" {
		t.Errorf("ExtraFrameworkPaths[0] = %q", bs.ExtraFrameworkPaths[0])
	}
}

func TestExtractCompilerPaths_ModuleMapFiles(t *testing.T) {
	bs, dirs := setupRespFile(t, `-fmodule-map-file=/path/to/FirebaseCore.modulemap
-fmodule-map-file=/path/to/nanopb.modulemap
`)
	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraModuleMapFiles) != 2 {
		t.Fatalf("ExtraModuleMapFiles count = %d, want 2", len(bs.ExtraModuleMapFiles))
	}
	if bs.ExtraModuleMapFiles[0] != "/path/to/FirebaseCore.modulemap" {
		t.Errorf("ExtraModuleMapFiles[0] = %q", bs.ExtraModuleMapFiles[0])
	}
}

func TestExtractCompilerPaths_DeduplicatesIncludePaths(t *testing.T) {
	bs, dirs := setupRespFile(t, `-I/path/to/headers
-I/path/to/headers
-I/path/to/other
`)
	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 2 {
		t.Fatalf("ExtraIncludePaths count = %d, want 2", len(bs.ExtraIncludePaths))
	}
}

func TestExtractCompilerPaths_NoRespFile(t *testing.T) {
	bs := &build.Settings{ModuleName: "NoSuchModule", BuiltProductsDir: "/tmp/none"}
	dirs := build.ProjectDirs{Build: t.TempDir()}

	// Should not panic or error, just silently return.
	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 0 {
		t.Errorf("ExtraIncludePaths should be empty, got %d", len(bs.ExtraIncludePaths))
	}
}

func TestExtractCompilerPaths_IgnoresUnrelatedFlags(t *testing.T) {
	bs, dirs := setupRespFile(t, `-DDEBUG
-sdk
/path/to/sdk
-target
arm64-apple-ios17.0-simulator
-I/real/path
-swift-version
5
`)
	build.ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 1 {
		t.Fatalf("ExtraIncludePaths count = %d, want 1", len(bs.ExtraIncludePaths))
	}
}

// setupRespFile creates a temporary directory structure mimicking the xcodebuild
// intermediates layout and writes content as a swiftc response file.
func setupRespFile(t *testing.T, content string) (*build.Settings, build.ProjectDirs) {
	t.Helper()
	root := t.TempDir()
	dirs := build.ProjectDirs{Build: root}
	bs := &build.Settings{ModuleName: "TestModule", BuiltProductsDir: "/products/dir"}

	respDir := filepath.Join(root, "Build", "Intermediates.noindex",
		"TestProject.build", "Debug-iphonesimulator",
		"TestModule.build", "Objects-normal", "arm64")
	if err := os.MkdirAll(respDir, 0o755); err != nil {
		t.Fatal(err)
	}
	respPath := filepath.Join(respDir, "arguments-abc123.resp")
	if err := os.WriteFile(respPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return bs, dirs
}
