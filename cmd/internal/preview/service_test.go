package preview

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/simruntime"
)

func TestRunServeWithDepsUsesRuntimeManagerAndShutsItDown(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	projectDir := filepath.Join(tmpDir, "GenericApp.xcodeproj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(tmpDir, "Sources")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "GenericView.swift"), []byte("import SwiftUI\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pc, err := build.NewProjectConfig(projectDir, "", "GenericApp", "Debug")
	if err != nil {
		t.Fatal(err)
	}

	runtime := newFakeSimRuntimeManager()
	var gotDeviceSetPath string
	var out syncBuffer

	err = runServeWithDeps(context.Background(), pc, false, 32, 0, serveDeps{
		in:  bytes.NewReader(nil),
		out: &out,
		runners: func() (BuildRunner, ToolchainRunner, AppRunner, FileCopier, SourceLister) {
			return nopRunners()
		},
		newRuntime: func(deviceSetPath string) (simruntime.Manager, error) {
			gotDeviceSetPath = deviceSetPath
			return runtime, nil
		},
		newWatcher: defaultServeDeps().newWatcher,
	})
	if err != nil {
		t.Fatalf("runServeWithDeps: %v", err)
	}
	if gotDeviceSetPath == "" {
		t.Fatal("runtime factory was not called")
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.shutdown {
		t.Fatal("expected runtime.Shutdown to be called")
	}

	events := collectEvents(t, &out)
	if len(events) == 0 {
		t.Fatal("expected hello event")
	}
}
