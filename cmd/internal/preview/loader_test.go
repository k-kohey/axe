package preview

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderCacheKey_IncludesAllInputs(t *testing.T) {
	base := loaderCacheKey("source", "/sdk/path", "17.0")

	// Same inputs must produce the same key
	if got := loaderCacheKey("source", "/sdk/path", "17.0"); got != base {
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
			if got := loaderCacheKey(tt.source, tt.sdk, tt.dep); got == base {
				t.Errorf("expected different key for %s, got same: %s", tt.name, got)
			}
		})
	}
}

func TestLoaderCacheKey_NoDelimiterCollision(t *testing.T) {
	// Fields that share a boundary must not collide.
	// e.g. shifting content across the delimiter boundary must produce a different key.
	a := loaderCacheKey("src", "/sdk/path", "17.0")
	b := loaderCacheKey("src\x00/sdk", "path", "17.0")
	if a == b {
		t.Error("delimiter collision: different field boundaries produced the same key")
	}
}

func TestLoaderCacheKey_Format(t *testing.T) {
	key := loaderCacheKey("src", "/sdk", "17.0")
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

	err = sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
	if err != nil {
		t.Fatalf("sendReloadCommand returned error: %v", err)
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

	err = sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
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

	err := sendReloadCommand(sockPath, "/tmp/thunk_0.dylib")
	if err == nil {
		t.Fatal("expected error when socket does not exist")
	}
}

// --- compileLoader DI tests ---

// fakeToolchainRunnerForLoader is a minimal fake for loader tests.
// It reuses fakeToolchainRunner from compiler_test.go via the same
// interface, but we define a separate helper to keep loader tests self-contained.
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

func (f *fakeLoaderToolchain) LinkDylib(_ context.Context, _ []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "LinkDylib")
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
	dirs := previewDirs{Loader: loaderDir}
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	dylibPath, err := compileLoader(context.Background(), dirs, "17.0", tc)
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
	dirs := previewDirs{Loader: loaderDir}
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	// First call: compiles.
	dylibPath1, err := compileLoader(context.Background(), dirs, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib so the cache check finds an existing file.
	if err := os.WriteFile(dylibPath1, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second call with a fresh toolchain: should skip compilation due to cache.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}
	dylibPath2, err := compileLoader(context.Background(), dirs, "17.0", tc2)
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
	dirs := previewDirs{Loader: loaderDir}
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}

	// First compile with deployment target 17.0.
	dylibPath, err := compileLoader(context.Background(), dirs, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib.
	if err := os.WriteFile(dylibPath, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second compile with different deployment target should recompile.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator"}
	_, err = compileLoader(context.Background(), dirs, "18.0", tc2)
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
	dirs := previewDirs{Loader: loaderDir}
	tc := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator15"}

	// First compile.
	dylibPath, err := compileLoader(context.Background(), dirs, "17.0", tc)
	if err != nil {
		t.Fatalf("first compile error: %v", err)
	}

	// Write a dummy dylib.
	if err := os.WriteFile(dylibPath, []byte("fake-dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second compile with different SDK path should recompile.
	tc2 := &fakeLoaderToolchain{sdkPathResult: "/sdk/iphonesimulator16"}
	_, err = compileLoader(context.Background(), dirs, "17.0", tc2)
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
			dirs := previewDirs{Loader: loaderDir}

			_, err := compileLoader(context.Background(), dirs, "17.0", tt.tc)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantError)
			}
		})
	}
}
