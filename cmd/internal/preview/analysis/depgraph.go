package analysis

import (
	"context"
	"log/slog"
	"path/filepath"
)

// DependencyGraph holds the set of all files transitively depended upon by a target.
// Used by the watcher to decide whether a file change is relevant.
type DependencyGraph struct {
	all   map[string]bool // cleaned file paths → true for all transitive dependencies
	depth map[string]int  // cleaned file path → BFS depth (0=target, 1=direct, 2+=transitive)
}

// Contains reports whether path (after filepath.Clean) is in the dependency graph.
func (g *DependencyGraph) Contains(path string) bool {
	return g.all[filepath.Clean(path)]
}

// Len returns the number of files in the dependency graph.
func (g *DependencyGraph) Len() int {
	return len(g.all)
}

// NewDependencyGraph creates a DependencyGraph from a set of file paths.
// All files are assigned depth 0. This is intended for testing and simple
// construction where BFS depth information is not needed.
func NewDependencyGraph(paths []string) *DependencyGraph {
	g := &DependencyGraph{
		all:   make(map[string]bool, len(paths)),
		depth: make(map[string]int, len(paths)),
	}
	for _, p := range paths {
		clean := filepath.Clean(p)
		g.all[clean] = true
		g.depth[clean] = 0
	}
	return g
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
		all:   make(map[string]bool),
		depth: make(map[string]int),
	}
	cleanTarget := filepath.Clean(targetFile)
	graph.all[cleanTarget] = true
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
				if graph.all[cleanPath] {
					continue
				}
				graph.all[cleanPath] = true
				graph.depth[cleanPath] = nextDepth
				queue = append(queue, bfsEntry{path: cleanPath, depth: nextDepth})
			}
		}
	}

	slog.Debug("Built transitive dependency graph",
		"target", targetFile,
		"files", len(graph.all),
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
	return g.DepsUpTo(1)
}

// DepsUpTo returns file paths at depth 1 through maxDepth (inclusive).
// Depth 0 (the target itself) is never included.
//
//   - maxDepth == 0 → empty slice (target only, no deps)
//   - maxDepth == 1 → equivalent to DirectDeps()
//   - maxDepth < 0  → all transitive dependencies (no depth limit)
func (g *DependencyGraph) DepsUpTo(maxDepth int) []string {
	if maxDepth == 0 {
		return nil
	}
	var deps []string
	for path, d := range g.depth {
		if d == 0 {
			continue // skip target
		}
		if maxDepth > 0 && d > maxDepth {
			continue
		}
		deps = append(deps, path)
	}
	return deps
}
