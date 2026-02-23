package analysis

import (
	"context"
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
	"time"
)

//go:embed swift-analysis/Package.swift swift-analysis/Sources/AxeParser/*.swift swift-analysis/Sources/AxeParserCore/*.swift swift-analysis/Sources/AxeIndexReader/*.swift
var swiftAnalysisFS embed.FS

var (
	swiftParserOnce sync.Once
	swiftParserPath string
	swiftParserErr  error

	indexReaderOnce sync.Once
	indexReaderPath string
	indexReaderErr  error
)

// ensureSwiftParser builds (or locates the cached) axe-parser binary.
// The binary is cached at ~/Library/Caches/axe/swift-analysis/<hash>/axe-parser.
// The cache key is a hash of the embedded source + `swift --version` + macOS version.
func ensureSwiftParser() (string, error) {
	swiftParserOnce.Do(func() {
		swiftParserPath, swiftParserErr = buildSwiftAnalysisProduct("axe-parser")
	})
	return swiftParserPath, swiftParserErr
}

// ensureIndexReader builds (or locates the cached) axe-index-reader binary.
// Uses the same cache directory and key as axe-parser (same package source).
func ensureIndexReader() (string, error) {
	indexReaderOnce.Do(func() {
		indexReaderPath, indexReaderErr = buildSwiftAnalysisProduct("axe-index-reader")
	})
	return indexReaderPath, indexReaderErr
}

// buildSwiftAnalysisProduct builds a specific product from the SwiftAnalysis package.
// Products share the same embedded source, cache key, and build directory.
func buildSwiftAnalysisProduct(product string) (string, error) {
	// Collect all embedded files dynamically so new source files
	// are automatically included without editing this list.
	var entries []string
	if err := fs.WalkDir(swiftAnalysisFS, ".", func(path string, d fs.DirEntry, err error) error {
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
		data, err := swiftAnalysisFS.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("reading embedded %s: %w", name, err)
		}
		h.Write(data)
	}

	// swift --version
	swiftVerCtx, swiftVerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer swiftVerCancel()
	swiftVer, err := exec.CommandContext(swiftVerCtx, "swift", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("getting swift version: %w", err)
	}
	h.Write(swiftVer)

	// macOS version
	macVerCtx, macVerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer macVerCancel()
	macVer, _ := exec.CommandContext(macVerCtx, "sw_vers", "-productVersion").Output()
	h.Write(macVer)

	cacheKey := fmt.Sprintf("%x", h.Sum(nil))

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	binDir := filepath.Join(cacheDir, "axe", "swift-analysis", cacheKey)
	binPath := filepath.Join(binDir, product)

	// Check if cached binary exists.
	if _, err := os.Stat(binPath); err == nil {
		slog.Debug("Swift analysis product cached", "product", product, "path", binPath)
		return binPath, nil
	}

	// Extract embedded sources to a temp directory and build.
	tmpDir, err := os.MkdirTemp("", "axe-swift-analysis-build-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for _, name := range entries {
		data, err := swiftAnalysisFS.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("reading embedded %s: %w", name, err)
		}
		dst := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("creating dir for %s: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return "", fmt.Errorf("writing %s: %w", name, err)
		}
	}

	// Create placeholder for the test target directory so SPM doesn't
	// complain about the missing Tests/AxeParserTests source directory.
	testDir := filepath.Join(tmpDir, "swift-analysis", "Tests", "AxeParserTests")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return "", fmt.Errorf("creating test placeholder dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "Placeholder.swift"), []byte("// placeholder\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing test placeholder: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Building %s (first run, this may take a moment)...\n", product)
	pkgPath := filepath.Join(tmpDir, "swift-analysis")

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer buildCancel()
	cmd := exec.CommandContext(buildCtx, "swift", "build", "-c", "release", "--product", product, "--package-path", pkgPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("building %s: %w", product, err)
	}

	// Find the built binary.
	srcBin := filepath.Join(pkgPath, ".build", "release", product)
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
			slog.Debug("Removing old swift-analysis cache", "path", old)
			_ = os.RemoveAll(old)
		}
	}

	slog.Debug("Swift analysis product built and cached", "product", product, "path", binPath)
	swiftVersion := strings.TrimSpace(string(swiftVer))
	slog.Debug("Swift version", "version", swiftVersion)

	return binPath, nil
}
