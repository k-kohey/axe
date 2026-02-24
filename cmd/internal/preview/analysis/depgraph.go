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
// When cache is non-nil, referenced types are looked up from the in-memory
// Index Store cache (no subprocess calls). When cache is nil, the parser
// fallback is used instead.
func BuildTransitiveDeps(ctx context.Context, targetFile string, typeMap map[string][]string, cache *IndexStoreCache, parser SwiftFileParser) (*DependencyGraph, error) {
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

		referencedTypes := refsFromCacheOrParser(entry.path, cache, parser)

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

// refsFromCacheOrParser returns referenced type names for a file,
// preferring the Index Store cache when available.
func refsFromCacheOrParser(path string, cache *IndexStoreCache, parser SwiftFileParser) []string {
	if cache != nil {
		return cache.ReferencedTypes(path)
	}
	if parser != nil {
		refs, _, err := parser.ParseTypes(path)
		if err != nil {
			slog.Debug("Skipping file in BFS (parse error)", "path", path, "err", err)
			return nil
		}
		return refs
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
