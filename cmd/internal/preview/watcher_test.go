package preview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyChange_BodyOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "V.swift")

	base := `import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}
`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	prevSkeleton, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	// Change only the body content.
	modified := `import SwiftUI

struct V: View {
    var body: some View {
        Text("World")
            .bold()
    }
}
`
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	strategy, newSkeleton := classifyChange(path, prevSkeleton)
	if strategy != strategyHotReload {
		t.Errorf("expected strategyHotReload, got %d", strategy)
	}
	if newSkeleton != prevSkeleton {
		t.Errorf("skeleton should not change for body-only edit")
	}
}

func TestClassifyChange_StoredPropertyAdded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "V.swift")

	base := `import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}
`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	prevSkeleton, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	// Add a stored property → structural change.
	modified := `import SwiftUI

struct V: View {
    @State var count = 0

    var body: some View {
        Text("Hello")
    }
}
`
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	strategy, newSkeleton := classifyChange(path, prevSkeleton)
	if strategy != strategyRebuild {
		t.Errorf("expected strategyRebuild, got %d", strategy)
	}
	if newSkeleton == prevSkeleton {
		t.Errorf("skeleton should differ for structural change")
	}
}

func TestClassifyChange_FileUnreadable(t *testing.T) {
	strategy, newSkeleton := classifyChange("/nonexistent/path.swift", "abc")
	if strategy != strategyRebuild {
		t.Errorf("expected strategyRebuild for unreadable file, got %d", strategy)
	}
	if newSkeleton != "" {
		t.Errorf("expected empty skeleton for unreadable file, got %q", newSkeleton)
	}
}

func TestClassifyChange_PreviewBodyOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "V.swift")

	base := `import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    V()
}
`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	prevSkeleton, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	// Change only the #Preview body.
	modified := `import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    V()
        .preferredColorScheme(.dark)
}
`
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	strategy, _ := classifyChange(path, prevSkeleton)
	if strategy != strategyHotReload {
		t.Errorf("expected strategyHotReload for #Preview body change, got %d", strategy)
	}
}

func TestSwitchFile_NonexistentPath(t *testing.T) {
	ws := &watchState{}
	pc, _ := NewProjectConfig("dummy.xcodeproj", "", "Scheme", "")
	bs := &buildSettings{}
	dirs := previewDirs{}

	wctx := watchContext{}
	err := switchFile(context.Background(), "/nonexistent/path/File.swift", pc, bs, dirs, wctx, ws)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	expected := "source file not found: /nonexistent/path/File.swift"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestSwitchFile_CancelledContext(t *testing.T) {
	dir := t.TempDir()

	// Create a valid source file so os.Stat passes.
	sourcePath := filepath.Join(dir, "V.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()

	ws := &watchState{}
	pc, _ := NewProjectConfig(filepath.Join(dir, "dummy.xcodeproj"), "", "Scheme", "")
	bs := &buildSettings{ModuleName: "TestModule"}
	dirs := previewDirs{Thunk: dir}
	wctx := watchContext{}

	// Cancel the context before calling switchFile.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := switchFile(ctx, sourcePath, pc, bs, dirs, wctx, ws)
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestReadStdinCommands_JSONServeMode(t *testing.T) {
	// Create a pipe to simulate stdin.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan stdinCommand, 10)

	// Save and restore os.Stdin.
	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	go readStdinCommands(ch, true)

	// Write a JSON switchFile command.
	data, _ := json.Marshal(stdinCommand{Type: "switchFile", Path: "/path/to/File.swift"})
	_, _ = w.Write(data)
	_, _ = w.WriteString("\n")
	cmd := <-ch
	if cmd.Type != "switchFile" || cmd.Path != "/path/to/File.swift" {
		t.Errorf("expected switchFile command, got %+v", cmd)
	}

	// Write a JSON tap command.
	data, _ = json.Marshal(stdinCommand{Type: "tap", X: 0.5, Y: 0.3})
	_, _ = w.Write(data)
	_, _ = w.WriteString("\n")
	cmd = <-ch
	if cmd.Type != "tap" || cmd.X != 0.5 || cmd.Y != 0.3 {
		t.Errorf("expected tap command, got %+v", cmd)
	}

	// Write a JSON swipe command.
	data, _ = json.Marshal(stdinCommand{Type: "swipe", StartX: 0.1, StartY: 0.2, EndX: 0.8, EndY: 0.9, Duration: 0.5})
	_, _ = w.Write(data)
	_, _ = w.WriteString("\n")
	cmd = <-ch
	if cmd.Type != "swipe" || cmd.StartX != 0.1 || cmd.EndX != 0.8 || cmd.Duration != 0.5 {
		t.Errorf("expected swipe command, got %+v", cmd)
	}

	// Write a JSON text command.
	data, _ = json.Marshal(stdinCommand{Type: "text", Value: "hello"})
	_, _ = w.Write(data)
	_, _ = w.WriteString("\n")
	cmd = <-ch
	if cmd.Type != "text" || cmd.Value != "hello" {
		t.Errorf("expected text command, got %+v", cmd)
	}

	// Write a JSON nextPreview command.
	data, _ = json.Marshal(stdinCommand{Type: "nextPreview"})
	_, _ = w.Write(data)
	_, _ = w.WriteString("\n")
	cmd = <-ch
	if cmd.Type != "nextPreview" {
		t.Errorf("expected nextPreview command, got %+v", cmd)
	}

	// Empty line → nextPreview.
	_, _ = w.WriteString("\n")
	cmd = <-ch
	if cmd.Type != "nextPreview" {
		t.Errorf("expected nextPreview for empty line, got %+v", cmd)
	}

	_ = w.Close()
}

func TestReadStdinCommands_LegacyServeMode(t *testing.T) {
	// Test that non-JSON lines in serve mode fallback to switchFile.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan stdinCommand, 10)

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	go readStdinCommands(ch, true)

	// Non-JSON line in serve mode → legacy file path switchFile.
	_, _ = w.WriteString("/path/to/File.swift\n")
	cmd := <-ch
	if cmd.Type != "switchFile" || cmd.Path != "/path/to/File.swift" {
		t.Errorf("expected legacy switchFile, got %+v", cmd)
	}

	_ = w.Close()
}

func TestReadStdinCommands_NonServeMode(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan stdinCommand, 10)

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	go readStdinCommands(ch, false)

	// In non-serve mode, any input → nextPreview.
	_, _ = w.WriteString("/path/to/File.swift\n")
	cmd := <-ch
	if cmd.Type != "nextPreview" {
		t.Errorf("expected nextPreview in non-serve mode, got %+v", cmd)
	}

	_ = w.Close()
}

func TestClassifyChange_ImportAdded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "V.swift")

	base := `import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}
`
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	prevSkeleton, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	modified := `import SwiftUI
import MapKit

struct V: View {
    var body: some View {
        Text("Hello")
    }
}
`
	if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	strategy, _ := classifyChange(path, prevSkeleton)
	if strategy != strategyRebuild {
		t.Errorf("expected strategyRebuild for import addition, got %d", strategy)
	}
}

func TestStdinCommand_JSONRoundtrip(t *testing.T) {
	tests := []stdinCommand{
		{Type: "switchFile", Path: "/path/to/file.swift"},
		{Type: "nextPreview"},
		{Type: "tap", X: 0.5, Y: 0.3},
		{Type: "swipe", StartX: 0.1, StartY: 0.2, EndX: 0.9, EndY: 0.8, Duration: 0.5},
		{Type: "text", Value: "hello world"},
	}

	for _, tc := range tests {
		data, err := json.Marshal(tc)
		if err != nil {
			t.Fatalf("marshal %+v: %v", tc, err)
		}

		var got stdinCommand
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}

		if got.Type != tc.Type {
			t.Errorf("type mismatch: got %q, want %q", got.Type, tc.Type)
		}
	}
}
