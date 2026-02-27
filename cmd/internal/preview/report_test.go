package preview

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateReportFiles_NotSwift(t *testing.T) {
	_, err := validateReportFiles([]string{"foo.txt"})
	if err == nil {
		t.Fatal("expected error for non-Swift file")
	}
}

func TestValidateReportFiles_NotFound(t *testing.T) {
	_, err := validateReportFiles([]string{"/nonexistent/Foo.swift"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveOutputMode_SinglePreviewFile(t *testing.T) {
	blocks := []fileBlocks{{file: "Foo.swift", count: 1}}
	isDir, err := resolveOutputMode("out.png", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Error("expected file mode for out.png")
	}
}

func TestResolveOutputMode_MultiplePreviewsWithFilePath(t *testing.T) {
	blocks := []fileBlocks{
		{file: "Foo.swift", count: 2},
	}
	_, err := resolveOutputMode("out.png", blocks)
	if err == nil {
		t.Fatal("expected error for multiple previews with file output")
	}
}

func TestResolveOutputMode_Directory(t *testing.T) {
	blocks := []fileBlocks{
		{file: "Foo.swift", count: 2},
		{file: "Bar.swift", count: 1},
	}
	isDir, err := resolveOutputMode("screens", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !isDir {
		t.Error("expected directory mode for 'screens'")
	}
}

func TestResolveOutputMode_ExistingDirectory(t *testing.T) {
	// Even with a dot in the name, an existing directory should be treated as directory.
	dir := t.TempDir()
	dotDir := filepath.Join(dir, "my.screenshots")
	if err := os.Mkdir(dotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	blocks := []fileBlocks{{file: "Foo.swift", count: 2}}
	isDir, err := resolveOutputMode(dotDir, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !isDir {
		t.Error("expected directory mode for existing directory with dot in name")
	}
}

func TestComputeOutputPath_Directory(t *testing.T) {
	tests := []struct {
		output     string
		sourceFile string
		index      int
		want       string
	}{
		{
			output:     "/screens",
			sourceFile: "/path/to/FooView.swift",
			index:      0,
			want:       filepath.Join("/screens", "FooView--preview-0.png"),
		},
		{
			output:     "/screens",
			sourceFile: "/path/to/BarView.swift",
			index:      2,
			want:       filepath.Join("/screens", "BarView--preview-2.png"),
		},
	}

	for _, tt := range tests {
		got := computeOutputPath(tt.output, tt.sourceFile, tt.index, true)
		if got != tt.want {
			t.Errorf("computeOutputPath(%q, %q, %d, true) = %q, want %q",
				tt.output, tt.sourceFile, tt.index, got, tt.want)
		}
	}
}

func TestComputeOutputPath_File(t *testing.T) {
	got := computeOutputPath("/out.png", "/path/to/FooView.swift", 0, false)
	if got != "/out.png" {
		t.Errorf("computeOutputPath file mode = %q, want /out.png", got)
	}
}

func TestCheckOutputCollisions_NoCollision(t *testing.T) {
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", count: 1},
		{file: "/b/BarView.swift", count: 1},
	}
	if err := checkOutputCollisions("/out", blocks); err != nil {
		t.Fatalf("unexpected collision: %v", err)
	}
}

func TestCheckOutputCollisions_SameBaseName(t *testing.T) {
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", count: 1},
		{file: "/b/FooView.swift", count: 1},
	}
	err := checkOutputCollisions("/out", blocks)
	if err == nil {
		t.Fatal("expected collision error for same-name files")
	}
}

func TestCheckOutputCollisions_SameBaseNameMultiplePreviews(t *testing.T) {
	// Different directories, same file name, multiple previews each.
	// preview-0 from /a/ collides with preview-0 from /b/.
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", count: 2},
		{file: "/b/FooView.swift", count: 1},
	}
	err := checkOutputCollisions("/out", blocks)
	if err == nil {
		t.Fatal("expected collision error for same-name files with multiple previews")
	}
}

func TestCheckOutputCollisions_SameFileMultiplePreviews(t *testing.T) {
	// Single file with multiple previews should NOT collide (different indices).
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", count: 3},
	}
	if err := checkOutputCollisions("/out", blocks); err != nil {
		t.Fatalf("unexpected collision for single file with multiple previews: %v", err)
	}
}

func TestResolveOutputMode_ExistingFile(t *testing.T) {
	// An existing regular file (with extension) should be treated as file mode.
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "out.png")
	if err := os.WriteFile(filePath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks := []fileBlocks{{file: "Foo.swift", count: 1}}
	isDir, err := resolveOutputMode(filePath, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Error("expected file mode for existing regular file")
	}
}
