package preview

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

var updateGolden = flag.Bool("update", false, "update golden files")

const goldenVersion = "abc1234"

// assertGolden compares got against the golden file at path.
// If -update is set, it writes got to the golden file instead.
func assertGolden(t *testing.T, goldenPath string, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("updating golden file: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to create)", goldenPath, err)
	}
	if string(want) != got {
		// Find first difference for a useful error message.
		wantLines := strings.Split(string(want), "\n")
		gotLines := strings.Split(got, "\n")
		for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
			var wl, gl string
			if i < len(wantLines) {
				wl = wantLines[i]
			}
			if i < len(gotLines) {
				gl = gotLines[i]
			}
			if wl != gl {
				t.Fatalf("golden mismatch in %s at line %d:\n  want: %q\n  got:  %q",
					filepath.Base(goldenPath), i+1, wl, gl)
			}
		}
		t.Fatalf("golden mismatch in %s (different lengths: want %d lines, got %d lines)",
			filepath.Base(goldenPath), len(wantLines), len(gotLines))
	}
}

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
		{in: "html", want: "html"},
		{in: "HTML", want: "html"},
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

func TestPrepareReportOutputPaths(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "report.md")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareReportOutputPaths(filePath, markdownReportFileName); err == nil {
		t.Fatal("expected error for existing non-directory path")
	}

	targetDir := filepath.Join(tmp, "nested-report")
	mdPath, assetsDir, err := prepareReportOutputPaths(targetDir, markdownReportFileName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdPath != filepath.Join(targetDir, markdownReportFileName) {
		t.Fatalf("unexpected markdown path: %s", mdPath)
	}
	if assetsDir != filepath.Join(targetDir, reportAssetsDirName) {
		t.Fatalf("unexpected assets dir: %s", assetsDir)
	}
	if _, err := os.Stat(targetDir); err != nil {
		t.Fatalf("expected output directory to exist: %v", err)
	}
	if _, err := os.Stat(assetsDir); err != nil {
		t.Fatalf("expected assets directory to exist: %v", err)
	}
}

func TestPrepareReportOutputPaths_HTML(t *testing.T) {
	tmp := t.TempDir()
	targetDir := filepath.Join(tmp, "html-report")
	htmlPath, assetsDir, err := prepareReportOutputPaths(targetDir, htmlReportFileName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if htmlPath != filepath.Join(targetDir, htmlReportFileName) {
		t.Fatalf("unexpected html path: %s", htmlPath)
	}
	if assetsDir != filepath.Join(targetDir, reportAssetsDirName) {
		t.Fatalf("unexpected assets dir: %s", assetsDir)
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
	}, nil, "", "test")

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
	}, nil, "", "test")

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

func TestWriteReportAssets(t *testing.T) {
	tmp := t.TempDir()
	assetsDir := filepath.Join(tmp, reportAssetsDirName)
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

	out, err := writeReportAssets(assetsDir, tmp, captures)
	if err != nil {
		t.Fatalf("writeReportAssets error: %v", err)
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
			imageRef:  reportAssetsDirName + "/FooView--deadbeef--preview-0.png",
		},
	}, nil, "", "test")

	if !strings.Contains(md, reportAssetsDirName+"/FooView--deadbeef--preview-0.png") {
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
	}, nil, "/project", "test")

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
	}, nil, "/project", "test")

	if !strings.Contains(md, "<code>FooView.swift:5</code>") {
		t.Fatal("expected base name for file outside cwd")
	}
	if strings.Contains(md, "/other/path") {
		t.Fatal("absolute path should not leak for files outside cwd")
	}
}

func TestPrepareReportOutputPaths_EmptyOutput(t *testing.T) {
	_, _, err := prepareReportOutputPaths("", markdownReportFileName)
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestPrepareReportOutputPaths_DotInDirName(t *testing.T) {
	tmp := t.TempDir()
	dotDir := filepath.Join(tmp, "reports.v1")

	mdPath, assetsDir, err := prepareReportOutputPaths(dotDir, markdownReportFileName)
	if err != nil {
		t.Fatalf("unexpected error for dot-containing directory name: %v", err)
	}
	if mdPath != filepath.Join(dotDir, markdownReportFileName) {
		t.Fatalf("unexpected markdown path: %s", mdPath)
	}
	if assetsDir != filepath.Join(dotDir, reportAssetsDirName) {
		t.Fatalf("unexpected assets dir: %s", assetsDir)
	}
	if _, err := os.Stat(dotDir); err != nil {
		t.Fatalf("expected directory to be created: %v", err)
	}
}

// --- Shared helper tests ---

func TestGroupCapturesByFile(t *testing.T) {
	captures := []reportCapture{
		{file: "/tmp/FooView.swift", index: 0},
		{file: "/tmp/FooView.swift", index: 1},
		{file: "/tmp/BarView.swift", index: 0},
	}
	groups := groupCapturesByFile(captures)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].file != "/tmp/FooView.swift" {
		t.Fatalf("expected first group to be FooView, got %s", groups[0].file)
	}
	if len(groups[0].captures) != 2 {
		t.Fatalf("expected 2 captures in first group, got %d", len(groups[0].captures))
	}
	if groups[1].file != "/tmp/BarView.swift" {
		t.Fatalf("expected second group to be BarView, got %s", groups[1].file)
	}
	if len(groups[1].captures) != 1 {
		t.Fatalf("expected 1 capture in second group, got %d", len(groups[1].captures))
	}
}

func TestResolveSourceDisplay(t *testing.T) {
	// Relative path within cwd
	got := resolveSourceDisplay("/project/Sources/FooView.swift", "/project")
	if got != "Sources/FooView.swift" {
		t.Fatalf("expected relative path, got %s", got)
	}

	// Outside cwd falls back to base name
	got = resolveSourceDisplay("/other/path/FooView.swift", "/project")
	if got != "FooView.swift" {
		t.Fatalf("expected base name, got %s", got)
	}

	// Empty cwd falls back to base name
	got = resolveSourceDisplay("/any/path/FooView.swift", "")
	if got != "FooView.swift" {
		t.Fatalf("expected base name for empty cwd, got %s", got)
	}
}

func TestResolveImageSrc(t *testing.T) {
	// With imageRef
	c := reportCapture{imageRef: "assets/foo.png", png: []byte("data")}
	got := resolveImageSrc(c)
	if got != "assets/foo.png" {
		t.Fatalf("expected imageRef, got %s", got)
	}

	// Without imageRef, falls back to data URI
	c = reportCapture{png: []byte("fake-png")}
	got = resolveImageSrc(c)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fake-png"))
	if got != want {
		t.Fatalf("expected data URI, got %s", got)
	}
}

// --- HTML rendering tests ---

func TestRenderHTMLReport(t *testing.T) {
	png := []byte("fake-png")
	html := renderHTMLReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Dark Mode",
			startLine: 12,
			png:       png,
		},
	}, nil, "", "test")

	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Fatal("missing DOCTYPE")
	}
	if !strings.Contains(html, "<title>SwiftUI Preview Report</title>") {
		t.Fatal("missing title")
	}
	if !strings.Contains(html, "<style>") {
		t.Fatal("missing CSS")
	}
	if !strings.Contains(html, "class=\"card\"") {
		t.Fatal("missing card element")
	}
	if !strings.Contains(html, "FooView.swift:12") {
		t.Fatal("missing source location")
	}
	wantData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	if !strings.Contains(html, wantData) {
		t.Fatal("missing embedded image data URI")
	}
}

func TestRenderHTMLReport_MultiplePathsAndPreviews(t *testing.T) {
	html := renderHTMLReport([]reportCapture{
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
	}, nil, "", "test")

	if !strings.Contains(html, "FooView.swift") {
		t.Fatal("missing first file section")
	}
	if !strings.Contains(html, "BarView.swift") {
		t.Fatal("missing second file section")
	}
	if !strings.Contains(html, "(Untitled)") {
		t.Fatal("missing untitled fallback")
	}
	if !strings.Contains(html, "Preview 0") || !strings.Contains(html, "Preview 1") {
		t.Fatal("missing preview cards")
	}
	// Verify grouping order
	fooIdx := strings.Index(html, "FooView.swift")
	barIdx := strings.Index(html, "BarView.swift")
	if fooIdx > barIdx {
		t.Fatal("file group ordering should follow capture order")
	}
	if !strings.Contains(html, "3 previews") {
		t.Fatal("missing total preview count in summary")
	}
}

func TestRenderHTMLReport_UsesImageRefsWhenProvided(t *testing.T) {
	html := renderHTMLReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Dark Mode",
			startLine: 12,
			png:       []byte("fake-png"),
			imageRef:  reportAssetsDirName + "/FooView--deadbeef--preview-0.png",
		},
	}, nil, "", "test")

	if !strings.Contains(html, reportAssetsDirName+"/FooView--deadbeef--preview-0.png") {
		t.Fatal("missing imageRef in src")
	}
	if strings.Contains(html, "data:image/png;base64,") {
		t.Fatal("did not expect data URI when imageRef is present")
	}
}

func TestRenderHTMLReport_DarkModeCSS(t *testing.T) {
	html := renderHTMLReport([]reportCapture{
		{
			file:      "/tmp/FooView.swift",
			index:     0,
			title:     "Test",
			startLine: 1,
			png:       []byte("x"),
		},
	}, nil, "", "test")

	if !strings.Contains(html, "prefers-color-scheme: dark") {
		t.Fatal("missing dark mode media query")
	}
}

func TestRenderHTMLReport_HTMLEscaping(t *testing.T) {
	// File name uses a payload without '/' to avoid filepath.Base splitting.
	out := renderHTMLReport([]reportCapture{
		{
			file:      "/tmp/<b>evil<b>.swift",
			index:     0,
			title:     "<img onerror=alert(1)>",
			startLine: 1,
			png:       []byte("x"),
		},
	}, nil, "", "test")

	if strings.Contains(out, "<b>evil<b>") {
		t.Fatal("XSS: unescaped HTML tag in file name")
	}
	if strings.Contains(out, "<img onerror=alert(1)>") {
		t.Fatal("XSS: unescaped img tag in title")
	}
	if !strings.Contains(out, "&lt;b&gt;evil&lt;b&gt;") {
		t.Fatal("expected escaped HTML tag in file name")
	}
	if !strings.Contains(out, "&lt;img onerror=alert(1)&gt;") {
		t.Fatal("expected escaped img tag in title")
	}
}

func TestRenderHTMLReport_LightboxScript(t *testing.T) {
	out := renderHTMLReport([]reportCapture{
		{file: "/tmp/Foo.swift", index: 0, title: "T", startLine: 1, png: []byte("x")},
	}, nil, "", "test")

	if !strings.Contains(out, `id="lightbox"`) {
		t.Fatal("missing lightbox dialog element")
	}
	if !strings.Contains(out, "data-lightbox") {
		t.Fatal("missing data-lightbox attribute on img")
	}
	if !strings.Contains(out, "dialog.showModal") {
		t.Fatal("missing showModal call in lightbox script")
	}
	if strings.Contains(out, "onclick=") {
		t.Fatal("should not use inline onclick handler (CSP)")
	}
}

func TestRenderMarkdownReport_WithFailures(t *testing.T) {
	captures := []reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}
	failures := []captureFailure{
		{file: "/tmp/HogeView.swift", index: 1, title: "Dark", startLine: 20, err: fmt.Errorf("simulator timeout")},
	}
	md := renderMarkdownReport(captures, failures, "", "abc1234")
	if !strings.Contains(md, "## Failures") {
		t.Fatal("missing failures section")
	}
	if !strings.Contains(md, "simulator timeout") {
		t.Fatal("missing error message in failures")
	}
}

func TestRenderMarkdownReport_NoFailures(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}, nil, "", "test")
	if strings.Contains(md, "Failures") {
		t.Fatal("should not show failures section when there are none")
	}
}

func TestRenderHTMLReport_WithFailures(t *testing.T) {
	captures := []reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}
	failures := []captureFailure{
		{file: "/tmp/HogeView.swift", index: 1, title: "Dark", startLine: 20, err: fmt.Errorf("simulator timeout")},
	}
	html := renderHTMLReport(captures, failures, "", "abc1234")
	if !strings.Contains(html, "Failures") {
		t.Fatal("missing failures section")
	}
	if !strings.Contains(html, "simulator timeout") {
		t.Fatal("missing error message in failures")
	}
}

func TestRenderHTMLReport_NoFailures(t *testing.T) {
	html := renderHTMLReport([]reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}, nil, "", "test")
	if strings.Contains(html, "Failures") {
		t.Fatal("should not show failures section when there are none")
	}
}

func TestRenderHTMLReport_Version(t *testing.T) {
	html := renderHTMLReport([]reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}, nil, "", "abc1234")
	if !strings.Contains(html, "abc1234") {
		t.Fatal("expected version string in HTML report")
	}
}

func TestRenderMarkdownReport_Version(t *testing.T) {
	md := renderMarkdownReport([]reportCapture{
		{file: "/tmp/HogeView.swift", index: 0, title: "OK", startLine: 10, png: []byte("x")},
	}, nil, "", "abc1234")
	if !strings.Contains(md, "abc1234") {
		t.Fatal("expected version string in markdown report")
	}
}

func TestCaptureFailure_DisplayTitle(t *testing.T) {
	f := captureFailure{title: "Dark Mode"}
	if f.displayTitle() != "Dark Mode" {
		t.Fatalf("expected 'Dark Mode', got %q", f.displayTitle())
	}
	f2 := captureFailure{title: ""}
	if f2.displayTitle() != "(Untitled)" {
		t.Fatalf("expected '(Untitled)', got %q", f2.displayTitle())
	}
}

func TestReportAssetName(t *testing.T) {
	name := reportAssetName("/path/to/FooView.swift", 0)
	if !strings.HasPrefix(name, "FooView--") {
		t.Fatalf("expected FooView-- prefix, got %s", name)
	}
	if !strings.HasSuffix(name, "--preview-0.png") {
		t.Fatalf("expected --preview-0.png suffix, got %s", name)
	}
	// Different full paths should produce different names (SHA256 hash differs).
	name2 := reportAssetName("/other/path/FooView.swift", 0)
	if name == name2 {
		t.Fatal("expected different asset names for different full paths")
	}
}

func TestWriteReportAssets_CollisionFallback(t *testing.T) {
	tmp := t.TempDir()
	assetsDir := filepath.Join(tmp, reportAssetsDirName)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two captures from the same file and same index produce identical asset names,
	// triggering the collision fallback.
	captures := []reportCapture{
		{file: "/tmp/FooView.swift", index: 0, png: []byte("data-a")},
		{file: "/tmp/FooView.swift", index: 0, png: []byte("data-b")},
	}

	out, err := writeReportAssets(assetsDir, tmp, captures)
	if err != nil {
		t.Fatalf("writeReportAssets error: %v", err)
	}
	if out[0].imageRef == out[1].imageRef {
		t.Fatal("collision fallback should produce different imageRefs")
	}
	// Both files should exist on disk.
	for _, c := range out {
		path := filepath.Join(tmp, filepath.FromSlash(c.imageRef))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected asset file %s: %v", path, err)
		}
	}
}

func TestDisplayTitle(t *testing.T) {
	c := reportCapture{title: "Dark Mode"}
	if c.displayTitle() != "Dark Mode" {
		t.Fatalf("expected 'Dark Mode', got %q", c.displayTitle())
	}
	c2 := reportCapture{title: ""}
	if c2.displayTitle() != "(Untitled)" {
		t.Fatalf("expected '(Untitled)', got %q", c2.displayTitle())
	}
}

func TestSourceBaseName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/path/to/FooView.swift", "FooView"},
		{"/path/to/BarView.swift", "BarView"},
		{"NoExtension", "NoExtension"},
	}
	for _, tt := range tests {
		got := sourceBaseName(tt.in)
		if got != tt.want {
			t.Errorf("sourceBaseName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- Golden file tests ---
// Run with -update to regenerate: go test ./internal/preview/ -run Golden -update

func goldenSingleCaptures() []reportCapture {
	return []reportCapture{
		{
			file:      "/project/Sources/HogeView.swift",
			index:     0,
			title:     "Dark Mode",
			startLine: 12,
			png:       []byte("fake-png"),
			imageRef:  reportAssetsDirName + "/HogeView--aabbccdd--preview-0.png",
		},
	}
}

func goldenMultipleCaptures() []reportCapture {
	return []reportCapture{
		{
			file:      "/project/Sources/HogeView.swift",
			index:     0,
			title:     "",
			startLine: 8,
			png:       []byte("hoge-0"),
			imageRef:  reportAssetsDirName + "/HogeView--aabbccdd--preview-0.png",
		},
		{
			file:      "/project/Sources/HogeView.swift",
			index:     1,
			title:     "Dark Mode",
			startLine: 20,
			png:       []byte("hoge-1"),
			imageRef:  reportAssetsDirName + "/HogeView--aabbccdd--preview-1.png",
		},
		{
			file:      "/project/Sources/FugaView.swift",
			index:     0,
			title:     "Fuga",
			startLine: 11,
			png:       []byte("fuga-0"),
			imageRef:  reportAssetsDirName + "/FugaView--11223344--preview-0.png",
		},
	}
}

func goldenFailures() []captureFailure {
	return []captureFailure{
		{file: "/project/Sources/HogeView.swift", index: 2, title: "Broken", startLine: 30, err: fmt.Errorf("simulator timeout")},
	}
}

func TestGolden_MD_Single(t *testing.T) {
	got := renderMarkdownReport(goldenSingleCaptures(), nil, "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_md_single.md"), got)
}

func TestGolden_MD_Multiple(t *testing.T) {
	got := renderMarkdownReport(goldenMultipleCaptures(), nil, "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_md_multiple.md"), got)
}

func TestGolden_MD_WithFailures(t *testing.T) {
	got := renderMarkdownReport(goldenMultipleCaptures(), goldenFailures(), "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_md_with_failures.md"), got)
}

func TestGolden_HTML_Single(t *testing.T) {
	got := renderHTMLReport(goldenSingleCaptures(), nil, "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_html_single.html"), got)
}

func TestGolden_HTML_Multiple(t *testing.T) {
	got := renderHTMLReport(goldenMultipleCaptures(), nil, "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_html_multiple.html"), got)
}

func TestGolden_HTML_WithFailures(t *testing.T) {
	got := renderHTMLReport(goldenMultipleCaptures(), goldenFailures(), "/project", goldenVersion)
	assertGolden(t, filepath.Join("testdata", "golden_html_with_failures.html"), got)
}
