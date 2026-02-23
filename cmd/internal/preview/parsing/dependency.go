package parsing

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
)

// ResolveDependencies finds source files that define types referenced by the target file.
// It returns a list of absolute paths to 1-level dependency files (excluding the target itself).
func ResolveDependencies(ctx context.Context, targetFile string, projectRoot string, sl SwiftFileLister) ([]string, error) {
	targetResult, err := swiftParse(targetFile)
	if err != nil {
		return nil, fmt.Errorf("parsing target file: %w", err)
	}

	if len(targetResult.ReferencedTypes) == 0 {
		slog.Debug("No referenced types in target file")
		return nil, nil
	}

	refSet := make(map[string]bool, len(targetResult.ReferencedTypes))
	for _, t := range targetResult.ReferencedTypes {
		refSet[t] = true
	}

	// Remove types defined in the target file itself.
	for _, t := range targetResult.DefinedTypes {
		delete(refSet, t)
	}

	if len(refSet) == 0 {
		slog.Debug("All referenced types are defined in the target file")
		return nil, nil
	}

	slog.Debug("Looking for dependency files", "referencedTypes", refSet)

	// Get all Swift files in the project.
	swiftFiles, err := sl.SwiftFiles(ctx, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("listing swift files: %w", err)
	}

	cleanTarget := filepath.Clean(targetFile)
	var deps []string

	for _, f := range swiftFiles {
		if filepath.Clean(f) == cleanTarget {
			continue
		}

		result, err := swiftParse(f)
		if err != nil {
			slog.Debug("Skipping unparseable file", "path", f, "err", err)
			continue
		}

		for _, dt := range result.DefinedTypes {
			if refSet[dt] {
				deps = append(deps, f)
				slog.Debug("Found dependency file", "path", f, "type", dt)
				break
			}
		}
	}

	return deps, nil
}
