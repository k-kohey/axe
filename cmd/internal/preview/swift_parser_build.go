package preview

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed swift-parser/Package.swift swift-parser/Sources/AxeParser/*.swift swift-parser/Sources/AxeParserCore/*.swift
var swiftParserFS embed.FS

var (
	swiftParserOnce sync.Once
	swiftParserPath string
	swiftParserErr  error
)

// ensureSwiftParser builds (or locates the cached) axe-parser binary.
// The binary is cached at ~/Library/Caches/axe/swift-parser/<hash>/axe-parser.
// The cache key is a hash of the embedded source + `swift --version` + macOS version.
func ensureSwiftParser() (string, error) {
	swiftParserOnce.Do(func() {
		swiftParserPath, swiftParserErr = buildSwiftParser()
	})
	return swiftParserPath, swiftParserErr
}

func buildSwiftParser() (string, error) {
	// Collect all embedded files dynamically so new source files
	// are automatically included without editing this list.
	var entries []string
	if err := fs.WalkDir(swiftParserFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			entries = append(entries, path)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("walking embedded FS: %w", err)
	}
	sort.Strings(entries) // deterministic hash order

	// Compute cache key from embedded sources + swift version + macOS version.
	h := sha256.New()
	for _, name := range entries {
		data, err := swiftParserFS.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("reading embedded %s: %w", name, err)
		}
		h.Write(data)
	}

	// swift --version
	swiftVer, err := exec.Command("swift", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("getting swift version: %w", err)
	}
	h.Write(swiftVer)

	// macOS version
	macVer, _ := exec.Command("sw_vers", "-productVersion").Output()
	h.Write(macVer)

	cacheKey := fmt.Sprintf("%x", h.Sum(nil))

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	binDir := filepath.Join(cacheDir, "axe", "swift-parser", cacheKey)
	binPath := filepath.Join(binDir, "axe-parser")

	// Check if cached binary exists.
	if _, err := os.Stat(binPath); err == nil {
		slog.Debug("Swift parser cached", "path", binPath)
		return binPath, nil
	}

	// Extract embedded sources to a temp directory and build.
	tmpDir, err := os.MkdirTemp("", "axe-swift-parser-build-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for _, name := range entries {
		data, err := swiftParserFS.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("reading embedded %s: %w", name, err)
		}
		dst := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("creating dir for %s: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", name, err)
		}
	}

	// Create placeholder for the test target directory so SPM doesn't
	// complain about the missing Tests/AxeParserTests source directory.
	testDir := filepath.Join(tmpDir, "swift-parser", "Tests", "AxeParserTests")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return "", fmt.Errorf("creating test placeholder dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "Placeholder.swift"), []byte("// placeholder\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing test placeholder: %w", err)
	}

	fmt.Println("Building Swift parser (first run, this may take a moment)...")
	pkgPath := filepath.Join(tmpDir, "swift-parser")

	cmd := exec.Command("swift", "build", "-c", "release", "--product", "axe-parser", "--package-path", pkgPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building swift parser: %w", err)
	}

	// Find the built binary.
	srcBin := filepath.Join(pkgPath, ".build", "release", "axe-parser")
	if _, err := os.Stat(srcBin); err != nil {
		return "", fmt.Errorf("built binary not found at %s: %w", srcBin, err)
	}

	// Copy to cache.
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}
	data, err := os.ReadFile(srcBin)
	if err != nil {
		return "", fmt.Errorf("reading built binary: %w", err)
	}
	if err := os.WriteFile(binPath, data, 0o755); err != nil {
		return "", fmt.Errorf("caching binary: %w", err)
	}

	// Trim old cache entries (keep only current).
	parentDir := filepath.Dir(binDir)
	dirEntries, _ := os.ReadDir(parentDir)
	for _, d := range dirEntries {
		if d.IsDir() && d.Name() != cacheKey {
			old := filepath.Join(parentDir, d.Name())
			slog.Debug("Removing old swift-parser cache", "path", old)
			_ = os.RemoveAll(old)
		}
	}

	slog.Debug("Swift parser built and cached", "path", binPath)
	swiftVersion := strings.TrimSpace(string(swiftVer))
	slog.Debug("Swift version", "version", swiftVersion)

	return binPath, nil
}
