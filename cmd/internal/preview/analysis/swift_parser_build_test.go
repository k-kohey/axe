package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindPreinstalledBinaryInDir(t *testing.T) {
	t.Parallel()

	t.Run("returns empty when binary does not exist", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if got := findPreinstalledBinaryInDir(dir, "axe-parser"); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("returns path when executable binary exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bin := filepath.Join(dir, "axe-parser")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := findPreinstalledBinaryInDir(dir, "axe-parser"); got != bin {
			t.Errorf("expected %q, got %q", bin, got)
		}
	})

	t.Run("returns empty when path is a directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "axe-parser"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := findPreinstalledBinaryInDir(dir, "axe-parser"); got != "" {
			t.Errorf("expected empty string for directory, got %q", got)
		}
	})

	t.Run("returns empty when file is not executable", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "axe-parser"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := findPreinstalledBinaryInDir(dir, "axe-parser"); got != "" {
			t.Errorf("expected empty string for non-executable, got %q", got)
		}
	})
}

func TestResolveSwiftBinary_DevUsesPreinstalled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bin := filepath.Join(dir, "axe-parser")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Dev build should find sibling binary without any version marker
	if got := findPreinstalledBinaryInDir(dir, "axe-parser"); got != bin {
		t.Errorf("expected %q, got %q", bin, got)
	}
}

func TestParseChecksumFor(t *testing.T) {
	t.Parallel()

	checksums := "abc123def456  axe-parser-darwin-arm64\n" +
		"789abc012def  axe-parser-darwin-amd64\n" +
		"111222333444  axe-index-reader-darwin-arm64\n"

	t.Run("finds matching binary", func(t *testing.T) {
		t.Parallel()
		got, err := parseChecksumFor(strings.NewReader(checksums), "axe-parser-darwin-arm64")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "abc123def456" {
			t.Errorf("expected abc123def456, got %q", got)
		}
	})

	t.Run("returns error for missing binary", func(t *testing.T) {
		t.Parallel()
		_, err := parseChecksumFor(strings.NewReader(checksums), "axe-nonexistent-darwin-arm64")
		if err == nil {
			t.Fatal("expected error for missing binary")
		}
	})

	t.Run("handles empty input", func(t *testing.T) {
		t.Parallel()
		_, err := parseChecksumFor(strings.NewReader(""), "axe-parser-darwin-arm64")
		if err == nil {
			t.Fatal("expected error for empty checksums")
		}
	})

	t.Run("ignores blank lines", func(t *testing.T) {
		t.Parallel()
		input := "\n\nabc123  target-binary\n\n"
		got, err := parseChecksumFor(strings.NewReader(input), "target-binary")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "abc123" {
			t.Errorf("expected abc123, got %q", got)
		}
	})

	t.Run("ignores malformed lines", func(t *testing.T) {
		t.Parallel()
		input := "malformed-line\nabc123  target-binary\n"
		got, err := parseChecksumFor(strings.NewReader(input), "target-binary")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "abc123" {
			t.Errorf("expected abc123, got %q", got)
		}
	})
}

// TestDownloadSwiftBinary is not parallel because it mutates the global Version variable.
func TestDownloadSwiftBinary(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()

	t.Run("skips download for dev version", func(t *testing.T) {
		Version = "dev"
		_, err := downloadSwiftBinary("axe-parser")
		if err == nil {
			t.Fatal("expected error for dev version")
		}
	})

	t.Run("skips download for empty version", func(t *testing.T) {
		Version = ""
		_, err := downloadSwiftBinary("axe-parser")
		if err == nil {
			t.Fatal("expected error for empty version")
		}
	})

	t.Run("returns cached binary without network access", func(t *testing.T) {
		version := fmt.Sprintf("v0.0.0-test-%d", os.Getpid())
		Version = version

		cacheDir := t.TempDir()
		t.Setenv("HOME", cacheDir)

		// os.UserCacheDir() returns $HOME/Library/Caches on macOS
		binDir := filepath.Join(cacheDir, "Library", "Caches", "axe", "swift-analysis", "releases", version)
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		binPath := filepath.Join(binDir, "axe-parser")
		if err := os.WriteFile(binPath, []byte("cached-binary"), 0o755); err != nil {
			t.Fatal(err)
		}

		got, err := downloadSwiftBinary("axe-parser")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != binPath {
			t.Errorf("expected %q, got %q", binPath, got)
		}
	})

	t.Run("returns error for non-existent release", func(t *testing.T) {
		Version = "v99.99.99"
		t.Setenv("HOME", t.TempDir())

		_, err := downloadSwiftBinary("axe-parser")
		if err == nil {
			t.Fatal("expected error for non-existent release")
		}
	})
}
