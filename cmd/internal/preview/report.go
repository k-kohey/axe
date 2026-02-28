package preview

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview/analysis"
)

// ReportOptions holds parameters for the preview report command.
type ReportOptions struct {
	Files       []string
	Output      string        // directory or file path
	RenderDelay time.Duration // wait time before screenshot
	Format      string        // png, md, or html
	PC          ProjectConfig
	Device      string
}

const (
	reportFormatPNG  = "png"
	reportFormatMD   = "md"
	reportFormatHTML = "html"

	markdownReportFileName = "axe_swiftui_preview_report.md"
	htmlReportFileName     = "axe_swiftui_preview_report.html"
	reportAssetsDirName    = "axe_swiftui_preview_report_assets"
)

type reportCapture struct {
	file      string
	index     int
	title     string
	startLine int
	png       []byte
	imageRef  string
}

func (c reportCapture) displayTitle() string {
	if c.title == "" {
		return "(Untitled)"
	}
	return c.title
}

// RunReport captures previews and writes them in the requested output format.
func RunReport(opts ReportOptions) error {
	format, err := normalizeReportFormat(opts.Format)
	if err != nil {
		return err
	}
	slog.Info("preview report start", "format", format, "fileCount", len(opts.Files), "output", opts.Output)

	// Validate all files upfront before doing any work.
	blocks, err := validateReportFiles(opts.Files)
	if err != nil {
		return err
	}

	switch format {
	case reportFormatPNG:
		return runReportPNG(opts, blocks)
	case reportFormatMD:
		return runReportDocument(opts, blocks, markdownReportFileName, renderMarkdownReport)
	case reportFormatHTML:
		return runReportDocument(opts, blocks, htmlReportFileName, renderHTMLReport)
	default:
		// defensive: normalizeReportFormat should have caught this
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func runReportPNG(opts ReportOptions, blocks []fileBlocks) error {
	outputIsDir, err := resolveOutputMode(opts.Output, blocks)
	if err != nil {
		return err
	}
	if outputIsDir {
		if err := os.MkdirAll(opts.Output, 0o755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
	} else {
		parentDir := filepath.Dir(opts.Output)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("creating output parent directory: %w", err)
		}
	}

	if outputIsDir {
		if err := checkOutputCollisions(opts.Output, blocks); err != nil {
			return err
		}
	}

	return captureLoop(opts, blocks, func(file string, index int, _ analysis.PreviewBlock, png []byte) error {
		outputPath := computeOutputPath(opts.Output, file, index, outputIsDir)
		return os.WriteFile(outputPath, png, 0o644)
	})
}

func runReportDocument(
	opts ReportOptions,
	blocks []fileBlocks,
	reportFileName string,
	render func([]reportCapture, string) string,
) error {
	reportPath, assetsDir, err := prepareReportOutputPaths(opts.Output, reportFileName)
	if err != nil {
		return err
	}

	slog.Info("preview report capture begin", "format", reportFileName, "fileCount", len(blocks))

	var captures []reportCapture
	err = captureLoop(opts, blocks, func(file string, index int, pb analysis.PreviewBlock, png []byte) error {
		captures = append(captures, reportCapture{
			file:      file,
			index:     index,
			title:     pb.Title,
			startLine: pb.StartLine,
			png:       append([]byte(nil), png...),
		})
		return nil
	})
	if err != nil {
		return err
	}
	slog.Info("preview report capture done", "captureCount", len(captures))

	capturesWithRefs, err := writeReportAssets(assetsDir, filepath.Dir(reportPath), captures)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("failed to get working directory, source paths will use base names", "err", err)
	}
	content := render(capturesWithRefs, cwd)
	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		return err
	}
	slog.Info("preview report written", "destination", reportPath, "bytes", len(content))

	if opener, err := exec.LookPath("open"); err == nil {
		if err := exec.Command(opener, reportPath).Start(); err != nil {
			slog.Warn("failed to open report", "err", err)
		}
	}

	return nil
}

// captureLoop iterates all preview blocks, captures screenshots via the simulator,
// and calls onCapture with the resulting PNG data for each preview.
func captureLoop(opts ReportOptions, blocks []fileBlocks, onCapture func(file string, index int, pb analysis.PreviewBlock, png []byte) error) error {
	firstRun := true
	for _, fb := range blocks {
		for i, pb := range fb.previews {
			runOpts := RunOptions{
				SourceFile:      fb.file,
				PC:              opts.PC,
				PreviewSelector: strconv.Itoa(i),
				PreferredDevice: opts.Device,
				ReuseBuild:      !firstRun,
			}

			var png []byte
			runOpts.OnReady = func(ctx context.Context, device, deviceSetPath string) error {
				if opts.RenderDelay > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(opts.RenderDelay):
					}
				}
				data, err := platform.Screenshot(ctx, device, deviceSetPath)
				if err != nil {
					return err
				}
				png = data
				return nil
			}

			fmt.Fprintf(os.Stderr, "Capturing %s (preview %d)\n", filepath.Base(fb.file), i)
			slog.Info("preview report capture", "file", fb.file, "previewIndex", i)
			if err := Run(runOpts); err != nil {
				return fmt.Errorf("capturing %s preview %d: %w", filepath.Base(fb.file), i, err)
			}
			if len(png) == 0 {
				return fmt.Errorf("capturing %s preview %d: screenshot data was empty", filepath.Base(fb.file), i)
			}
			if err := onCapture(fb.file, i, pb, png); err != nil {
				return err
			}
			firstRun = false
		}
	}
	return nil
}

// fileBlocks pairs a source file with its parsed #Preview blocks.
type fileBlocks struct {
	file     string
	previews []analysis.PreviewBlock
}

// validateReportFiles checks that all files are .swift, exist, and contain #Preview blocks.
func validateReportFiles(files []string) ([]fileBlocks, error) {
	var result []fileBlocks
	for _, f := range files {
		if !strings.HasSuffix(f, ".swift") {
			return nil, fmt.Errorf("not a Swift file: %s", f)
		}

		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, fmt.Errorf("resolving path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("accessing file %s: %w", abs, err)
		}

		blocks, err := analysis.PreviewBlocks(abs)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filepath.Base(abs), err)
		}
		if len(blocks) == 0 {
			return nil, fmt.Errorf("no #Preview blocks found in %s", filepath.Base(abs))
		}

		result = append(result, fileBlocks{
			file:     abs,
			previews: blocks,
		})
	}
	return result, nil
}

// resolveOutputMode determines whether output is a directory or a single file.
// Returns true if output is a directory, false if a single file.
//
// Priority: if the path already exists as a directory, treat as directory.
// Otherwise, use file extension as heuristic (has extension → file, no extension → directory).
func resolveOutputMode(output string, blocks []fileBlocks) (bool, error) {
	totalPreviews := 0
	for _, fb := range blocks {
		totalPreviews += len(fb.previews)
	}

	// If path already exists as a directory, treat as directory regardless of extension.
	if info, err := os.Stat(output); err == nil && info.IsDir() {
		return true, nil
	}

	// If output has a file extension, treat as file path.
	if ext := filepath.Ext(output); ext != "" {
		if totalPreviews > 1 {
			return false, fmt.Errorf("output is a file path but %d previews found; use a directory for multiple previews", totalPreviews)
		}
		return false, nil
	}

	// No extension and not an existing directory → treat as new directory.
	return true, nil
}

// checkOutputCollisions detects when multiple source files would produce the same
// output file name (e.g. Sources/FooView.swift and Tests/FooView.swift).
func checkOutputCollisions(output string, blocks []fileBlocks) error {
	seen := make(map[string]string) // output path → source file
	for _, fb := range blocks {
		for i := range len(fb.previews) {
			p := computeOutputPath(output, fb.file, i, true)
			if prev, ok := seen[p]; ok {
				return fmt.Errorf("output collision: %s and %s both map to %s",
					filepath.Base(prev), filepath.Base(fb.file), filepath.Base(p))
			}
			seen[p] = fb.file
		}
	}
	return nil
}

// sourceBaseName returns the file name without directory and extension.
func sourceBaseName(sourceFile string) string {
	return strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
}

// computeOutputPath returns the file path for a screenshot.
func computeOutputPath(output, sourceFile string, index int, isDir bool) string {
	if !isDir {
		return output
	}
	return filepath.Join(output, fmt.Sprintf("%s--preview-%d.png", sourceBaseName(sourceFile), index))
}

func normalizeReportFormat(format string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" {
		f = reportFormatPNG
	}
	switch f {
	case reportFormatPNG, reportFormatMD, reportFormatHTML:
		return f, nil
	default:
		return "", fmt.Errorf("unsupported --format %q (supported: png, md, html)", format)
	}
}

// captureGroup groups captures belonging to the same source file.
type captureGroup struct {
	file     string
	captures []reportCapture
}

// groupCapturesByFile groups captures by source file, preserving encounter order.
func groupCapturesByFile(captures []reportCapture) []captureGroup {
	var groups []captureGroup
	index := make(map[string]int)
	for _, c := range captures {
		i, ok := index[c.file]
		if !ok {
			i = len(groups)
			index[c.file] = i
			groups = append(groups, captureGroup{file: c.file})
		}
		groups[i].captures = append(groups[i].captures, c)
	}
	return groups
}

// resolveSourceDisplay returns a display-friendly source path.
// If the file is under cwd, a relative path is returned; otherwise filepath.Base.
func resolveSourceDisplay(file, cwd string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return filepath.Base(file)
}

// resolveImageSrc returns the image src string for a capture.
// Prefers imageRef (file path) over inline base64 data URI.
func resolveImageSrc(c reportCapture) string {
	if c.imageRef != "" {
		return c.imageRef
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(c.png)
}

func prepareReportOutputPaths(output, reportFileName string) (string, string, error) {
	if strings.TrimSpace(output) == "" {
		return "", "", fmt.Errorf("--output is required for document report formats")
	}
	// Check existing path first: an existing directory (even with dots in name) is valid.
	if info, err := os.Stat(output); err == nil {
		if !info.IsDir() {
			return "", "", fmt.Errorf("--output must be a directory path: %s", output)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("checking output path %s: %w", output, err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return "", "", fmt.Errorf("creating report output directory: %w", err)
	}
	assetsDir := filepath.Join(output, reportAssetsDirName)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating report assets directory: %w", err)
	}
	reportPath := filepath.Join(output, reportFileName)
	return reportPath, assetsDir, nil
}

func writeReportAssets(assetsDir, reportDir string, captures []reportCapture) ([]reportCapture, error) {
	out := make([]reportCapture, len(captures))
	seen := make(map[string]struct{})
	for i, c := range captures {
		assetName := reportAssetName(c.file, c.index)
		if _, exists := seen[assetName]; exists {
			assetName = fmt.Sprintf("%s-%d.png", strings.TrimSuffix(assetName, ".png"), i)
		}
		seen[assetName] = struct{}{}

		assetPath := filepath.Join(assetsDir, assetName)
		if err := os.WriteFile(assetPath, c.png, 0o644); err != nil {
			return nil, fmt.Errorf("writing report asset %s: %w", assetName, err)
		}

		relRef, err := filepath.Rel(reportDir, assetPath)
		if err != nil {
			return nil, fmt.Errorf("computing report asset path: %w", err)
		}

		out[i] = c
		out[i].imageRef = filepath.ToSlash(relRef)
	}

	slog.Info("preview report assets written", "directory", assetsDir, "count", len(captures))
	return out, nil
}

func reportAssetName(sourceFile string, index int) string {
	sum := sha256.Sum256([]byte(sourceFile))
	return fmt.Sprintf("%s--%x--preview-%d.png", sourceBaseName(sourceFile), sum[:4], index)
}

func renderMarkdownReport(captures []reportCapture, cwd string) string {
	const columns = 2

	var b strings.Builder
	b.WriteString("# SwiftUI Preview Report\n\n")

	groups := groupCapturesByFile(captures)

	fmt.Fprintf(&b, "_Generated by `axe preview report --format md`_\n\n")
	fmt.Fprintf(&b, "_Generated at: %s_\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Files: **%d**\n", len(groups))
	fmt.Fprintf(&b, "- Previews: **%d**\n\n", len(captures))
	b.WriteString("## Overview\n\n")
	b.WriteString("| File | Preview Count |\n")
	b.WriteString("| --- | ---: |\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "| `%s` | %d |\n", filepath.Base(g.file), len(g.captures))
	}
	b.WriteString("\n")

	for _, g := range groups {
		base := filepath.Base(g.file)
		fmt.Fprintf(&b, "## %s (%d previews)\n\n", base, len(g.captures))
		b.WriteString("<table>\n")
		for i, c := range g.captures {
			if i%columns == 0 {
				b.WriteString("<tr>\n")
			}

			title := c.displayTitle()
			alt := html.EscapeString(fmt.Sprintf("%s preview %d", base, c.index))
			imgSrc := resolveImageSrc(c)
			source := resolveSourceDisplay(c.file, cwd)

			fmt.Fprintf(&b, "<td valign=\"top\" width=\"50%%\" align=\"center\">\n")
			fmt.Fprintf(&b, "<img src=\"%s\" alt=\"%s\" width=\"100%%\" />\n", html.EscapeString(imgSrc), alt)
			fmt.Fprintf(&b, "<br/><strong>Preview %d</strong><br/>\n", c.index)
			fmt.Fprintf(&b, "<sub>Title: <code>%s</code></sub><br/>\n", html.EscapeString(title))
			fmt.Fprintf(&b, "<sub>Source: <code>%s:%d</code></sub>\n", html.EscapeString(source), c.startLine)
			b.WriteString("</td>\n")

			isRowEnd := i%columns == columns-1 || i == len(g.captures)-1
			if isRowEnd {
				if i%columns != columns-1 {
					b.WriteString("<td></td>\n")
				}
				b.WriteString("</tr>\n")
			}
		}
		b.WriteString("</table>\n\n")
		b.WriteString("---\n\n")
	}

	return b.String()
}

func renderHTMLReport(captures []reportCapture, cwd string) string {
	groups := groupCapturesByFile(captures)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>SwiftUI Preview Report</title>
<style>
:root {
  --bg: #fafaf9;
  --card-bg: #ffffff;
  --text: #1a1a1a;
  --text-secondary: #737373;
  --border: #e5e5e5;
  --shadow: rgba(0,0,0,0.06);
  --accent: #e25822;
  --accent-hover: #c94a1a;
  --header-from: #1a1a1a;
  --header-to: #2d2d2d;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #171717;
    --card-bg: #262626;
    --text: #fafafa;
    --text-secondary: #a3a3a3;
    --border: #404040;
    --shadow: rgba(0,0,0,0.4);
    --accent: #f97316;
    --accent-hover: #fb923c;
    --header-from: #0a0a0a;
    --header-to: #171717;
  }
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.6;
}
header {
  background: linear-gradient(135deg, var(--header-from), var(--header-to));
  color: #fafafa;
  text-align: center;
  padding: 3rem 2rem 2.5rem;
}
header h1 {
  font-size: 1.75rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  margin-bottom: 0.5rem;
}
header .summary { color: #a3a3a3; font-size: 0.85rem; }
nav {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--bg);
  border-bottom: 1px solid var(--border);
  padding: 0.75rem 2rem;
}
nav ul { list-style: none; display: flex; flex-wrap: wrap; gap: 0.5rem; justify-content: center; }
nav a {
  color: var(--text);
  text-decoration: none;
  padding: 0.3rem 0.85rem;
  border: 1px solid var(--border);
  border-radius: 2rem;
  font-size: 0.8rem;
  transition: all 0.2s;
}
nav a:hover { border-color: var(--accent); color: var(--accent); }
main { padding: 2rem; max-width: 1400px; margin: 0 auto; }
section { margin-bottom: 3rem; }
section h2 {
  font-size: 1.2rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  margin-bottom: 1rem;
  padding-bottom: 0.5rem;
  border-bottom: 2px solid var(--accent);
  display: inline-block;
}
section h2 .summary { font-weight: 400; }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1.5rem;
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: translateY(0); }
}
.card {
  background: var(--card-bg);
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid var(--border);
  transition: transform 0.2s, box-shadow 0.2s;
  animation: card-in 0.4s ease both;
}
.card:hover {
  transform: translateY(-3px);
  box-shadow: 0 8px 24px var(--shadow);
}
.card img {
  width: 100%;
  display: block;
  cursor: pointer;
  background: var(--bg);
}
.card-body { padding: 0.75rem 1rem; }
.card-title { font-weight: 600; font-size: 0.9rem; }
.card-meta {
  color: var(--text-secondary);
  font-size: 0.75rem;
  margin-top: 0.2rem;
  font-family: "SF Mono", ui-monospace, monospace;
}
dialog {
  border: none;
  background: transparent;
  max-width: 90vw;
  max-height: 90vh;
  padding: 0;
  opacity: 0;
  transform: scale(0.95);
  transition: opacity 0.2s, transform 0.2s;
}
dialog[open] { opacity: 1; transform: scale(1); }
dialog::backdrop { background: rgba(0,0,0,0.75); }
dialog img { max-width: 90vw; max-height: 85vh; border-radius: 8px; display: block; }
dialog .hint {
  text-align: center;
  color: #a3a3a3;
  font-size: 0.75rem;
  margin-top: 0.75rem;
}
footer {
  text-align: center;
  color: var(--text-secondary);
  font-size: 0.75rem;
  padding: 1.5rem 2rem;
  border-top: 1px solid var(--border);
}
</style>
</head>
<body>
`)

	fmt.Fprintf(&b, "<header>\n<h1>SwiftUI Preview Report</h1>\n")
	fmt.Fprintf(&b, "<p class=\"summary\">%d files &middot; %d previews &middot; Generated at %s</p>\n",
		len(groups), len(captures), html.EscapeString(time.Now().Format(time.RFC3339)))
	b.WriteString("</header>\n")

	// TOC
	b.WriteString("<nav><ul>\n")
	for i, g := range groups {
		anchorID := fmt.Sprintf("file-%d", i)
		fmt.Fprintf(&b, "<li><a href=\"#%s\">%s (%d)</a></li>\n",
			anchorID, html.EscapeString(filepath.Base(g.file)), len(g.captures))
	}
	b.WriteString("</ul></nav>\n")

	// Sections
	b.WriteString("<main>\n")
	for i, g := range groups {
		base := filepath.Base(g.file)
		anchorID := fmt.Sprintf("file-%d", i)
		fmt.Fprintf(&b, "<section id=\"%s\">\n<h2>%s <span class=\"summary\">(%d previews)</span></h2>\n<div class=\"grid\">\n",
			anchorID, html.EscapeString(base), len(g.captures))
		for j, c := range g.captures {
			title := c.displayTitle()
			alt := html.EscapeString(fmt.Sprintf("%s preview %d", base, c.index))
			imgSrc := resolveImageSrc(c)
			source := resolveSourceDisplay(c.file, cwd)
			delay := float64(j) * 0.08

			fmt.Fprintf(&b, "<div class=\"card\" style=\"animation-delay:%.2fs\">\n", delay)
			fmt.Fprintf(&b, "<img src=\"%s\" alt=\"%s\" data-lightbox />\n",
				html.EscapeString(imgSrc), alt)
			b.WriteString("<div class=\"card-body\">\n")
			fmt.Fprintf(&b, "<div class=\"card-title\">Preview %d &mdash; %s</div>\n",
				c.index, html.EscapeString(title))
			fmt.Fprintf(&b, "<div class=\"card-meta\">%s:%d</div>\n",
				html.EscapeString(source), c.startLine)
			b.WriteString("</div>\n</div>\n")
		}
		b.WriteString("</div>\n</section>\n")
	}
	b.WriteString("</main>\n")

	// Lightbox dialog + footer (event delegation for CSP compatibility — no inline handlers)
	b.WriteString(`<dialog id="lightbox"><img src="" alt="preview" /><p class="hint">Click outside to close</p></dialog>
<script>
(function() {
  var dialog = document.getElementById('lightbox');
  var dialogImg = dialog ? dialog.querySelector('img') : null;
  if (!dialog || !dialogImg || typeof dialog.showModal !== 'function') return;
  document.addEventListener('click', function(e) {
    if (!(e.target instanceof HTMLImageElement)) return;
    if (!e.target.hasAttribute('data-lightbox')) return;
    dialogImg.src = e.target.src;
    dialogImg.alt = e.target.alt;
    dialog.showModal();
  });
  dialog.addEventListener('click', function(e) {
    var rect = dialogImg.getBoundingClientRect();
    var inside = e.clientX >= rect.left && e.clientX <= rect.right &&
                 e.clientY >= rect.top && e.clientY <= rect.bottom;
    if (!inside) dialog.close();
  });
})();
</script>
<footer>Generated by <code>axe preview report --format html</code></footer>
</body>
</html>
`)
	return b.String()
}
