package analysis

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/procgroup"
)

//go:embed swift-analysis/Package.swift swift-analysis/Sources/AxeParser/*.swift swift-analysis/Sources/AxeParserCore/*.swift swift-analysis/Sources/AxeIndexReader/*.swift swift-analysis/Sources/AxeAnalysisProto/*.swift
var swiftAnalysisFS embed.FS

// Version is the axe CLI version (e.g. "v1.2.3"), injected by main at startup.
// Used to download matching pre-built binaries from GitHub Releases.
var Version string

var (
	swiftParserOnce sync.Once
	swiftParserPath string
	swiftParserErr  error

	indexReaderOnce sync.Once
	indexReaderPath string
	indexReaderErr  error
)

// ensureSwiftParser returns the path to the axe-parser binary.
// Resolution order: pre-installed sibling binary → GitHub Releases download → build from embedded source.
func ensureSwiftParser() (string, error) {
	swiftParserOnce.Do(func() {
		swiftParserPath, swiftParserErr = resolveSwiftBinary("axe-parser")
	})
	return swiftParserPath, swiftParserErr
}

// ensureIndexReader returns the path to the axe-index-reader binary.
// Resolution order: pre-installed sibling binary → GitHub Releases download → build from embedded source.
func ensureIndexReader() (string, error) {
	indexReaderOnce.Do(func() {
		indexReaderPath, indexReaderErr = resolveSwiftBinary("axe-index-reader")
	})
	return indexReaderPath, indexReaderErr
}

// resolveSwiftBinary locates a Swift CLI binary through a fallback chain:
//   - Dev builds:     sibling binary (mise deploy) → build from embedded source
//   - Release builds: download from GitHub Releases  → build from embedded source
func resolveSwiftBinary(product string) (string, error) {
	if Version == "dev" {
		if p := findPreinstalledBinary(product); p != "" {
			slog.Debug("Found pre-installed binary", "product", product, "path", p)
			return p, nil
		}
	}
	if p, err := downloadSwiftBinary(product); err != nil {
		slog.Debug("Download from GitHub Releases failed, falling back to source build", "product", product, "error", err)
	} else {
		return p, nil
	}
	return buildSwiftAnalysisProduct(product)
}

// findPreinstalledBinary checks if a pre-built binary exists in the same
// directory as the running axe executable (e.g. placed by mise deploy).
// Used only for dev builds; release builds use downloadSwiftBinary instead.
func findPreinstalledBinary(product string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	return findPreinstalledBinaryInDir(filepath.Dir(exe), product)
}

// findPreinstalledBinaryInDir checks if an executable binary named product
// exists in the given directory. Returns the full path or empty string.
func findPreinstalledBinaryInDir(dir, product string) string {
	candidate := filepath.Join(dir, product)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return ""
	}
	return candidate
}

const githubRepo = "k-kohey/axe"

// downloadSwiftBinary downloads a pre-built binary from the GitHub Release
// matching the current axe version. Skipped for dev builds.
func downloadSwiftBinary(product string) (string, error) {
	if Version == "" || Version == "dev" {
		return "", fmt.Errorf("no release version available")
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	binDir := filepath.Join(cacheDir, "axe", "swift-analysis", "releases", Version)
	binPath := filepath.Join(binDir, product)

	// Return cached download if present.
	if info, err := os.Stat(binPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		ok, verifyErr := verifyCachedBinaryHash(binPath)
		if verifyErr == nil && ok {
			slog.Debug("Using cached downloaded binary", "product", product, "path", binPath)
			return binPath, nil
		}
		slog.Warn("Cached binary hash verification failed; re-downloading",
			"product", product, "path", binPath, "err", verifyErr)
		_ = os.Remove(binPath)
		_ = os.Remove(checksumSidecarPath(binPath))
	}

	arch := runtime.GOARCH
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s-darwin-%s", githubRepo, Version, product, arch)

	slog.Debug("Downloading Swift binary from GitHub Releases", "product", product, "url", url)
	fmt.Fprintf(os.Stderr, "Downloading %s...\n", product)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL is constructed from a hardcoded GitHub repo constant
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", product, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", product, resp.StatusCode)
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	// Write to a temp file first, then rename for atomicity.
	tmp, err := os.CreateTemp(binDir, product+"-*")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing %s: %w", product, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("setting permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing temp file: %w", err)
	}

	// Verify checksum against checksums.txt from the same release.
	binaryName := fmt.Sprintf("%s-darwin-%s", product, arch)
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyChecksum(ctx, binaryName, actualHash); err != nil {
		return "", err
	}

	if err := os.Rename(tmpPath, binPath); err != nil {
		return "", fmt.Errorf("renaming to %s: %w", binPath, err)
	}
	if err := os.WriteFile(checksumSidecarPath(binPath), []byte(actualHash+"\n"), 0o600); err != nil {
		_ = os.Remove(binPath)
		return "", fmt.Errorf("writing checksum sidecar: %w", err)
	}

	slog.Debug("Downloaded Swift binary", "product", product, "path", binPath)
	return binPath, nil
}

func checksumSidecarPath(binPath string) string {
	return binPath + ".sha256"
}

func verifyCachedBinaryHash(binPath string) (bool, error) {
	expectedBytes, err := os.ReadFile(checksumSidecarPath(binPath))
	if err != nil {
		return false, err
	}
	expected := strings.TrimSpace(string(expectedBytes))
	if expected == "" {
		return false, fmt.Errorf("empty checksum sidecar")
	}

	f, err := os.Open(binPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	return actual == expected, nil
}

// verifyChecksum downloads checksums.txt from the GitHub release and verifies
// that the binary hash matches. Skips verification for old releases without checksums.txt.
func verifyChecksum(ctx context.Context, binaryName, actualHash string) error {
	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", githubRepo, Version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL is constructed from a hardcoded GitHub repo constant
	if err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// Old releases without checksums.txt: skip verification with a warning.
		slog.Warn("checksums.txt not found for this release, skipping verification", "version", Version)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading checksums.txt: HTTP %d", resp.StatusCode)
	}

	expected, err := parseChecksumFor(resp.Body, binaryName)
	if err != nil {
		return err
	}

	if actualHash != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", binaryName, expected, actualHash)
	}

	slog.Debug("Checksum verified", "binary", binaryName)
	return nil
}

// parseChecksumFor reads a checksums.txt formatted body and returns
// the expected hash for the given binary name.
func parseChecksumFor(r io.Reader, binaryName string) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "<hash>  <filename>" (shasum -a 256 output uses two spaces)
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == binaryName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading checksums.txt: %w", err)
	}
	return "", fmt.Errorf("no checksum found for %s in checksums.txt", binaryName)
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
	swiftVer, err := procgroup.Command(swiftVerCtx, "swift", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("getting swift version: %w", err)
	}
	h.Write(swiftVer)

	// macOS version
	macVerCtx, macVerCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer macVerCancel()
	macVer, _ := procgroup.Command(macVerCtx, "sw_vers", "-productVersion").Output()
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
	cmd := procgroup.Command(buildCtx, "swift", "build", "-c", "release", "--product", product, "--package-path", pkgPath)
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
