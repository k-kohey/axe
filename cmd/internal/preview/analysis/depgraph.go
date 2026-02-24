package analysis

import (
	"context"
	"log/slog"
	"path/filepath"
)

// DependencyGraph holds the set of all files transitively depended upon by a target.
// Used by the watcher to decide whether a file change is relevant.
type DependencyGraph struct {
	All   map[string]bool // cleaned file paths → true for all transitive dependencies
	depth map[string]int  // cleaned file path → BFS depth (0=target, 1=direct, 2+=transitive)
}

// bfsEntry is a BFS queue element that pairs a file path with its depth.
type bfsEntry struct {
	path  string
	depth int
}

// BuildTransitiveDeps performs a BFS over the type→file map starting from
// targetFile, collecting all transitively referenced files.
// The returned graph includes targetFile itself.
//
// Referenced types are looked up from the in-memory Index Store cache.
func BuildTransitiveDeps(ctx context.Context, targetFile string, typeMap map[string][]string, cache *IndexStoreCache) (*DependencyGraph, error) {
	graph := &DependencyGraph{
		All:   make(map[string]bool),
		depth: make(map[string]int),
	}
	cleanTarget := filepath.Clean(targetFile)
	graph.All[cleanTarget] = true
	graph.depth[cleanTarget] = 0

	// BFS queue of files to process.
	queue := []bfsEntry{{path: cleanTarget, depth: 0}}

	for len(queue) > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		entry := queue[0]
		queue = queue[1:]

		referencedTypes := refsFromCache(entry.path, cache)

		nextDepth := entry.depth + 1
		for _, typeName := range referencedTypes {
			filePaths, ok := typeMap[typeName]
			if !ok {
				continue
			}
			for _, filePath := range filePaths {
				cleanPath := filepath.Clean(filePath)
				if graph.All[cleanPath] {
					continue
				}
				graph.All[cleanPath] = true
				graph.depth[cleanPath] = nextDepth
				queue = append(queue, bfsEntry{path: cleanPath, depth: nextDepth})
			}
		}
	}

	slog.Debug("Built transitive dependency graph",
		"target", targetFile,
		"files", len(graph.All),
	)
	return graph, nil
}

// refsFromCache returns referenced type names for a file from the Index Store cache.
func refsFromCache(path string, cache *IndexStoreCache) []string {
	if cache != nil {
		return cache.ReferencedTypes(path)
	}
	return nil
}

// DirectDeps returns the file paths at depth 1 (direct dependencies of the target).
func (g *DependencyGraph) DirectDeps() []string {
	var deps []string
	for path, d := range g.depth {
		if d == 1 {
			deps = append(deps, path)
		}
	}
	return deps
}
