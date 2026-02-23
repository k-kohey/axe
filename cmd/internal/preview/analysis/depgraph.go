package analysis

import (
	"context"
	"log/slog"
	"path/filepath"
)

// DependencyGraph holds the set of all files transitively depended upon by a target.
// Used by the watcher to decide whether a file change is relevant.
type DependencyGraph struct {
	All map[string]bool // cleaned file paths → true for all transitive dependencies
}

// BuildTransitiveDeps performs a BFS over the type→file map starting from
// targetFile, collecting all transitively referenced files.
// The returned graph includes targetFile itself.
func BuildTransitiveDeps(ctx context.Context, targetFile string, typeMap map[string][]string, parser SwiftFileParser) (*DependencyGraph, error) {
	graph := &DependencyGraph{All: make(map[string]bool)}
	cleanTarget := filepath.Clean(targetFile)
	graph.All[cleanTarget] = true

	// BFS queue of files to process.
	queue := []string{cleanTarget}

	for len(queue) > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		current := queue[0]
		queue = queue[1:]

		referencedTypes, _, err := parser.ParseTypes(current)
		if err != nil {
			slog.Debug("Skipping file in BFS (parse error)", "path", current, "err", err)
			continue
		}

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
				queue = append(queue, cleanPath)
			}
		}
	}

	slog.Debug("Built transitive dependency graph",
		"target", targetFile,
		"files", len(graph.All),
	)
	return graph, nil
}
