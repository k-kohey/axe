package preview

import (
	"github.com/k-kohey/axe/internal/preview/build"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAppBundle_DirectPath(t *testing.T) {
	root := t.TempDir()
	productsDir := filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(productsDir, "MyApp.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{ModuleName: "MyApp", BuiltProductsDir: productsDir}
	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: root}}

	got, err := resolveAppBundle(bs, dirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != appDir {
		t.Errorf("got %q, want %q", got, appDir)
	}
}

func TestResolveAppBundle_GlobFallback(t *testing.T) {
	root := t.TempDir()
	// Place app in a different configuration directory than BuiltProductsDir.
	altDir := filepath.Join(root, "Build", "Products", "Release-iphonesimulator")
	appDir := filepath.Join(altDir, "MyApp.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// BuiltProductsDir points to a non-existent configuration.
	bs := &build.Settings{ModuleName: "MyApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: root}}

	got, err := resolveAppBundle(bs, dirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != appDir {
		t.Errorf("got %q, want %q", got, appDir)
	}
}

func TestResolveAppBundle_NotFound(t *testing.T) {
	root := t.TempDir()
	bs := &build.Settings{ModuleName: "NoApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: root}}

	_, err := resolveAppBundle(bs, dirs)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHasPreviousBuild_True(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "Build", "Products", "Debug-iphonesimulator", "MyApp.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bs := &build.Settings{ModuleName: "MyApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: root}}

	if !hasPreviousBuild(bs, dirs) {
		t.Error("hasPreviousBuild = false, want true")
	}
}

func TestHasPreviousBuild_False(t *testing.T) {
	root := t.TempDir()
	bs := &build.Settings{ModuleName: "MyApp", BuiltProductsDir: filepath.Join(root, "Build", "Products", "Debug-iphonesimulator")}
	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: root}}

	if hasPreviousBuild(bs, dirs) {
		t.Error("hasPreviousBuild = true, want false")
	}
}
