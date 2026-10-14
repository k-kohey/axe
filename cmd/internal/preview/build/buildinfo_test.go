package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRunBuildInfoCommandDoesNotEvaluateValues(t *testing.T) {
	const printJSON = `printf '{"f":"%s","d":"%s"}' `
	cases := []struct {
		name, template, file, device string
	}{
		// Unquoted expansion is word-split, so these values contain no whitespace.
		{"unquoted", printJSON + `$AXE_PREVIEW_FILE $AXE_PREVIEW_DEVICE`, "$(id)'x.swift", "$(id)'dev"},
		{"double quoted", printJSON + `"$AXE_PREVIEW_FILE" "$AXE_PREVIEW_DEVICE"`, "$(printf INJECTED)'x.swift", "$(printf INJECTED)'dev"},
		{"double quoted, spaces", printJSON + `"$AXE_PREVIEW_FILE" "$AXE_PREVIEW_DEVICE"`, "a b/$(touch PWNED)`touch PWNED`.swift", "$(touch PWNED) dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			root := t.TempDir() // the command runs here, so a substituted touch would create root/PWNED
			path, err := RunBuildInfoCommand(ProjectConfig{Root: root, BuildInfoCommand: tc.template}, tc.file, tc.device)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "PWNED")); err == nil {
				t.Fatal("command substitution in a value was executed")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				F string `json:"f"`
				D string `json:"d"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("%v: %s", err, data)
			}
			if got.F != tc.file || got.D != tc.device {
				t.Errorf("values changed: file=%q device=%q", got.F, got.D)
			}
		})
	}
}

func TestRunBuildInfoCommandDefaultDevice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path, err := RunBuildInfoCommand(ProjectConfig{Root: t.TempDir(), BuildInfoCommand: `printf %s "$AXE_PREVIEW_DEVICE"`}, "/a.swift", "")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "generic" {
		t.Errorf("device = %q, want generic", data)
	}
}

func TestRunBuildInfoCommandRejectsPlaceholders(t *testing.T) {
	for _, template := range []string{`./build {file}`, `./build "{file}"`, `./build --device {device}`} {
		_, err := RunBuildInfoCommand(ProjectConfig{Root: t.TempDir(), BuildInfoCommand: template}, "/a.swift", "dev")
		if err == nil {
			t.Fatalf("%q: expected error", template)
		}
		if !strings.Contains(err.Error(), `"$AXE_PREVIEW_FILE"`) || !strings.Contains(err.Error(), `"$AXE_PREVIEW_DEVICE"`) {
			t.Errorf("%q: error lacks replacement hint: %v", template, err)
		}
	}
}
