package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
)

// ResolveDependencies finds source files that define types referenced by the target file.
// It returns a list of absolute paths to 1-level dependency files (excluding the target itself).
func ResolveDependencies(ctx context.Context, targetFile string, projectRoot string, sl SwiftFileLister, parser SwiftFileParser) ([]string, error) {
	referencedTypes, definedTypes, err := parser.ParseTypes(targetFile)
	if err != nil {
		return nil, fmt.Errorf("parsing target file: %w", err)
	}

	if len(referencedTypes) == 0 {
		slog.Debug("No referenced types in target file")
		return nil, nil
	}

	refSet := make(map[string]bool, len(referencedTypes))
	for _, t := range referencedTypes {
		refSet[t] = true
	}

	// Remove types defined in the target file itself.
	for _, t := range definedTypes {
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if filepath.Clean(f) == cleanTarget {
			continue
		}

		_, fileDefined, err := parser.ParseTypes(f)
		if err != nil {
			slog.Debug("Skipping unparseable file", "path", f, "err", err)
			continue
		}

		for _, dt := range fileDefined {
			if refSet[dt] {
				deps = append(deps, f)
				slog.Debug("Found dependency file", "path", f, "type", dt)
				break
			}
		}
	}

	return deps, nil
}

// ResolveTransitiveDependencies uses the index store to build a complete
// transitive dependency graph for the target file. If the index store is
// unavailable or fails, it falls back to the 1-level ResolveDependencies.
//
// When cache is non-nil, the BFS uses it for in-memory lookups (no subprocess
// calls per file). When cache is nil, the type→file map is read from the index
// store and the parser is used for per-file type references.
//
// Returns the dependency graph and the 1-level dependency list for thunk generation.
func ResolveTransitiveDependencies(ctx context.Context, targetFile string, projectRoot string, indexStorePath string, sl SwiftFileLister, cache *IndexStoreCache, parser SwiftFileParser) (*DependencyGraph, []string, error) {
	var typeMap map[string][]string

	if cache != nil {
		typeMap = cache.TypeFileMultiMap()
	} else {
		var err error
		typeMap, err = readTypeFileMultiMap(ctx, indexStorePath, projectRoot)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			slog.Warn("Index store unavailable, falling back to 1-level dependency resolution", "err", err)
			deps, depErr := ResolveDependencies(ctx, targetFile, projectRoot, sl, parser)
			return nil, deps, depErr
		}
	}

	graph, err := BuildTransitiveDeps(ctx, targetFile, typeMap, cache, parser)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		slog.Warn("Transitive graph construction failed, falling back to 1-level dependency resolution", "err", err)
		deps, depErr := ResolveDependencies(ctx, targetFile, projectRoot, sl, parser)
		return nil, deps, depErr
	}

	return graph, graph.DirectDeps(), nil
}
