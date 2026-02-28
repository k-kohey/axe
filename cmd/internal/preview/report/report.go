package report

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	htmlpkg "html"
	"html/template"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview"
	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
)

// ReportOptions holds parameters for the preview report command.
type ReportOptions struct {
	Files       []string
	Output      string        // directory or file path
	RenderDelay time.Duration // wait time before screenshot
	Format      string        // png, md, or html
	PC          build.ProjectConfig
	Device      string
	Concurrency int  // 0 = auto, 1 = sequential (existing path)
	ReuseBuild  bool // skip xcodebuild and reuse artifacts from a previous build
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

// displayTitle returns the title or "(Untitled)" fallback.
func displayTitle(title string) string {
	if title == "" {
		return "(Untitled)"
	}
	return title
}

// captureFailure records a preview that failed to capture after retries.
type captureFailure struct {
	file      string
	index     int
	title     string
	startLine int
	err       error
}

// captureResult holds the outcome of a captureLoopPartial run.
type captureResult struct {
	captures []reportCapture
	failures []captureFailure
}

// escapeMDTableCell escapes characters that would break a Markdown table cell.
func escapeMDTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// pluralizeWord returns singular when count is 1, plural otherwise.
func pluralizeWord(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
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

	dirs, err := build.NewProjectDirs(opts.PC.PrimaryPath())
	if err != nil {
		return fmt.Errorf("resolving build directories: %w", err)
	}
	br := build.NewRunner()
	preparer := build.NewPreparer(opts.PC, dirs, opts.ReuseBuild, br)

	useParallel := len(blocks) > 1 && opts.Concurrency != 1
	if useParallel && opts.Device != "" {
		slog.Warn("--device is ignored in parallel mode")
		opts.Device = ""
	}

	switch format {
	case reportFormatPNG:
		if useParallel {
			return runReportPNGParallel(opts, blocks, preparer)
		}
		return runReportPNG(opts, blocks, preparer)
	case reportFormatMD:
		if useParallel {
			return runReportDocumentParallel(opts, blocks, markdownReportFileName, renderMarkdownReport, preparer)
		}
		return runReportDocument(opts, blocks, markdownReportFileName, renderMarkdownReport, preparer)
	case reportFormatHTML:
		if useParallel {
			return runReportDocumentParallel(opts, blocks, htmlReportFileName, renderHTMLReport, preparer)
		}
		return runReportDocument(opts, blocks, htmlReportFileName, renderHTMLReport, preparer)
	default:
		// defensive: normalizeReportFormat should have caught this
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func runReportPNG(opts ReportOptions, blocks []fileBlocks, preparer *build.Preparer) error {
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

	return captureLoop(opts, blocks, preparer, func(file string, index int, _ analysis.PreviewBlock, png []byte) error {
		outputPath := computeOutputPath(opts.Output, file, index, outputIsDir)
		return os.WriteFile(outputPath, png, 0o644)
	})
}

func runReportDocument(
	opts ReportOptions,
	blocks []fileBlocks,
	reportFileName string,
	render func([]reportCapture, []captureFailure, string, string) (string, error),
	preparer *build.Preparer,
) error {
	reportPath, assetsDir, err := prepareReportOutputPaths(opts.Output, reportFileName)
	if err != nil {
		return err
	}

	slog.Info("preview report capture begin", "format", reportFileName, "fileCount", len(blocks))

	result := captureLoopPartial(opts, blocks, preparer)

	slog.Info("preview report capture done",
		"captureCount", len(result.captures), "failureCount", len(result.failures))

	var capturesWithRefs []reportCapture
	if len(result.captures) > 0 {
		capturesWithRefs, err = writeReportAssets(assetsDir, filepath.Dir(reportPath), result.captures)
		if err != nil {
			return err
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Warn("failed to get working directory, source paths will use base names", "err", err)
	}
	version := resolveVersion()
	content, err := render(capturesWithRefs, result.failures, cwd, version)
	if err != nil {
		return fmt.Errorf("rendering report: %w", err)
	}
	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		return err
	}
	slog.Info("preview report written", "destination", reportPath, "bytes", len(content))

	if opener, err := exec.LookPath("open"); err == nil {
		if err := exec.Command(opener, reportPath).Start(); err != nil {
			slog.Warn("failed to open report", "err", err)
		}
	}

	if len(result.failures) > 0 {
		return fmt.Errorf("%d of %d preview captures failed (report generated with partial results)",
			len(result.failures), len(result.captures)+len(result.failures))
	}
	return nil
}

// captureOnce executes a single preview capture and returns the PNG data.
// deviceUDID and deviceSetPath are optional; when non-empty, Run() skips
// ResolveAxeSimulator and uses the pre-acquired device directly.
func captureOnce(opts ReportOptions, file string, previewIndex int,
	preparer *build.Preparer, deviceUDID, deviceSetPath string) ([]byte, error) {
	runOpts := preview.RunOptions{
		SourceFile:      file,
		PC:              opts.PC,
		PreviewSelector: strconv.Itoa(previewIndex),
		PreferredDevice: opts.Device,
		Preparer:        preparer,
		DeviceUDID:      deviceUDID,
		DeviceSetPath:   deviceSetPath,
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
	if err := preview.Run(runOpts); err != nil {
		return nil, err
	}
	if len(png) == 0 {
		return nil, fmt.Errorf("screenshot data was empty")
	}
	return png, nil
}

// captureLoop iterates all preview blocks, captures screenshots via the simulator,
// and calls onCapture with the resulting PNG data for each preview.
func captureLoop(opts ReportOptions, blocks []fileBlocks, preparer *build.Preparer, onCapture func(file string, index int, pb analysis.PreviewBlock, png []byte) error) error {
	for _, fb := range blocks {
		for i, pb := range fb.previews {
			fmt.Fprintf(os.Stderr, "Capturing %s (preview %d)\n", filepath.Base(fb.file), i)
			slog.Info("preview report capture", "file", fb.file, "previewIndex", i)
			png, err := captureOnce(opts, fb.file, i, preparer, "", "")
			if err != nil {
				return fmt.Errorf("capturing %s preview %d: %w", filepath.Base(fb.file), i, err)
			}
			if err := onCapture(fb.file, i, pb, png); err != nil {
				return err
			}
		}
	}
	return nil
}

const captureMaxRetries = 3

// captureLoopPartial iterates all preview blocks, captures screenshots,
// retries on failure with backoff, and continues past individual errors.
// Used by runReportDocument (MD/HTML) to produce partial reports.
func captureLoopPartial(opts ReportOptions, blocks []fileBlocks, preparer *build.Preparer) captureResult {
	var result captureResult
	for _, fb := range blocks {
		for i, pb := range fb.previews {
			var png []byte
			var lastErr error
			for attempt := range captureMaxRetries {
				fmt.Fprintf(os.Stderr, "Capturing %s (preview %d)\n", filepath.Base(fb.file), i)
				slog.Info("preview report capture", "file", fb.file, "previewIndex", i, "attempt", attempt+1)
				png, lastErr = captureOnce(opts, fb.file, i, preparer, "", "")
				if lastErr == nil {
					break
				}
				slog.Warn("preview capture failed",
					"file", filepath.Base(fb.file), "previewIndex", i,
					"attempt", attempt+1, "maxRetries", captureMaxRetries, "err", lastErr)
				if attempt < captureMaxRetries-1 {
					time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
				}
			}

			if lastErr != nil {
				fmt.Fprintf(os.Stderr, "  Failed after %d attempts: %s preview %d: %v\n",
					captureMaxRetries, filepath.Base(fb.file), i, lastErr)
				result.failures = append(result.failures, captureFailure{
					file:      fb.file,
					index:     i,
					title:     pb.Title,
					startLine: pb.StartLine,
					err:       lastErr,
				})
			} else {
				result.captures = append(result.captures, reportCapture{
					file:      fb.file,
					index:     i,
					title:     pb.Title,
					startLine: pb.StartLine,
					png:       append([]byte(nil), png...),
				})
			}
		}
	}
	return result
}

// resolveVersion returns a version string for the report.
// Uses the git commit hash if available, otherwise falls back to a timestamp.
func resolveVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD").Output()
	if err == nil {
		hash := strings.TrimSpace(string(out))
		if hash != "" {
			return hash
		}
	}
	return time.Now().Format(time.RFC3339)
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
// Otherwise, use file extension as heuristic (has extension -> file, no extension -> directory).
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

	// No extension and not an existing directory -> treat as new directory.
	return true, nil
}

// checkOutputCollisions detects when multiple source files would produce the same
// output file name (e.g. Sources/FooView.swift and Tests/FooView.swift).
func checkOutputCollisions(output string, blocks []fileBlocks) error {
	seen := make(map[string]string) // output path -> source file
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

func renderMarkdownReport(captures []reportCapture, failures []captureFailure, cwd, version string) (string, error) {
	const columns = 2

	var b strings.Builder
	b.WriteString("# SwiftUI Preview Report\n\n")

	groups := groupCapturesByFile(captures)

	fmt.Fprintf(&b, "_Generated by `axe preview report --format md`_\n\n")
	fmt.Fprintf(&b, "_%s_\n\n", version)
	fmt.Fprintf(&b, "- Files: **%d**\n", len(groups))
	fmt.Fprintf(&b, "- Previews: **%d**\n\n", len(captures))

	if len(groups) > 0 {
		b.WriteString("## Overview\n\n")
		b.WriteString("| File | Preview Count |\n")
		b.WriteString("| --- | ---: |\n")
		for _, g := range groups {
			fmt.Fprintf(&b, "| `%s` | %d |\n", filepath.Base(g.file), len(g.captures))
		}
		b.WriteString("\n")
	}

	if len(failures) > 0 {
		fmt.Fprintf(&b, "## Failures (%d)\n\n", len(failures))
		b.WriteString("| File | Preview | Error |\n")
		b.WriteString("| --- | ---: | --- |\n")
		for _, f := range failures {
			fmt.Fprintf(&b, "| `%s` | %d — %s | %s |\n",
				filepath.Base(f.file), f.index, escapeMDTableCell(displayTitle(f.title)), escapeMDTableCell(f.err.Error()))
		}
		b.WriteString("\n")
	}

	for _, g := range groups {
		base := filepath.Base(g.file)
		fmt.Fprintf(&b, "## %s (%d %s)\n\n", base, len(g.captures), pluralizeWord(len(g.captures), "preview", "previews"))
		b.WriteString("<table>\n")
		for i, c := range g.captures {
			if i%columns == 0 {
				b.WriteString("<tr>\n")
			}

			title := displayTitle(c.title)
			alt := htmlpkg.EscapeString(fmt.Sprintf("%s preview %d", base, c.index))
			imgSrc := resolveImageSrc(c)
			source := resolveSourceDisplay(c.file, cwd)

			fmt.Fprintf(&b, "<td valign=\"top\" width=\"50%%\" align=\"center\">\n")
			fmt.Fprintf(&b, "<img src=\"%s\" alt=\"%s\" width=\"100%%\" />\n", htmlpkg.EscapeString(imgSrc), alt)
			fmt.Fprintf(&b, "<br/><strong>Preview %d</strong><br/>\n", c.index)
			fmt.Fprintf(&b, "<sub>Title: <code>%s</code></sub><br/>\n", htmlpkg.EscapeString(title))
			fmt.Fprintf(&b, "<sub>Source: <code>%s:%d</code></sub>\n", htmlpkg.EscapeString(source), c.startLine)
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

	return b.String(), nil
}

// htmlReportData holds the data passed to the HTML report template.
type htmlReportData struct {
	FileCount    int
	PreviewCount int
	Version      string
	Groups       []htmlGroupData
	Failures     []htmlFailureData
}

// htmlFailureData represents a failed preview capture in the HTML report.
type htmlFailureData struct {
	FileName string
	Index    int
	Title    string
	ErrorMsg string
}

// htmlGroupData represents one source file and its preview cards.
type htmlGroupData struct {
	AnchorID     string
	FileName     string
	PreviewCount int
	Cards        []htmlCardData
}

// htmlCardData represents a single preview card.
type htmlCardData struct {
	Index     int
	Title     string
	ImageSrc  template.URL // data: URIs and relative paths are both safe here
	Alt       string
	Source    string
	StartLine int
	Delay     string
}

//go:embed templates/report.html
var htmlReportTmplSource string

var htmlReportTmpl = template.Must(
	template.New("report").Funcs(template.FuncMap{
		"pluralize": pluralizeWord,
	}).Parse(htmlReportTmplSource),
)

func renderHTMLReport(captures []reportCapture, failures []captureFailure, cwd, version string) (string, error) {
	groups := groupCapturesByFile(captures)

	data := htmlReportData{
		FileCount:    len(groups),
		PreviewCount: len(captures),
		Version:      version,
	}
	for i, g := range groups {
		base := filepath.Base(g.file)
		gd := htmlGroupData{
			AnchorID:     fmt.Sprintf("file-%d", i),
			FileName:     base,
			PreviewCount: len(g.captures),
		}
		for j, c := range g.captures {
			gd.Cards = append(gd.Cards, htmlCardData{
				Index:     c.index,
				Title:     displayTitle(c.title),
				ImageSrc:  template.URL(resolveImageSrc(c)), //nolint:gosec // resolveImageSrc returns only our generated data URIs or asset paths
				Alt:       fmt.Sprintf("%s preview %d", base, c.index),
				Source:    resolveSourceDisplay(c.file, cwd),
				StartLine: c.startLine,
				Delay:     fmt.Sprintf("%.2fs", float64(j)*0.08),
			})
		}
		data.Groups = append(data.Groups, gd)
	}
	for _, f := range failures {
		data.Failures = append(data.Failures, htmlFailureData{
			FileName: filepath.Base(f.file),
			Index:    f.index,
			Title:    displayTitle(f.title),
			ErrorMsg: f.err.Error(),
		})
	}

	var b strings.Builder
	if err := htmlReportTmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("executing HTML report template: %w", err)
	}
	return b.String(), nil
}
