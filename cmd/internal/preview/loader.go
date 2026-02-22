package preview

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// loaderSource is an Objective-C source that acts as a dylib loader injected
// into the simulator app via DYLD_INSERT_LIBRARIES.
//
// On load (__attribute__((constructor))):
//  1. Reads the Unix domain socket path from AXE_PREVIEW_SOCKET_PATH env var
//  2. Spawns a background pthread that listens on that socket
//  3. For each connection: reads a dylib path, dlopen()s it, calls
//     axe_preview_refresh via dlsym to replace rootViewController with preview content
//  4. Registers a UIApplicationDidBecomeActiveNotification observer (+fallback timer)
//     for initial preview refresh on app launch
//
//go:embed loader_source/loader.m
var loaderSource string

// loaderCacheKey computes a SHA256 cache key from the loader source,
// SDK path, and deployment target. This ensures the cached dylib is
// invalidated when any of these change (e.g. Xcode update).
func loaderCacheKey(source, sdk, deploymentTarget string) string {
	hashInput := source + "\x00" + sdk + "\x00" + deploymentTarget
	return fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))
}

// compileLoader compiles the Obj-C loader dylib for the simulator.
// The result is cached: recompilation is skipped if the source hash matches.
func compileLoader(dirs previewDirs, deploymentTarget string) (string, error) {
	if err := os.MkdirAll(dirs.Loader, 0o755); err != nil {
		return "", fmt.Errorf("creating loader dir: %w", err)
	}

	dylibPath := filepath.Join(dirs.Loader, "axe-preview-loader.dylib")
	hashPath := filepath.Join(dirs.Loader, "loader.sha256")

	// SDK path is needed both for cache key and compilation
	sdkPathOut, err := exec.Command("xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Output()
	if err != nil {
		return "", fmt.Errorf("getting simulator SDK path: %w", err)
	}
	sdk := strings.TrimSpace(string(sdkPathOut))

	// Check if source hash matches the cached build
	currentHash := loaderCacheKey(loaderSource, sdk, deploymentTarget)
	if _, err := os.Stat(dylibPath); err == nil {
		if cached, err := os.ReadFile(hashPath); err == nil && string(cached) == currentHash {
			slog.Debug("Loader dylib cached, skipping compile", "path", dylibPath)
			return dylibPath, nil
		}
	}

	srcPath := filepath.Join(dirs.Loader, "loader.m")
	if err := os.WriteFile(srcPath, []byte(loaderSource), 0o600); err != nil {
		return "", fmt.Errorf("writing loader source: %w", err)
	}

	target := fmt.Sprintf("arm64-apple-ios%s-simulator", deploymentTarget)

	compileArgs := []string{
		"xcrun", "clang",
		"-dynamiclib",
		"-fobjc-arc",
		"-target", target,
		"-isysroot", sdk,
		"-framework", "Foundation",
		"-framework", "UIKit",
		"-o", dylibPath,
		srcPath,
	}
	slog.Debug("Compiling loader", "args", compileArgs)
	if out, err := exec.Command(compileArgs[0], compileArgs[1:]...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("compiling loader: %w\n%s", err, out)
	}

	// Ad-hoc codesign
	if out, err := exec.Command("codesign", "--force", "--sign", "-", dylibPath).CombinedOutput(); err != nil {
		return "", fmt.Errorf("codesigning loader: %w\n%s", err, out)
	}

	// Save source hash for cache invalidation
	if err := os.WriteFile(hashPath, []byte(currentHash), 0o600); err != nil {
		slog.Warn("Failed to write loader hash", "err", err)
	}

	slog.Debug("Loader dylib ready", "path", dylibPath)
	return dylibPath, nil
}

// sendReloadCommand connects to the loader's Unix domain socket and sends
// a dylib path for hot-reload. It retries with exponential backoff if the
// socket is not yet ready.
func sendReloadCommand(socketPath, dylibPath string) error {
	backoffs := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}

	var conn net.Conn
	var lastErr error
	for _, d := range backoffs {
		conn, lastErr = net.DialTimeout("unix", socketPath, 1*time.Second)
		if lastErr == nil {
			break
		}
		slog.Debug("Socket not ready, retrying", "backoff", d, "err", lastErr)
		time.Sleep(d)
	}
	if lastErr != nil {
		return fmt.Errorf("connecting to loader socket: %w", lastErr)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := fmt.Fprintf(conn, "%s\n", dylibPath); err != nil {
		return fmt.Errorf("sending dylib path: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("reading response: %w", err)
		}
		return fmt.Errorf("no response from loader")
	}

	resp := scanner.Text()
	if after, ok := strings.CutPrefix(resp, "ERR:"); ok {
		return fmt.Errorf("loader error: %s", after)
	}
	if resp != "OK" {
		return fmt.Errorf("unexpected loader response: %s", resp)
	}

	return nil
}
