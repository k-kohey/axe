package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// --- Fake Runner ---

type fakeRunner struct {
	fetchOutput []byte
	fetchErr    error
	buildOutput []byte
	buildErr    error

	// Captured args for assertions.
	fetchArgs []string
	buildArgs []string
}

func (f *fakeRunner) FetchBuildSettings(_ context.Context, args []string) ([]byte, error) {
	f.fetchArgs = args
	return f.fetchOutput, f.fetchErr
}

func (f *fakeRunner) Build(_ context.Context, args []string) ([]byte, error) {
	f.buildArgs = args
	return f.buildOutput, f.buildErr
}

// --- FetchSettings tests ---

func TestFetchSettings_ParsesAllFields(t *testing.T) {
	t.Parallel()

	output := `Build settings for action build and target TestModule:
    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
    SWIFT_VERSION = 5.0
    OTHER_SETTING = ignored
`
	r := &fakeRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	bs, err := FetchSettings(context.Background(), pc, dirs, r)
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

func TestFetchSettings_BuiltProductsDir(t *testing.T) {
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
			r := &fakeRunner{fetchOutput: []byte(output)}
			pc := ProjectConfig{
				Project:       "/tmp/TestProject.xcodeproj",
				Scheme:        "TestScheme",
				Configuration: tt.configuration,
			}
			dirs := ProjectDirs{Build: "/tmp/build"}

			bs, err := FetchSettings(context.Background(), pc, dirs, r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.HasSuffix(bs.BuiltProductsDir, tt.wantSuffix) {
				t.Errorf("BuiltProductsDir = %q, want suffix %q", bs.BuiltProductsDir, tt.wantSuffix)
			}
		})
	}
}

func TestFetchSettings_MissingFields(t *testing.T) {
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

			r := &fakeRunner{fetchOutput: []byte(tt.output)}
			pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
			dirs := ProjectDirs{Build: t.TempDir()}

			_, err := FetchSettings(context.Background(), pc, dirs, r)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestFetchSettings_SwiftVersionOptional(t *testing.T) {
	t.Parallel()

	// SwiftVersion is not required; FetchSettings should succeed without it.
	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	r := &fakeRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	bs, err := FetchSettings(context.Background(), pc, dirs, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs.SwiftVersion != "" {
		t.Errorf("SwiftVersion = %q, want empty", bs.SwiftVersion)
	}
}

func TestFetchSettings_RunnerError(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{
		fetchOutput: []byte("error output"),
		fetchErr:    errors.New("exit status 1"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	_, err := FetchSettings(context.Background(), pc, dirs, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild -showBuildSettings failed") {
		t.Errorf("error = %q, want to contain xcodebuild failure message", err.Error())
	}
}

func TestFetchSettings_PassesCorrectArgs(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	r := &fakeRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{
		Workspace: "/tmp/TestWorkspace.xcworkspace",
		Scheme:    "TestScheme",
	}
	dirs := ProjectDirs{Build: t.TempDir()}

	_, err := FetchSettings(context.Background(), pc, dirs, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify args contain workspace flag.
	args := strings.Join(r.fetchArgs, " ")
	if !strings.Contains(args, "-workspace") {
		t.Errorf("fetchArgs = %v, want to contain -workspace", r.fetchArgs)
	}
	if !strings.Contains(args, "-showBuildSettings") {
		t.Errorf("fetchArgs = %v, want to contain -showBuildSettings", r.fetchArgs)
	}
}

// --- Prepare tests ---

func TestPrepare_Success(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	r := &fakeRunner{
		fetchOutput: []byte(output),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	result, err := Prepare(context.Background(), pc, dirs, false, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", result.Settings.ModuleName, "TestModule")
	}
	if !result.Built {
		t.Error("Built = false, want true")
	}
	if len(r.buildArgs) == 0 {
		t.Error("Build was not called")
	}
}

func TestPrepare_ReuseBuild(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirs := ProjectDirs{Build: root}

	// Create .app so HasPreviousBuild returns true.
	appDir := filepath.Join(root, "Build", "Products", "Debug-iphonesimulator", "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	r := &fakeRunner{fetchOutput: []byte(output)}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}

	result, err := Prepare(context.Background(), pc, dirs, true, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Built {
		t.Error("Built = true, want false (should reuse)")
	}
	if len(r.buildArgs) != 0 {
		t.Error("Build should not have been called when reusing")
	}
}

func TestPrepare_FetchError(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{
		fetchOutput: []byte("error"),
		fetchErr:    errors.New("exit status 1"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	_, err := Prepare(context.Background(), pc, dirs, false, r)
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
	r := &fakeRunner{
		fetchOutput: []byte(output),
		buildOutput: []byte("BUILD FAILED"),
		buildErr:    errors.New("exit status 65"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	_, err := Prepare(context.Background(), pc, dirs, false, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild build failed") {
		t.Errorf("error = %q, want to contain build failure message", err.Error())
	}
}

// --- Run tests ---

func TestRun_Success(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{buildOutput: []byte("BUILD SUCCEEDED")}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	err := Run(context.Background(), pc, dirs, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify args contain derivedDataPath.
	args := strings.Join(r.buildArgs, " ")
	if !strings.Contains(args, "-derivedDataPath") {
		t.Errorf("buildArgs should contain -derivedDataPath, got %v", r.buildArgs)
	}
	if !strings.Contains(args, "OTHER_SWIFT_FLAGS") {
		t.Errorf("buildArgs should contain OTHER_SWIFT_FLAGS, got %v", r.buildArgs)
	}
}

func TestRun_Failure(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{
		buildOutput: []byte("BUILD FAILED"),
		buildErr:    errors.New("exit status 65"),
	}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: t.TempDir()}

	err := Run(context.Background(), pc, dirs, r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "xcodebuild build failed") {
		t.Errorf("error = %q, want to contain xcodebuild build failure message", err.Error())
	}
}

// --- ExtractCompilerPaths tests ---

func TestExtractCompilerPaths_IncludePaths(t *testing.T) {
	bs, dirs := setupRespFile(t, `-I/path/to/headers
-I/path/to/more/headers
-I
/path/split/across/lines
`)
	ExtractCompilerPaths(context.Background(), bs, dirs)

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

	ExtractCompilerPaths(context.Background(), bs, dirs)

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

	ExtractCompilerPaths(context.Background(), bs, dirs)

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
	ExtractCompilerPaths(context.Background(), bs, dirs)

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
	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 2 {
		t.Fatalf("ExtraIncludePaths count = %d, want 2", len(bs.ExtraIncludePaths))
	}
}

func TestExtractCompilerPaths_NoRespFile(t *testing.T) {
	bs := &Settings{ModuleName: "NoSuchModule", BuiltProductsDir: "/tmp/none"}
	dirs := ProjectDirs{Build: t.TempDir()}

	// Should not panic or error, just silently return.
	ExtractCompilerPaths(context.Background(), bs, dirs)

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
	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraIncludePaths) != 1 {
		t.Fatalf("ExtraIncludePaths count = %d, want 1", len(bs.ExtraIncludePaths))
	}
}

func TestExtractCompilerPaths_DependencyManifestFallback(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	// Place framework inside buildDir so it passes the ownership check.
	frameworkRoot := filepath.Join(dirs.Build, "SourcePackages", "artifacts", "FirebaseCore.framework")
	headersDir := filepath.Join(frameworkRoot, "Headers")
	modulesDir := filepath.Join(frameworkRoot, "Modules")
	if err := os.MkdirAll(headersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(headersDir, "FirebaseCore.h"), []byte("// header"), 0o644); err != nil {
		t.Fatal(err)
	}
	moduleMapPath := filepath.Join(modulesDir, "module.modulemap")
	if err := os.WriteFile(moduleMapPath, []byte(`framework module FirebaseCore {
  umbrella header "`+filepath.Join(headersDir, "FirebaseCore.h")+`"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	generatedModuleMapsDir := filepath.Join(dirs.Build, "Build", "Intermediates.noindex", "GeneratedModuleMaps-iphonesimulator")
	if err := os.MkdirAll(generatedModuleMapsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeDependencyManifest(t, dirs, bs.ModuleName, []dependencyManifestEntry{
		{ClangModuleMapPath: moduleMapPath},
	})

	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraModuleMapFiles) != 1 || bs.ExtraModuleMapFiles[0] != moduleMapPath {
		t.Fatalf("ExtraModuleMapFiles = %v, want [%q]", bs.ExtraModuleMapFiles, moduleMapPath)
	}
	if !slices.Contains(bs.ExtraFrameworkPaths, filepath.Dir(frameworkRoot)) {
		t.Fatalf("ExtraFrameworkPaths = %v, want to contain %q", bs.ExtraFrameworkPaths, filepath.Dir(frameworkRoot))
	}
	// Framework Headers are resolved via -F, not -I, so headersDir should NOT be in ExtraIncludePaths.
	if !slices.Contains(bs.ExtraIncludePaths, generatedModuleMapsDir) {
		t.Fatalf("ExtraIncludePaths = %v, want to contain generated module maps dir %q", bs.ExtraIncludePaths, generatedModuleMapsDir)
	}
}

func TestExtractCompilerPaths_DependencyManifestRelativeUmbrellaPath(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	// Place module inside buildDir so it passes the ownership check.
	moduleMapDir := filepath.Join(dirs.Build, "SourcePackages", "checkouts", "RelativeModule")
	if err := os.MkdirAll(moduleMapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	moduleMapPath := filepath.Join(moduleMapDir, "module.modulemap")
	headerPath := filepath.Join(moduleMapDir, "shim.h")
	if err := os.WriteFile(headerPath, []byte("// shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moduleMapPath, []byte(`module RelativeModule {
  umbrella header "shim.h"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeDependencyManifest(t, dirs, bs.ModuleName, []dependencyManifestEntry{
		{ClangModuleMapPath: moduleMapPath},
	})

	ExtractCompilerPaths(context.Background(), bs, dirs)

	if !slices.Contains(bs.ExtraModuleMapFiles, moduleMapPath) {
		t.Fatalf("ExtraModuleMapFiles = %v, want to contain %q", bs.ExtraModuleMapFiles, moduleMapPath)
	}
	if !slices.Contains(bs.ExtraIncludePaths, moduleMapDir) {
		t.Fatalf("ExtraIncludePaths = %v, want to contain module map dir %q", bs.ExtraIncludePaths, moduleMapDir)
	}
}

func TestExtractCompilerPaths_DependencyManifestSkipsSDKPaths(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	// Create a module map inside buildDir (should be included).
	spmModuleDir := filepath.Join(dirs.Build, "SourcePackages", "checkouts", "some-pkg", "Sources", "CModule")
	if err := os.MkdirAll(spmModuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spmModuleMapPath := filepath.Join(spmModuleDir, "module.modulemap")
	spmHeaderPath := filepath.Join(spmModuleDir, "shim.h")
	if err := os.WriteFile(spmHeaderPath, []byte("// shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spmModuleMapPath, []byte(`module CModule {
  umbrella header "shim.h"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a module map outside buildDir simulating an SDK framework (should be excluded).
	sdkRoot := filepath.Join(t.TempDir(), "Xcode.app", "Contents", "Developer", "Platforms",
		"iPhoneSimulator.platform", "Developer", "SDKs", "iPhoneSimulator.sdk",
		"System", "Library", "Frameworks", "UIKit.framework")
	sdkModulesDir := filepath.Join(sdkRoot, "Modules")
	sdkHeadersDir := filepath.Join(sdkRoot, "Headers")
	if err := os.MkdirAll(sdkModulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sdkHeadersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkModuleMapPath := filepath.Join(sdkModulesDir, "module.modulemap")
	if err := os.WriteFile(sdkModuleMapPath, []byte(`framework module UIKit {
  umbrella header "UIKit.h"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkHeadersDir, "UIKit.h"), []byte("// UIKit"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeDependencyManifest(t, dirs, bs.ModuleName, []dependencyManifestEntry{
		{ClangModuleMapPath: spmModuleMapPath},
		{ClangModuleMapPath: sdkModuleMapPath},
	})

	ExtractCompilerPaths(context.Background(), bs, dirs)

	// SPM module map (inside buildDir) should be included.
	if !slices.Contains(bs.ExtraModuleMapFiles, spmModuleMapPath) {
		t.Errorf("ExtraModuleMapFiles should contain SPM module map %q, got %v", spmModuleMapPath, bs.ExtraModuleMapFiles)
	}
	if !slices.Contains(bs.ExtraIncludePaths, spmModuleDir) {
		t.Errorf("ExtraIncludePaths should contain SPM include dir %q, got %v", spmModuleDir, bs.ExtraIncludePaths)
	}

	// SDK module map (outside buildDir) should be excluded.
	if slices.Contains(bs.ExtraModuleMapFiles, sdkModuleMapPath) {
		t.Errorf("ExtraModuleMapFiles should NOT contain SDK module map %q", sdkModuleMapPath)
	}
	if slices.Contains(bs.ExtraFrameworkPaths, filepath.Dir(sdkRoot)) {
		t.Errorf("ExtraFrameworkPaths should NOT contain SDK framework path %q", filepath.Dir(sdkRoot))
	}
	for _, p := range bs.ExtraIncludePaths {
		if strings.HasPrefix(p, sdkRoot) {
			t.Errorf("ExtraIncludePaths should NOT contain SDK path %q", p)
		}
	}
}

func TestExtractCompilerPaths_DependencyManifestInvalidJSON(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	depDir := filepath.Join(dirs.Build, "Build", "Intermediates.noindex",
		"TestProject.build", "Debug-iphonesimulator",
		bs.ModuleName+".build", "Objects-normal", "arm64")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "TestModule-dependencies-1.json"), []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Should not panic; paths remain empty.
	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraModuleMapFiles) != 0 {
		t.Errorf("ExtraModuleMapFiles should be empty, got %v", bs.ExtraModuleMapFiles)
	}
	if len(bs.ExtraIncludePaths) != 0 {
		t.Errorf("ExtraIncludePaths should be empty, got %v", bs.ExtraIncludePaths)
	}
}

func TestExtractCompilerPaths_DependencyManifestEmptyArray(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	writeDependencyManifest(t, dirs, bs.ModuleName, []dependencyManifestEntry{})

	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraModuleMapFiles) != 0 {
		t.Errorf("ExtraModuleMapFiles should be empty, got %v", bs.ExtraModuleMapFiles)
	}
}

func TestExtractCompilerPaths_DependencyManifestNonExistentModuleMap(t *testing.T) {
	bs, dirs := setupDependencyManifest(t)

	writeDependencyManifest(t, dirs, bs.ModuleName, []dependencyManifestEntry{
		{ClangModuleMapPath: "/nonexistent/path/module.modulemap"},
	})

	// Should not panic; non-existent paths are silently skipped.
	ExtractCompilerPaths(context.Background(), bs, dirs)

	if len(bs.ExtraModuleMapFiles) != 0 {
		t.Errorf("ExtraModuleMapFiles should be empty for non-existent paths, got %v", bs.ExtraModuleMapFiles)
	}
}

// --- HasPreviousBuild tests ---

func TestHasPreviousBuild_True(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "Build", "Products", "Debug-iphonesimulator", "MyApp.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &Settings{ModuleName: "MyApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := ProjectDirs{Build: root}

	if !HasPreviousBuild(bs, dirs) {
		t.Error("HasPreviousBuild = false, want true")
	}
}

func TestHasPreviousBuild_False(t *testing.T) {
	root := t.TempDir()
	bs := &Settings{ModuleName: "MyApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := ProjectDirs{Build: root}

	if HasPreviousBuild(bs, dirs) {
		t.Error("HasPreviousBuild = true, want false")
	}
}

// --- Helpers ---

// setupRespFile creates a temporary directory structure mimicking the xcodebuild
// intermediates layout and writes content as a swiftc response file.
func setupRespFile(t *testing.T, content string) (*Settings, ProjectDirs) {
	t.Helper()
	root := t.TempDir()
	dirs := ProjectDirs{Build: root}
	bs := &Settings{ModuleName: "TestModule", BuiltProductsDir: "/products/dir"}

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

func setupDependencyManifest(t *testing.T) (*Settings, ProjectDirs) {
	t.Helper()
	root := t.TempDir()
	dirs := ProjectDirs{Build: root}
	bs := &Settings{ModuleName: "TestModule", BuiltProductsDir: "/products/dir"}
	return bs, dirs
}

func writeDependencyManifest(t *testing.T, dirs ProjectDirs, moduleName string, entries []dependencyManifestEntry) string {
	t.Helper()
	depDir := filepath.Join(dirs.Build, "Build", "Intermediates.noindex",
		"TestProject.build", "Debug-iphonesimulator",
		moduleName+".build", "Objects-normal", "arm64")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(depDir, "TestModule-dependencies-1.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}
