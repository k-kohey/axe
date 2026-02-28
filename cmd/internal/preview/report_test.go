package preview

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
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

func TestValidateReportFiles_NoPreviewBlocks(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "NoPreview.swift")
	src := "import SwiftUI\n\nstruct NoPreview: View {\n    var body: some View { Text(\"Hello\") }\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := validateReportFiles([]string{path})
	if err == nil {
		t.Fatal("expected error for file without #Preview blocks")
	}
	if !strings.Contains(err.Error(), "no #Preview blocks found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveOutputMode_SinglePreviewFile(t *testing.T) {
	blocks := []fileBlocks{{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 1)}}
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
		{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 2)},
	}
	_, err := resolveOutputMode("out.png", blocks)
	if err == nil {
		t.Fatal("expected error for multiple previews with file output")
	}
}

func TestResolveOutputMode_MultipleFilesWithFilePath(t *testing.T) {
	blocks := []fileBlocks{
		{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 1)},
		{file: "Bar.swift", previews: make([]analysis.PreviewBlock, 1)},
	}
	_, err := resolveOutputMode("out.png", blocks)
	if err == nil {
		t.Fatal("expected error for multiple files with file output")
	}
}

func TestResolveOutputMode_Directory(t *testing.T) {
	blocks := []fileBlocks{
		{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 2)},
		{file: "Bar.swift", previews: make([]analysis.PreviewBlock, 1)},
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

	blocks := []fileBlocks{{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 2)}}
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
		{file: "/a/FooView.swift", previews: make([]analysis.PreviewBlock, 1)},
		{file: "/b/BarView.swift", previews: make([]analysis.PreviewBlock, 1)},
	}
	if err := checkOutputCollisions("/out", blocks); err != nil {
		t.Fatalf("unexpected collision: %v", err)
	}
}

func TestCheckOutputCollisions_SameBaseName(t *testing.T) {
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", previews: make([]analysis.PreviewBlock, 1)},
		{file: "/b/FooView.swift", previews: make([]analysis.PreviewBlock, 1)},
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
		{file: "/a/FooView.swift", previews: make([]analysis.PreviewBlock, 2)},
		{file: "/b/FooView.swift", previews: make([]analysis.PreviewBlock, 1)},
	}
	err := checkOutputCollisions("/out", blocks)
	if err == nil {
		t.Fatal("expected collision error for same-name files with multiple previews")
	}
}

func TestCheckOutputCollisions_SameFileMultiplePreviews(t *testing.T) {
	// Single file with multiple previews should NOT collide (different indices).
	blocks := []fileBlocks{
		{file: "/a/FooView.swift", previews: make([]analysis.PreviewBlock, 3)},
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

	blocks := []fileBlocks{{file: "Foo.swift", previews: make([]analysis.PreviewBlock, 1)}}
	isDir, err := resolveOutputMode(filePath, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if isDir {
		t.Error("expected file mode for existing regular file")
	}
}

func TestNormalizeReportFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: "png"},
		{in: "png", want: "png"},
		{in: "md", want: "md"},
		{in: "MD", want: "md"},
		{in: "json", wantErr: true},
	}

	for _, tt := range tests {
		got, err := normalizeReportFormat(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("normalizeReportFormat(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeReportFormat(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeReportFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPrepareMarkdownOutputPaths(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "report.md")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareMarkdownOutputPaths(filePath); err == nil {
		t.Fatal("expected error for existing non-directory path")
	}

	targetDir := filepath.Join(tmp, "nested-report")
	mdPath, assetsDir, err := prepareMarkdownOutputPaths(targetDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdPath != filepath.Join(targetDir, markdownReportFileName) {
		t.Fatalf("unexpected markdown path: %s", mdPath)
	}
	if assetsDir != filepath.Join(targetDir, markdownAssetsDirName) {
		t.Fatalf("unexpected assets dir: %s", assetsDir)
	}
	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("expected output directory to exist: %v", err)
	}
	if _, err := os.Stat(assetsDir); err != nil {
		t.Fatalf("expected assets directory to exist: %v", err)
	}
}

func TestRenderMarkdownReport(t *testing.T) {
	png := []byte("fake-png")
	md := renderMarkdownReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Dark Mode",
			startLine: 12,
			png:       png,
		},
	}, "")

	if !strings.Contains(md, "# SwiftUI Preview Report") {
		t.Fatal("missing report title")
	}
	if !strings.Contains(md, "## FooView.swift") {
		t.Fatal("missing file header")
	}
	if !strings.Contains(md, "<table>") {
		t.Fatal("missing html table")
	}
	if !strings.Contains(md, "<strong>Preview 0</strong>") {
		t.Fatal("missing preview card header")
	}
	// With empty cwd, source falls back to filepath.Base.
	if !strings.Contains(md, "<code>FooView.swift:12</code>") {
		t.Fatal("missing source location")
	}
	wantData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	if !strings.Contains(md, wantData) {
		t.Fatal("missing embedded image data URI")
	}
}

func TestRenderMarkdownReport_MultiplePathsAndPreviews(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "",
			startLine: 8,
			png:       []byte("foo-0"),
		},
		{
			file:      "/tmp/FooView.swift",
			index:     1,
			title:     "Dark Mode",
			startLine: 20,
			png:       []byte("foo-1"),
		},
		{
			file:      "/tmp/BarView.swift",
			index:     0,
			title:     "Bar",
			startLine: 11,
			png:       []byte("bar-0"),
		},
	}, "")

	if !strings.Contains(md, "## FooView.swift") {
		t.Fatal("missing first file section")
	}
	if strings.Count(md, "## FooView.swift") != 1 {
		t.Fatal("expected single grouped section for FooView.swift")
	}
	if !strings.Contains(md, "## BarView.swift") {
		t.Fatal("missing second file section")
	}
	if !strings.Contains(md, "Title: <code>(Untitled)</code>") {
		t.Fatal("missing untitled fallback")
	}
	if !strings.Contains(md, "<strong>Preview 0</strong>") || !strings.Contains(md, "<strong>Preview 1</strong>") {
		t.Fatal("missing preview cards")
	}
	if !strings.Contains(md, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("foo-1"))) {
		t.Fatal("missing embedded image for preview 1")
	}
	if !strings.Contains(md, "data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("bar-0"))) {
		t.Fatal("missing embedded image for bar preview")
	}
	if strings.Index(md, "## FooView.swift") > strings.Index(md, "## BarView.swift") {
		t.Fatal("file group ordering should follow capture order")
	}
}

func TestWriteMarkdownAssets(t *testing.T) {
	tmp := t.TempDir()
	assetsDir := filepath.Join(tmp, markdownAssetsDirName)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	captures := []reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Foo",
			startLine: 10,
			png:       []byte("foo-png"),
		},
		{
			file:      "/tmp/BarView.swift",
			index:     1,
			title:     "Bar",
			startLine: 20,
			png:       []byte("bar-png"),
		},
	}

	out, err := writeMarkdownAssets(assetsDir, tmp, captures)
	if err != nil {
		t.Fatalf("writeMarkdownAssets error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("unexpected output length: %d", len(out))
	}
	for _, c := range out {
		if c.imageRef == "" {
			t.Fatal("expected imageRef to be populated")
		}
		if strings.HasPrefix(c.imageRef, "data:image/png;base64,") {
			t.Fatal("expected file reference, got data URI")
		}
		imgPath := filepath.Join(tmp, filepath.FromSlash(c.imageRef))
		if _, err := os.Stat(imgPath); err != nil {
			t.Fatalf("expected asset file %s: %v", imgPath, err)
		}
	}
}

func TestRenderMarkdownReport_UsesImageRefsWhenProvided(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Dark Mode",
			startLine: 12,
			png:       []byte("fake-png"),
			imageRef:  markdownAssetsDirName + "/FooView--deadbeef--preview-0.png",
		},
	}, "")

	if !strings.Contains(md, markdownAssetsDirName+"/FooView--deadbeef--preview-0.png") {
		t.Fatal("missing imageRef")
	}
	if strings.Contains(md, "data:image/png;base64,") {
		t.Fatal("did not expect data URI when imageRef is present")
	}
}

func TestRenderMarkdownReport_RelativeSource(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{
			file:      "/project/Sources/FooView.swift",
			index:     0,
			title:     "Test",
			startLine: 10,
			png:       []byte("x"),
		},
	}, "/project")

	if !strings.Contains(md, "<code>Sources/FooView.swift:10</code>") {
		t.Fatal("expected relative source path when cwd is set")
	}
}

func TestRenderMarkdownReport_OutsideCwdUsesBaseName(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{
			file:      "/other/path/FooView.swift",
			index:     0,
			title:     "Test",
			startLine: 5,
			png:       []byte("x"),
		},
	}, "/project")

	if !strings.Contains(md, "<code>FooView.swift:5</code>") {
		t.Fatal("expected base name for file outside cwd")
	}
	if strings.Contains(md, "/other/path") {
		t.Fatal("absolute path should not leak for files outside cwd")
	}
}

func TestPrepareMarkdownOutputPaths_EmptyOutput(t *testing.T) {
	_, _, err := prepareMarkdownOutputPaths("")
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestPrepareMarkdownOutputPaths_DotInDirName(t *testing.T) {
	tmp := t.TempDir()
	dotDir := filepath.Join(tmp, "reports.v1")

	mdPath, assetsDir, err := prepareMarkdownOutputPaths(dotDir)
	if err != nil {
		t.Fatalf("unexpected error for dot-containing directory name: %v", err)
	}
	if mdPath != filepath.Join(dotDir, markdownReportFileName) {
		t.Fatalf("unexpected markdown path: %s", mdPath)
	}
	if assetsDir != filepath.Join(dotDir, markdownAssetsDirName) {
		t.Fatalf("unexpected assets dir: %s", assetsDir)
	}
	if _, err := os.Stat(dotDir); err != nil {
		t.Fatalf("expected directory to be created: %v", err)
	}
}
