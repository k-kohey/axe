package build

import (
	"os"
	"path/filepath"
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

func TestBuildInfoSettings(t *testing.T) {
	path := writeBuildInfo(t, `{
	  "schemaVersion": 1, "module": "FeatureUI", "configuration": "Debug", "provider": "bazel",
	  "swiftcArguments": ["-module-name", "FeatureUI", "-target", "arm64-apple-ios17.0-simulator", "-swift-version", "6",
	                      "-Xcc", "-fmodule-map-file=/out/Clib.modulemap"],
	  "sourceFiles": ["/src/FeatureUI/View.swift"],
	  "moduleSearchPaths": ["/out/FeatureUI", "/out/app"],
	  "frameworkSearchPaths": ["/out/Frameworks"],
	  "indexStorePath": "/out/index",
	  "transitiveDependencies": [],
	  "appBundlePath": "/out/app/MyApp.app",
	  "appBundleId": "com.example.app"
	}`)
	info, err := LoadBuildInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := info.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ModuleName != "MyApp" || s.BundleID != "axe.com.example.app" || s.OriginalBundleID != "com.example.app" {
		t.Errorf("app identity: %+v", s)
	}
	if s.BuiltProductsDir != "/out/app" || s.DeploymentTarget != "17.0" || s.SwiftVersion != "6" {
		t.Errorf("paths/versions: %+v", s)
	}
	if len(s.ExtraIncludePaths) != 1 || s.ExtraIncludePaths[0] != "/out/FeatureUI" {
		t.Errorf("include paths (BuiltProductsDir must be de-duplicated): %v", s.ExtraIncludePaths)
	}
	if len(s.ExtraFrameworkPaths) != 1 || len(s.ExtraModuleMapFiles) != 1 || s.ExtraModuleMapFiles[0] != "/out/Clib.modulemap" {
		t.Errorf("framework/modulemap paths: %v %v", s.ExtraFrameworkPaths, s.ExtraModuleMapFiles)
	}
	if got := s.ModuleForSource("/src/FeatureUI/View.swift"); got != "FeatureUI" {
		t.Errorf("ModuleForSource = %q", got)
	}
	if got := s.ModuleForSource("/elsewhere.swift"); got != "" {
		t.Errorf("ModuleForSource for unknown file = %q", got)
	}

	dirs := ProjectDirs{Root: t.TempDir(), Build: filepath.Join(t.TempDir(), "build")}
	result, err := PrepareFromBuildInfo(path, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Built {
		t.Error("build info mode must not report a build")
	}
	if target, _ := os.Readlink(dirs.IndexStorePath()); target != "/out/index" {
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
