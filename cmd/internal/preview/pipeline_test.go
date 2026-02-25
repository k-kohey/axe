package preview

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

// --- Fake ToolchainRunner (for parent tests that need nop toolchain) ---

type fakeToolchainRunner struct {
	sdkPathResult string
	sdkPathErr    error

	compileSwiftErr error
	compileCErr     error
	codesignErr     error

	compileSwiftArgs []string
	compileCArgs     []string
	codesignPath     string
	callOrder        []string
}

func (f *fakeToolchainRunner) SDKPath(_ context.Context, _ string) (string, error) {
	f.callOrder = append(f.callOrder, "SDKPath")
	if f.sdkPathErr != nil {
		return "", f.sdkPathErr
	}
	return f.sdkPathResult, nil
}

func (f *fakeToolchainRunner) CompileSwift(_ context.Context, args []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileSwift")
	f.compileSwiftArgs = args
	return nil, f.compileSwiftErr
}

func (f *fakeToolchainRunner) CompileC(_ context.Context, args []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileC")
	f.compileCArgs = args
	return nil, f.compileCErr
}

func (f *fakeToolchainRunner) Codesign(_ context.Context, path string) error {
	f.callOrder = append(f.callOrder, "Codesign")
	f.codesignPath = path
	return f.codesignErr
}

// buildTestCache creates an IndexStoreCache for test files.
func buildTestCache(entries map[string]*pb.IndexFileData) *analysis.IndexStoreCache {
	return analysis.NewIndexStoreCache(entries, map[string][]string{})
}

func TestParseTrackedFiles_SourceAndDependency(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "MainView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI
import MapKit

struct MainView: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	depPath := filepath.Join(dir, "ChildView.swift")
	if err := os.WriteFile(depPath, []byte(`import SwiftUI

struct ChildView: View {
    var body: some View {
        Text("Child")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "MainView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 5},
					},
				},
			},
		},
		depPath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "ChildView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
	})

	files := parseTrackedFiles(sourcePath, []string{sourcePath, depPath}, cache)

	if len(files) != 2 {
		t.Fatalf("files count = %d, want 2", len(files))
	}

	// Verify AbsPath
	if files[0].AbsPath != sourcePath {
		t.Errorf("files[0].AbsPath = %q, want %q", files[0].AbsPath, sourcePath)
	}
	if files[1].AbsPath != depPath {
		t.Errorf("files[1].AbsPath = %q, want %q", files[1].AbsPath, depPath)
	}

	// Verify FileName is base name only
	if files[0].FileName != "MainView.swift" {
		t.Errorf("files[0].FileName = %q, want %q", files[0].FileName, "MainView.swift")
	}
	if files[1].FileName != "ChildView.swift" {
		t.Errorf("files[1].FileName = %q, want %q", files[1].FileName, "ChildView.swift")
	}

	// Verify Types are populated
	if len(files[0].Types) == 0 {
		t.Error("files[0].Types should not be empty")
	}

	// Verify Imports are propagated (MapKit from source)
	if len(files[0].Imports) != 1 || files[0].Imports[0] != "import MapKit" {
		t.Errorf("files[0].Imports = %v, want [import MapKit]", files[0].Imports)
	}
}

func TestParseTrackedFiles_SourceParseErrorSkipped(t *testing.T) {
	dir := t.TempDir()

	// Source file with no View type — SourceFile will fail.
	sourcePath := filepath.Join(dir, "Broken.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct NotAView {
    var name: String
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	depPath := filepath.Join(dir, "HelperView.swift")
	if err := os.WriteFile(depPath, []byte(`import SwiftUI

struct HelperView: View {
    var body: some View {
        Text("Helper")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{Name: "NotAView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal"},
			},
		},
		depPath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "HelperView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
	})

	// In lenient mode, sourceFile parse error is skipped (not fatal).
	files := parseTrackedFiles(sourcePath, []string{sourcePath, depPath}, cache)

	// Only the dependency should be returned.
	if len(files) != 1 {
		t.Fatalf("files count = %d, want 1 (source skipped)", len(files))
	}
	if files[0].AbsPath != depPath {
		t.Errorf("files[0].AbsPath = %q, want %q", files[0].AbsPath, depPath)
	}
}

func TestParseTrackedFiles_DependencyParseErrorSkipped(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "MainView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct MainView: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dependency file that doesn't exist.
	depPath := filepath.Join(dir, "NonExistent.swift")
	analysis.ResetCache()

	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "MainView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
	})

	files := parseTrackedFiles(sourcePath, []string{sourcePath, depPath}, cache)

	if len(files) != 1 {
		t.Fatalf("files count = %d, want 1 (broken dep skipped)", len(files))
	}
	if files[0].AbsPath != sourcePath {
		t.Errorf("files[0].AbsPath = %q, want %q", files[0].AbsPath, sourcePath)
	}
}

func TestParseTrackedFiles_DependencyWithNoComputedProperties(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "MainView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct MainView: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dependency with only stored properties → DependencyFile succeeds
	// but returns 0 types (no computed properties), hitting len(types) == 0.
	depPath := filepath.Join(dir, "Model.swift")
	if err := os.WriteFile(depPath, []byte(`import Foundation

struct Model {
    var name: String
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "MainView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
		depPath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "Model", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					Members: []*pb.IndexMemberInfo{
						{Name: "name", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: false, Line: 4},
					},
				},
			},
		},
	})

	files := parseTrackedFiles(sourcePath, []string{sourcePath, depPath}, cache)

	// Only source should be returned; dep is skipped (no computed properties).
	if len(files) != 1 {
		t.Fatalf("files count = %d, want 1 (dep with no computed props skipped)", len(files))
	}
	if files[0].AbsPath != sourcePath {
		t.Errorf("files[0].AbsPath = %q, want %q", files[0].AbsPath, sourcePath)
	}
}

func TestHasFile(t *testing.T) {
	files := []analysis.FileThunkData{
		{AbsPath: "/a/B.swift"},
		{AbsPath: "/a/C.swift"},
	}

	if !hasFile(files, "/a/B.swift") {
		t.Error("hasFile should return true for existing path")
	}
	if hasFile(files, "/a/D.swift") {
		t.Error("hasFile should return false for missing path")
	}
	if hasFile(nil, "/a/B.swift") {
		t.Error("hasFile should return false for nil slice")
	}
}

func TestParseAndFilterTrackedFiles_SourceMissing(t *testing.T) {
	dir := t.TempDir()

	// Source file without View (parseSourceFile will fail → skipped).
	sourcePath := filepath.Join(dir, "NoView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import Foundation

struct Model {
    var name: String
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	// Cache with no View type.
	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{Name: "Model", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal"},
			},
		},
	})

	_, _, err := parseAndFilterTrackedFiles(sourcePath, []string{sourcePath}, cache)
	if err == nil {
		t.Fatal("expected error when sourceFile is not in results")
	}
}

func TestCompilePipeline_EmptyParseResult(t *testing.T) {
	dir := t.TempDir()

	// Source file without any View type → parseTrackedFiles returns empty.
	sourcePath := filepath.Join(dir, "NoView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import Foundation

struct Model {
    var name: String
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	// No View type in cache.
	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{Name: "Model", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal"},
			},
		},
	})

	bs := &buildSettings{ModuleName: "TestModule"}
	dirs := previewDirs{Thunk: dir}

	tc := &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}
	_, err := compilePipeline(context.Background(), sourcePath, []string{sourcePath}, cache, bs, dirs, "0", 0, tc)
	if err == nil {
		t.Fatal("expected error for empty parse result, got nil")
	}
	expected := "no types found in tracked files"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestUpdatePreviewCount_UpdatesState(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "V.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    V()
}

#Preview("Dark") {
    V()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := &watchState{
		previewIndex:    5, // out of range for 2 previews
		previewSelector: "5",
		previewCount:    0,
	}

	updatePreviewCount(sourcePath, ws)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.previewCount != 2 {
		t.Errorf("previewCount = %d, want 2", ws.previewCount)
	}
	// previewIndex was 5, which is >= 2, so it should reset to 0.
	if ws.previewIndex != 0 {
		t.Errorf("previewIndex = %d, want 0 (reset because out of range)", ws.previewIndex)
	}
	if ws.previewSelector != "0" {
		t.Errorf("previewSelector = %q, want \"0\"", ws.previewSelector)
	}
}

func TestUpdatePreviewCount_NoResetWhenInRange(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "V.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct V: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    V()
}

#Preview("Dark") {
    V()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := &watchState{
		previewIndex:    1,
		previewSelector: "1",
		previewCount:    1,
	}

	updatePreviewCount(sourcePath, ws)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.previewCount != 2 {
		t.Errorf("previewCount = %d, want 2", ws.previewCount)
	}
	// previewIndex 1 is valid for 2 previews, should not reset.
	if ws.previewIndex != 1 {
		t.Errorf("previewIndex = %d, want 1 (still valid)", ws.previewIndex)
	}
}

func TestUpdatePreviewCount_UnparseableFile(t *testing.T) {
	ws := &watchState{
		previewIndex:    1,
		previewSelector: "1",
		previewCount:    3,
	}

	// Non-existent file → parsePreviewBlocks returns error → state unchanged.
	updatePreviewCount("/nonexistent/file.swift", ws)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.previewCount != 3 {
		t.Errorf("previewCount = %d, want 3 (unchanged)", ws.previewCount)
	}
	if ws.previewIndex != 1 {
		t.Errorf("previewIndex = %d, want 1 (unchanged)", ws.previewIndex)
	}
	if ws.previewSelector != "1" {
		t.Errorf("previewSelector = %q, want \"1\" (unchanged)", ws.previewSelector)
	}
}

func TestUpdatePreviewCount_FileWithNoPreviewBlocks(t *testing.T) {
	dir := t.TempDir()

	// File with no #Preview blocks → len(blocks) == 0 → state unchanged.
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

	ws := &watchState{
		previewIndex:    1,
		previewSelector: "1",
		previewCount:    3,
	}

	updatePreviewCount(sourcePath, ws)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.previewCount != 3 {
		t.Errorf("previewCount = %d, want 3 (unchanged)", ws.previewCount)
	}
	if ws.previewIndex != 1 {
		t.Errorf("previewIndex = %d, want 1 (unchanged)", ws.previewIndex)
	}
}

func TestParseAndFilterTrackedFiles_NoCollision(t *testing.T) {
	dir := t.TempDir()

	sourcePath := filepath.Join(dir, "MainView.swift")
	if err := os.WriteFile(sourcePath, []byte(`import SwiftUI

struct MainView: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	depPath := filepath.Join(dir, "HelperView.swift")
	if err := os.WriteFile(depPath, []byte(`import SwiftUI

struct HelperView: View {
    var body: some View {
        Text("Helper")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	analysis.ResetCache()

	cache := buildTestCache(map[string]*pb.IndexFileData{
		sourcePath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "MainView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
		depPath: {
			Types: []*pb.IndexTypeInfo{
				{
					Name: "HelperView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Members: []*pb.IndexMemberInfo{
						{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					},
				},
			},
		},
	})

	files, tracked, err := parseAndFilterTrackedFiles(sourcePath, []string{sourcePath, depPath}, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("files count = %d, want 2", len(files))
	}
	if len(tracked) != 2 {
		t.Errorf("tracked count = %d, want 2", len(tracked))
	}
}
