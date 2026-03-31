package codegen

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withLoaderBackoffs(t *testing.T, waitBackoffs, reloadBackoffs []time.Duration) {
	t.Helper()

	prevWait := waitForReadyBackoffs
	prevReload := sendReloadBackoffs
	waitForReadyBackoffs = waitBackoffs
	sendReloadBackoffs = reloadBackoffs
	t.Cleanup(func() {
		waitForReadyBackoffs = prevWait
		sendReloadBackoffs = prevReload
	})
}

func TestLoaderCacheKey_IncludesAllInputs(t *testing.T) {
	base := LoaderCacheKey("source", "/sdk/path", "17.0")

	// Same inputs must produce the same key
	if got := LoaderCacheKey("source", "/sdk/path", "17.0"); got != base {
		t.Errorf("same inputs produced different keys: %s vs %s", got, base)
	}

	// Changing any single input must produce a different key
	tests := []struct {
		name             string
		source, sdk, dep string
	}{
		{"different source", "source2", "/sdk/path", "17.0"},
		{"different sdk", "source", "/sdk/path2", "17.0"},
		{"different deployment target", "source", "/sdk/path", "18.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LoaderCacheKey(tt.source, tt.sdk, tt.dep); got == base {
				t.Errorf("expected different key for %s, got same: %s", tt.name, got)
			}
		})
	}
}

func TestLoaderCacheKey_NoDelimiterCollision(t *testing.T) {
	// Fields that share a boundary must not collide.
	// e.g. shifting content across the delimiter boundary must produce a different key.
	a := LoaderCacheKey("src", "/sdk/path", "17.0")
	b := LoaderCacheKey("src\x00/sdk", "path", "17.0")
	if a == b {
		t.Error("delimiter collision: different field boundaries produced the same key")
	}
}

func TestLoaderCacheKey_Format(t *testing.T) {
	key := LoaderCacheKey("src", "/sdk", "17.0")
	// SHA256 hex digest is 64 characters
	if len(key) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars: %s", len(key), key)
	}
}

func TestSendReloadCommand_OK(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Start a mock loader server
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	var received string
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		received = string(buf[:n])
		_, _ = conn.Write([]byte("OK\n"))
	}()

	err = SendReloadCommand(context.Background(), sockPath, "/tmp/thunk_0.dylib")
	if err != nil {
		t.Fatalf("SendReloadCommand returned error: %v", err)
	}

	<-done
	if received != "/tmp/thunk_0.dylib\n" {
		t.Errorf("received = %q, want %q", received, "/tmp/thunk_0.dylib\n")
	}
}

func TestSendReloadCommand_Error(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("ERR:dlopen failed: symbol not found\n"))
	}()

	err = SendReloadCommand(context.Background(), sockPath, "/tmp/thunk_0.dylib")
	if err == nil {
		t.Fatal("expected error for ERR response")
	}
	if got := err.Error(); got != "loader error: dlopen failed: symbol not found" {
		t.Errorf("error = %q", got)
	}
}

func TestSendReloadCommand_NoSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	// Remove socket file to make sure it doesn't exist
	_ = os.Remove(sockPath)

	err := SendReloadCommand(context.Background(), sockPath, "/tmp/thunk_0.dylib")
	if err == nil {
		t.Fatal("expected error when socket does not exist")
	}
}

func TestWaitForReady_OK(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	// Accept and immediately close (simulating loader behavior on disconnect).
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	if err := WaitForReady(context.Background(), sockPath); err != nil {
		t.Fatalf("WaitForReady returned error: %v", err)
	}
}

func TestWaitForReady_NoSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	err := WaitForReady(context.Background(), sockPath)
	if err == nil {
		t.Fatal("expected error when socket does not exist")
	}
}

func TestWaitForReady_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "nonexistent.sock")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := WaitForReady(ctx, sockPath)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %q, want to contain 'context canceled'", err.Error())
	}
}

func TestWaitForReady_Retry(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Start the listener after a short delay so the first dial attempt fails.
	go func() {
		time.Sleep(100 * time.Millisecond)
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			return
		}
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := WaitForReady(ctx, sockPath); err != nil {
		t.Fatalf("WaitForReady returned error: %v", err)
	}
}

func TestLoaderSocketBackoffPolicies_SplitBehavior(t *testing.T) {
	sockPath := filepath.Join("/tmp", "axe-loader-split.sock")
	_ = os.Remove(sockPath)
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	withLoaderBackoffs(t,
		[]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond},
		[]time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
	)

	go func() {
		time.Sleep(60 * time.Millisecond)
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			return
		}
		defer func() { _ = ln.Close() }()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				if n == 0 {
					return
				}
				_, _ = c.Write([]byte("OK\n"))
			}(conn)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := SendReloadCommand(ctx, sockPath, "/tmp/thunk_0.dylib"); err == nil {
		t.Fatal("expected SendReloadCommand to fail with shorter reload backoff")
	}

	if err := WaitForReady(ctx, sockPath); err != nil {
		t.Fatalf("WaitForReady should succeed with longer ready backoff: %v", err)
	}
}

// --- CompileLoader DI tests ---

// fakeLoaderToolchain is a minimal fake for loader tests.
type fakeLoaderToolchain struct {
	sdkPathResult string
	sdkPathErr    error
	compileCErr   error
	codesignErr   error
	callOrder     []string
}

func (f *fakeLoaderToolchain) SDKPath(_ context.Context, _ string) (string, error) {
	f.callOrder = append(f.callOrder, "SDKPath")
	return f.sdkPathResult, f.sdkPathErr
}

func (f *fakeLoaderToolchain) CompileSwift(_ context.Context, _ []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileSwift")
	return nil, nil
}

func (f *fakeLoaderToolchain) CompileC(_ context.Context, _ []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileC")
	return nil, f.compileCErr
}

func (f *fakeLoaderToolchain) Codesign(_ context.Context, _ string) error {
	f.callOrder = append(f.callOrder, "Codesign")
	return f.codesignErr
}

func TestCompileLoader_Success(t *testing.T) {
	t.Parallel()

	loaderDir := filepath.Join(t.TempDir(), "loader")
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	dylibPath, err := CompileLoader(context.Background(), loaderDir, "17.0", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify dylib path.
	if !strings.HasSuffix(dylibPath, "axe-preview-loader.dylib") {
		t.Errorf("dylibPath = %q, want suffix %q", dylibPath, "axe-preview-loader.dylib")
	}

	// Verify call order: SDKPath -> CompileC -> Codesign.
	wantOrder := []string{"SDKPath", "CompileC", "Codesign"}
	if len(tc.callOrder) != len(wantOrder) {
		t.Fatalf("callOrder = %v, want %v", tc.callOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if tc.callOrder[i] != want {
			t.Errorf("callOrder[%d] = %q, want %q", i, tc.callOrder[i], want)
		}
	}

	// Verify source file was written.
	srcPath := filepath.Join(loaderDir, "loader.m")
	if _, err := os.Stat(srcPath); err != nil {
		t.Errorf("loader source should exist at %s: %v", srcPath, err)
	}

	// Verify hash file was written.
	hashPath := filepath.Join(loaderDir, "loader.sha256")
	if _, err := os.Stat(hashPath); err != nil {
		t.Errorf("hash file should exist at %s: %v", hashPath, err)
	}
}

func TestCompileLoader_CacheHit(t *testing.T) {
	t.Parallel()

	loaderDir := filepath.Join(t.TempDir(), "loader")
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	// First call: compiles.
	dylibPath1, err := CompileLoader(context.Background(), loaderDir, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib so the cache check finds an existing file.
	if err := os.WriteFile(dylibPath1, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second call with a fresh toolchain: should skip compilation due to cache.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}
	dylibPath2, err := CompileLoader(context.Background(), loaderDir, "17.0", tc2)
	if err != nil {
		t.Fatalf("second compile error: %v", err)
	}

	if dylibPath1 != dylibPath2 {
		t.Errorf("dylib paths differ: %q vs %q", dylibPath1, dylibPath2)
	}

	// Cache hit should only call SDKPath (for hash computation), not CompileC.
	if len(tc2.callOrder) != 1 || tc2.callOrder[0] != "SDKPath" {
		t.Errorf("cached call should only call SDKPath, got %v", tc2.callOrder)
	}
}

func TestCompileLoader_CacheMiss_DifferentTarget(t *testing.T) {
	t.Parallel()

	loaderDir := filepath.Join(t.TempDir(), "loader")
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	// First compile with deployment target 17.0.
	dylibPath, err := CompileLoader(context.Background(), loaderDir, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib.
	if err := os.WriteFile(dylibPath, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second compile with different deployment target should recompile.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}
	_, err = CompileLoader(context.Background(), loaderDir, "18.0", tc2)
	if err != nil {
		t.Fatalf("second compile error: %v", err)
	}

	// Should have recompiled: SDKPath + CompileC + Codesign.
	wantOrder := []string{"SDKPath", "CompileC", "Codesign"}
	if len(tc2.callOrder) != len(wantOrder) {
		t.Fatalf("callOrder = %v, want %v", tc2.callOrder, wantOrder)
	}
}

func TestCompileLoader_CacheMiss_DifferentSDK(t *testing.T) {
	t.Parallel()

	loaderDir := filepath.Join(t.TempDir(), "loader")
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator15"}

	// First compile.
	dylibPath, err := CompileLoader(context.Background(), loaderDir, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib.
	if err := os.WriteFile(dylibPath, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second compile with different SDK path should recompile.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator16"}
	_, err = CompileLoader(context.Background(), loaderDir, "17.0", tc2)
	if err != nil {
		t.Fatalf("second compile error: %v", err)
	}

	wantOrder := []string{"SDKPath", "CompileC", "Codesign"}
	if len(tc2.callOrder) != len(wantOrder) {
		t.Fatalf("callOrder = %v, want %v (SDK changed, should recompile)", tc2.callOrder, wantOrder)
	}
}

func TestCompileLoader_ErrorPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tc        *fakeLoaderToolchain
		wantError string
	}{
		{
			name: "SDKPath failure",
			tc: &fakeLoaderToolchain{
				sdkPathErr: errors.New("xcrun not found"),
			},
			wantError: "getting simulator SDK path",
		},
		{
			name: "CompileC failure",
			tc: &fakeLoaderToolchain{
				sdkPathResult: "/sdk/iphonesimulator",
				compileCErr:   errors.New("clang error"),
			},
			wantError: "compiling loader",
		},
		{
			name: "Codesign failure",
			tc: &fakeLoaderToolchain{
				sdkPathResult: "/sdk/iphonesimulator",
				codesignErr:   errors.New("signing error"),
			},
			wantError: "codesigning loader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loaderDir := filepath.Join(t.TempDir(), "loader")

			_, err := CompileLoader(context.Background(), loaderDir, "17.0", tt.tc)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantError)
			}
		})
	}
}
