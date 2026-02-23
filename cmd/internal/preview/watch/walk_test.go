package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkSwiftDirs_FindsSwiftDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	srcDir := filepath.Join(root, "Sources")
	subDir := filepath.Join(srcDir, "Views")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "App.swift"), []byte("struct App {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "HogeView.swift"), []byte("struct HogeView {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	dirSet := make(map[string]bool)
	for _, d := range dirs {
		dirSet[d] = true
	}
	if !dirSet[srcDir] {
		t.Errorf("expected %s in results", srcDir)
	}
	if !dirSet[subDir] {
		t.Errorf("expected %s in results", subDir)
	}
}

func TestWalkSwiftDirs_SkipsHiddenDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	hiddenDir := filepath.Join(root, ".build")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, "Hidden.swift"), []byte("struct Hidden {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, d := range dirs {
		if d == hiddenDir {
			t.Errorf("should not include hidden directory %s", hiddenDir)
		}
	}
}

func TestWalkSwiftDirs_SkipsBuildAndDerivedData(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, name := range []string{"build", "DerivedData"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Generated.swift"), []byte("struct Generated {}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) != 0 {
		t.Errorf("expected 0 directories (build/DerivedData skipped), got %d: %v", len(dirs), dirs)
	}
}

func TestWalkSwiftDirs_IgnoresNonSwiftFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) != 0 {
		t.Errorf("expected 0 directories (no .swift files), got %d: %v", len(dirs), dirs)
	}
}

func TestWalkSwiftDirs_DeduplicatesDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "A.swift"), []byte("struct A {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "B.swift"), []byte("struct B {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) != 1 {
		t.Errorf("expected 1 directory (deduplicated), got %d: %v", len(dirs), dirs)
	}
}

func TestWalkSwiftDirs_EmptyDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	dirs, err := WalkSwiftDirs(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dirs) != 0 {
		t.Errorf("expected 0 directories for empty dir, got %d", len(dirs))
	}
}
