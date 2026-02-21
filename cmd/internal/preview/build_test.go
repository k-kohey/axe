package preview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCompilerPaths_IncludePaths(t *testing.T) {
	bs, dirs := setupRespFile(t, `-I/path/to/headers
-I/path/to/more/headers
-I
/path/split/across/lines
`)
	extractCompilerPaths(bs, dirs)

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

	extractCompilerPaths(bs, dirs)

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

	extractCompilerPaths(bs, dirs)

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
	extractCompilerPaths(bs, dirs)

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
	extractCompilerPaths(bs, dirs)

	if len(bs.ExtraIncludePaths) != 2 {
		t.Fatalf("ExtraIncludePaths count = %d, want 2", len(bs.ExtraIncludePaths))
	}
}

func TestExtractCompilerPaths_NoRespFile(t *testing.T) {
	bs := &buildSettings{ModuleName: "NoSuchModule", BuiltProductsDir: "/tmp/none"}
	dirs := previewDirs{Build: t.TempDir()}

	// Should not panic or error, just silently return.
	extractCompilerPaths(bs, dirs)

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
	extractCompilerPaths(bs, dirs)

	if len(bs.ExtraIncludePaths) != 1 {
		t.Fatalf("ExtraIncludePaths count = %d, want 1", len(bs.ExtraIncludePaths))
	}
}

// setupRespFile creates a temporary directory structure mimicking the xcodebuild
// intermediates layout and writes content as a swiftc response file.
func setupRespFile(t *testing.T, content string) (*buildSettings, previewDirs) {
	t.Helper()
	root := t.TempDir()
	dirs := previewDirs{Build: root}
	bs := &buildSettings{ModuleName: "TestModule", BuiltProductsDir: "/products/dir"}

	respDir := filepath.Join(root, "Build", "Intermediates.noindex",
		"TestProject.build", "Debug-iphonesimulator",
		"TestModule.build", "Objects-normal", "arm64")
	if err := os.MkdirAll(respDir, 0o755); err != nil {
		t.Fatal(err)
	}
	respPath := filepath.Join(respDir, "arguments-abc123.resp")
	if err := os.WriteFile(respPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return bs, dirs
}
