package preview

import (
	"bufio"
	"crypto/sha256"
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
const loaderSource = `
#import <Foundation/Foundation.h>
#import <UIKit/UIKit.h>
#import <dlfcn.h>
#import <pthread.h>
#import <sys/socket.h>
#import <sys/un.h>
#import <unistd.h>

#define LOG(fmt, ...) NSLog(@"[axe-loader] " fmt, ##__VA_ARGS__)

static void *listener_thread(void *arg) {
    const char *sock_path = (const char *)arg;

    // Remove stale socket file if it exists
    unlink(sock_path);

    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) { LOG("socket() failed: %s", strerror(errno)); return NULL; }

    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strlcpy(addr.sun_path, sock_path, sizeof(addr.sun_path));

    if (bind(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        LOG("bind() failed: %s", strerror(errno));
        close(fd);
        return NULL;
    }
    if (listen(fd, 5) < 0) {
        LOG("listen() failed: %s", strerror(errno));
        close(fd);
        return NULL;
    }

    LOG("Listening on %s", sock_path);

    while (1) {
        int client = accept(fd, NULL, NULL);
        if (client < 0) { LOG("accept() failed: %s", strerror(errno)); continue; }

        char buf[4096];
        ssize_t n = read(client, buf, sizeof(buf) - 1);
        if (n <= 0) { close(client); continue; }
        buf[n] = '\0';

        // Trim trailing newline
        while (n > 0 && (buf[n-1] == '\n' || buf[n-1] == '\r')) { buf[--n] = '\0'; }

        LOG("Loading dylib: %s", buf);
        void *handle = dlopen(buf, RTLD_NOW);
        if (handle) {
            LOG("dlopen succeeded");
            typedef void (*RefreshFunc)(void);
            RefreshFunc refresh = (RefreshFunc)dlsym(handle, "axe_preview_refresh");
            dispatch_async(dispatch_get_main_queue(), ^{
                if (refresh) {
                    refresh();
                    LOG("Called axe_preview_refresh");
                }
            });
            write(client, "OK\n", 3);
        } else {
            const char *err = dlerror();
            LOG("dlopen failed: %s", err ? err : "unknown");
            char resp[4096];
            snprintf(resp, sizeof(resp), "ERR:%s\n", err ? err : "unknown");
            write(client, resp, strlen(resp));
        }
        close(client);
    }
    return NULL;
}

__attribute__((constructor))
static void axe_loader_init(void) {
    const char *sock_path = getenv("AXE_PREVIEW_SOCKET_PATH");
    if (!sock_path || strlen(sock_path) == 0) {
        LOG("AXE_PREVIEW_SOCKET_PATH not set, loader inactive");
        return;
    }

    // Copy the path so it persists for the thread
    char *path_copy = strdup(sock_path);
    pthread_t tid;
    if (pthread_create(&tid, NULL, listener_thread, path_copy) != 0) {
        LOG("pthread_create failed");
        free(path_copy);
        return;
    }
    pthread_detach(tid);
    LOG("Loader initialized, socket: %s", sock_path);

    // Trigger initial preview refresh when the app becomes active.
    // Uses both notification observer and a delayed fallback to handle
    // cases where didBecomeActive fires before observer is registered.
    static BOOL didInitialRefresh = NO;

    void (^doInitialRefresh)(void) = ^{
        if (didInitialRefresh) return;
        didInitialRefresh = YES;
        typedef void (*RefreshFunc)(void);
        RefreshFunc refresh = (RefreshFunc)dlsym(RTLD_DEFAULT, "axe_preview_refresh");
        if (refresh) {
            refresh();
            LOG("Initial preview refresh triggered");
        }
    };

    [[NSNotificationCenter defaultCenter]
        addObserverForName:UIApplicationDidBecomeActiveNotification
                    object:nil
                     queue:[NSOperationQueue mainQueue]
                usingBlock:^(NSNotification *note) {
        doInitialRefresh();
    }];

    // Fallback: if the notification was already posted before the observer
    // was registered (e.g. rapid relaunch), fire after a short delay.
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.5 * NSEC_PER_SEC)),
                   dispatch_get_main_queue(), ^{
        doInitialRefresh();
    });
}
`

// compileLoader compiles the Obj-C loader dylib for the simulator.
// The result is cached: recompilation is skipped if the source hash matches.
func compileLoader(dirs previewDirs, deploymentTarget string) (string, error) {
	if err := os.MkdirAll(dirs.Loader, 0o755); err != nil {
		return "", fmt.Errorf("creating loader dir: %w", err)
	}

	dylibPath := filepath.Join(dirs.Loader, "axe-preview-loader.dylib")
	hashPath := filepath.Join(dirs.Loader, "loader.sha256")

	// Check if source hash matches the cached build
	currentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(loaderSource)))
	if _, err := os.Stat(dylibPath); err == nil {
		if cached, err := os.ReadFile(hashPath); err == nil && string(cached) == currentHash {
			slog.Debug("Loader dylib cached, skipping compile", "path", dylibPath)
			return dylibPath, nil
		}
	}

	srcPath := filepath.Join(dirs.Loader, "loader.m")
	if err := os.WriteFile(srcPath, []byte(loaderSource), 0o644); err != nil {
		return "", fmt.Errorf("writing loader source: %w", err)
	}

	target := fmt.Sprintf("arm64-apple-ios%s-simulator", deploymentTarget)

	sdkPathOut, err := exec.Command("xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Output()
	if err != nil {
		return "", fmt.Errorf("getting simulator SDK path: %w", err)
	}
	sdk := strings.TrimSpace(string(sdkPathOut))

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
	if err := os.WriteFile(hashPath, []byte(currentHash), 0o644); err != nil {
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
	if strings.HasPrefix(resp, "ERR:") {
		return fmt.Errorf("loader error: %s", strings.TrimPrefix(resp, "ERR:"))
	}
	if resp != "OK" {
		return fmt.Errorf("unexpected loader response: %s", resp)
	}

	return nil
}
