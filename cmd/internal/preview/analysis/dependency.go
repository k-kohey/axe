package analysis

import (
	"context"
	"fmt"
	"log/slog"
)

// ResolveTransitiveDependencies uses the index store to build a complete
// transitive dependency graph for the target file.
//
// The cache must be non-nil; the Index Store is required for dependency resolution.
//
// Returns the dependency graph and the 1-level dependency list for thunk generation.
func ResolveTransitiveDependencies(ctx context.Context, targetFile string, cache *IndexStoreCache) (*DependencyGraph, []string, error) {
	if cache == nil {
		return nil, nil, fmt.Errorf("index store cache is required for dependency resolution")
	}

	typeMap := cache.TypeFileMultiMap()

	graph, err := BuildTransitiveDeps(ctx, targetFile, typeMap, cache)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		slog.Warn("Transitive graph construction failed", "err", err)
		return nil, nil, err
	}

	return graph, graph.DirectDeps(), nil
}
