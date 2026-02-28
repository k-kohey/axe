package preview

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"os"
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
	Format      string        // png or md
	PC          ProjectConfig
	Device      string
}

const (
	reportFormatPNG = "png"
	reportFormatMD  = "md"

	markdownReportFileName = "axe_swiftui_preview_report.md"
	markdownAssetsDirName  = "axe_swiftui_preview_report_assets"
)

type reportCapture struct {
	file      string
	index     int
	title     string
	startLine int
	png       []byte
	imageRef  string
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
		return runReportMarkdown(opts, blocks)
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

func runReportMarkdown(opts ReportOptions, blocks []fileBlocks) error {
	mdPath, assetsDir, err := prepareMarkdownOutputPaths(opts.Output)
	if err != nil {
		return err
	}

	slog.Info("preview report markdown capture begin", "fileCount", len(blocks))

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
	slog.Info("preview report markdown capture done", "captureCount", len(captures))

	capturesWithRefs, err := writeMarkdownAssets(assetsDir, filepath.Dir(mdPath), captures)
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	md := renderMarkdownReport(capturesWithRefs, cwd)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return err
	}
	slog.Info("preview report markdown written", "destination", mdPath, "bytes", len(md))
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

// computeOutputPath returns the file path for a screenshot.
func computeOutputPath(output, sourceFile string, index int, isDir bool) string {
	if !isDir {
		return output
	}
	base := strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
	return filepath.Join(output, fmt.Sprintf("%s--preview-%d.png", base, index))
}

func normalizeReportFormat(format string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" {
		f = reportFormatPNG
	}
	switch f {
	case reportFormatPNG, reportFormatMD:
		return f, nil
	default:
		return "", fmt.Errorf("unsupported --format %q (supported: png, md)", format)
	}
}

func prepareMarkdownOutputPaths(output string) (string, string, error) {
	if strings.TrimSpace(output) == "" {
		return "", "", fmt.Errorf("--output is required for --format md")
	}
	// Check existing path first: an existing directory (even with dots in name) is valid.
	if info, err := os.Stat(output); err == nil && !info.IsDir() {
		return "", "", fmt.Errorf("for --format md, --output must be a directory path: %s", output)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return "", "", fmt.Errorf("creating markdown output directory: %w", err)
	}
	assetsDir := filepath.Join(output, markdownAssetsDirName)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return "", "", fmt.Errorf("creating markdown assets directory: %w", err)
	}
	mdPath := filepath.Join(output, markdownReportFileName)
	return mdPath, assetsDir, nil
}

func writeMarkdownAssets(assetsDir, markdownDir string, captures []reportCapture) ([]reportCapture, error) {
	out := make([]reportCapture, len(captures))
	seen := make(map[string]struct{})
	for i, c := range captures {
		assetName := markdownAssetName(c.file, c.index)
		if _, exists := seen[assetName]; exists {
			assetName = fmt.Sprintf("%s-%d.png", strings.TrimSuffix(assetName, ".png"), i)
		}
		seen[assetName] = struct{}{}

		assetPath := filepath.Join(assetsDir, assetName)
		if err := os.WriteFile(assetPath, c.png, 0o644); err != nil {
			return nil, fmt.Errorf("writing markdown asset %s: %w", assetName, err)
		}

		relRef, err := filepath.Rel(markdownDir, assetPath)
		if err != nil {
			return nil, fmt.Errorf("computing markdown asset path: %w", err)
		}

		out[i] = c
		out[i].imageRef = filepath.ToSlash(relRef)
	}

	slog.Info("preview report markdown assets written", "directory", assetsDir, "count", len(captures))
	return out, nil
}

func markdownAssetName(sourceFile string, index int) string {
	base := strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
	sum := sha256.Sum256([]byte(sourceFile))
	return fmt.Sprintf("%s--%x--preview-%d.png", base, sum[:4], index)
}

func renderMarkdownReport(captures []reportCapture, cwd string) string {
	const columns = 2

	var b strings.Builder
	b.WriteString("# SwiftUI Preview Report\n\n")

	groups := make([]struct {
		file     string
		captures []reportCapture
	}, 0)
	groupIndex := make(map[string]int)
	for _, c := range captures {
		i, ok := groupIndex[c.file]
		if !ok {
			i = len(groups)
			groupIndex[c.file] = i
			groups = append(groups, struct {
				file     string
				captures []reportCapture
			}{file: c.file})
		}
		groups[i].captures = append(groups[i].captures, c)
	}

	fileCount := len(groups)
	fmt.Fprintf(&b, "_Generated by `axe preview report --format md`_\n\n")
	fmt.Fprintf(&b, "_Generated at: %s_\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Files: **%d**\n", fileCount)
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

			title := c.title
			if title == "" {
				title = "(Untitled)"
			}
			alt := html.EscapeString(fmt.Sprintf("%s preview %d", base, c.index))
			imgSrc := c.imageRef
			if imgSrc == "" {
				encoded := base64.StdEncoding.EncodeToString(c.png)
				imgSrc = "data:image/png;base64," + encoded
			}

			source := filepath.Base(c.file)
			if cwd != "" {
				if rel, err := filepath.Rel(cwd, c.file); err == nil && !strings.HasPrefix(rel, "..") {
					source = rel
				}
			}

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
