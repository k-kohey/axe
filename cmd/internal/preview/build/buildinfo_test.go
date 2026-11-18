package build

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeBuildInfo(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "build-info.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Relative paths are resolved against the project root; absolute ones are kept.
func TestPrepareFromBuildInfo(t *testing.T) {
	root := t.TempDir()
	path := writeBuildInfo(t, `{
	  "schemaVersion": 1, "module": "FeatureUI", "configuration": "Debug", "provider": "bazel",
	  "swiftcArguments": ["-module-name", "FeatureUI", "-target", "arm64-apple-ios17.0-simulator", "-swift-version", "6",
	                      "-Xcc", "-fmodule-map-file=out/Clib.modulemap",
	                      "-Xcc", "-fmodule-map-file=/abs/Other.modulemap"],
	  "sourceFiles": ["Sources/rel.swift", "/abs/abs.swift"],
	  "moduleSearchPaths": ["out/mod", "/abs/mod", "out/app"],
	  "frameworkSearchPaths": ["out/fw", "out/app"],
	  "indexStorePath": "out/index",
	  "transitiveDependencies": [],
	  "appBundlePath": "out/app/MyApp.app", "appBundleId": "com.example.app"
	}`)
	dirs := ProjectDirs{Root: t.TempDir(), Build: filepath.Join(t.TempDir(), "build")}
	result, err := PrepareFromBuildInfo(path, root, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Built {
		t.Error("build info mode must not report a build")
	}
	s := result.Settings
	if s.ModuleName != "MyApp" || s.BundleID != "axe.com.example.app" || s.OriginalBundleID != "com.example.app" {
		t.Errorf("app identity: %+v", s)
	}
	if s.BuiltProductsDir != filepath.Join(root, "out/app") || s.DeploymentTarget != "17.0" || s.SwiftVersion != "6" {
		t.Errorf("paths/versions: %+v", s)
	}
	// BuiltProductsDir ("out/app") is de-duplicated from the search paths.
	if want := []string{filepath.Join(root, "out/mod"), "/abs/mod"}; !slices.Equal(s.ExtraIncludePaths, want) {
		t.Errorf("include paths = %v, want %v", s.ExtraIncludePaths, want)
	}
	if want := []string{filepath.Join(root, "out/fw")}; !slices.Equal(s.ExtraFrameworkPaths, want) {
		t.Errorf("framework paths = %v, want %v", s.ExtraFrameworkPaths, want)
	}
	if want := []string{filepath.Join(root, "out/Clib.modulemap"), "/abs/Other.modulemap"}; !slices.Equal(s.ExtraModuleMapFiles, want) {
		t.Errorf("module maps = %v, want %v", s.ExtraModuleMapFiles, want)
	}
	if got := s.ModuleForSource(filepath.Join(root, "Sources/rel.swift")); got != "FeatureUI" {
		t.Errorf("ModuleForSource(relative source) = %q", got)
	}
	if got := s.ModuleForSource("/abs/abs.swift"); got != "FeatureUI" {
		t.Errorf("ModuleForSource(absolute source) = %q", got)
	}
	if got := s.ModuleForSource("/elsewhere.swift"); got != "" {
		t.Errorf("ModuleForSource for unknown file = %q", got)
	}
	if target, _ := os.Readlink(dirs.IndexStorePath()); target != filepath.Join(root, "out/index") {
		t.Errorf("index store link = %q", target)
	}
}

func TestLoadBuildInfoRejectsInvalidInput(t *testing.T) {
	for name, body := range map[string]string{
		"version":  `{"schemaVersion": 2}`,
		"no app":   `{"schemaVersion": 1, "module": "M"}`,
		"not json": `{`,
	} {
		if _, err := LoadBuildInfo(writeBuildInfo(t, body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	info := &BuildInfo{SchemaVersion: 1, AppBundlePath: "/a/A.app", AppBundleID: "a"}
	if _, err := info.Settings(); err == nil {
		t.Error("missing -target must be an error")
	}
}

// minimalBuildInfo is a build info without its closing brace, so tests can append fields.
const minimalBuildInfo = `{
	  "schemaVersion": 1, "module": "FeatureUI",
	  "swiftcArguments": ["-module-name", "FeatureUI", "-target", "arm64-apple-ios17.0-simulator"],
	  "appBundlePath": "/out/app/MyApp.app", "appBundleId": "com.example.app"`

func staticBuildInfoConfig(t *testing.T) ProjectConfig {
	t.Helper()
	return ProjectConfig{BuildInfoPath: writeBuildInfo(t, minimalBuildInfo+"}")}
}

// Prepare must serve a static build info without touching xcodebuild.
func TestPrepareWithBuildInfoNeverCallsXcodebuild(t *testing.T) {
	pc := staticBuildInfoConfig(t)
	dirs := ProjectDirs{Root: t.TempDir(), Build: filepath.Join(t.TempDir(), "build")}

	r := &fakeRunner{}
	if _, err := Prepare(context.Background(), pc, dirs, false, r); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if r.fetchArgs != nil || r.buildArgs != nil {
		t.Errorf("xcodebuild must not run: fetch=%v build=%v", r.fetchArgs, r.buildArgs)
	}
}

// A build info without an index store must not leave a link from an earlier run behind.
func TestPrepareFromBuildInfoRemovesStaleIndexStoreLink(t *testing.T) {
	root := t.TempDir()
	dirs := ProjectDirs{Root: root, Build: filepath.Join(root, "build")}
	withStore := writeBuildInfo(t, minimalBuildInfo+`, "indexStorePath": "/out/index"}`)
	if _, err := PrepareFromBuildInfo(withStore, root, dirs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dirs.IndexStorePath()); err != nil {
		t.Fatalf("expected index store link after first Prepare: %v", err)
	}
	if _, err := PrepareFromBuildInfo(staticBuildInfoConfig(t).BuildInfoPath, root, dirs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dirs.IndexStorePath()); !os.IsNotExist(err) {
		t.Errorf("stale index store link must be removed, Lstat err = %v", err)
	}
}
