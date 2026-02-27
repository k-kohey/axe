package preview

import (
	"context"
	"fmt"
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
	PC          ProjectConfig
	Device      string
}

// RunReport captures screenshots of all #Preview blocks in the given files.
func RunReport(opts ReportOptions) error {
	// Validate all files upfront before doing any work.
	blocks, err := validateReportFiles(opts.Files)
	if err != nil {
		return err
	}

	// Determine output mode and validate.
	outputIsDir, err := resolveOutputMode(opts.Output, blocks)
	if err != nil {
		return err
	}

	// Ensure output directory exists.
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

	// Check for output path collisions when multiple files are involved.
	if outputIsDir {
		if err := checkOutputCollisions(opts.Output, blocks); err != nil {
			return err
		}
	}

	// Capture each preview.
	firstRun := true
	for _, fb := range blocks {
		for i := range fb.count {
			outputPath := computeOutputPath(opts.Output, fb.file, i, outputIsDir)

			runOpts := RunOptions{
				SourceFile:      fb.file,
				PC:              opts.PC,
				PreviewSelector: strconv.Itoa(i),
				PreferredDevice: opts.Device,
				ReuseBuild:      !firstRun,
			}
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
				return os.WriteFile(outputPath, data, 0o644)
			}

			fmt.Fprintf(os.Stderr, "Capturing %s (preview %d) → %s\n", filepath.Base(fb.file), i, outputPath)
			if err := Run(runOpts); err != nil {
				return fmt.Errorf("capturing %s preview %d: %w", filepath.Base(fb.file), i, err)
			}
			firstRun = false
		}
	}

	return nil
}

// fileBlocks pairs a source file with its #Preview block count.
type fileBlocks struct {
	file  string
	count int
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

		result = append(result, fileBlocks{file: abs, count: len(blocks)})
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
		totalPreviews += fb.count
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
		for i := range fb.count {
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
