package analysis

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

// IndexStoreCache holds project-wide Index Store data in memory.
// It is loaded once after a build and provides fast lookups for
// type→file mapping, per-file type references, and defined types.
//
// The cache is immutable after construction: callers must not modify
// the maps or slices returned by its accessor methods.
type IndexStoreCache struct {
	mu      sync.RWMutex
	files   map[string]*pb.IndexFileData // filePath → per-file data
	typeMap map[string][]string          // typeName → []filePath
}

// NewIndexStoreCache creates an IndexStoreCache from pre-built maps.
// Used by tests that need to construct a cache without invoking the index reader.
func NewIndexStoreCache(files map[string]*pb.IndexFileData, typeMap map[string][]string) *IndexStoreCache {
	return &IndexStoreCache{files: files, typeMap: typeMap}
}

func indexPathCandidates(path string) []string {
	if path == "" {
		return nil
	}

	seen := map[string]bool{}
	var paths []string
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		candidate = filepath.Clean(candidate)
		if !seen[candidate] {
			seen[candidate] = true
			paths = append(paths, candidate)
		}
	}

	add(path)
	if abs, err := filepath.Abs(path); err == nil {
		add(abs)
		if eval, err := filepath.EvalSymlinks(abs); err == nil {
			add(eval)
		}
	}

	for _, candidate := range append([]string(nil), paths...) {
		if suffix, ok := strings.CutPrefix(candidate, "/private/tmp/"); ok {
			add("/tmp/" + suffix)
		}
		if suffix, ok := strings.CutPrefix(candidate, "/tmp/"); ok {
			add("/private/tmp/" + suffix)
		}
	}

	return paths
}

func (c *IndexStoreCache) fileDataLocked(path string) *pb.IndexFileData {
	for _, candidate := range indexPathCandidates(path) {
		if fd := c.files[candidate]; fd != nil {
			return fd
		}
	}
	return nil
}

// LoadIndexStore invokes the extended axe-index-reader and caches all data.
func LoadIndexStore(ctx context.Context, indexStorePath, sourceRoot string) (*IndexStoreCache, error) {
	result, err := readIndexStore(ctx, indexStorePath, sourceRoot)
	if err != nil {
		return nil, err
	}

	cache := &IndexStoreCache{
		files:   make(map[string]*pb.IndexFileData, len(result.GetFiles())),
		typeMap: make(map[string][]string, len(result.GetTypeFileMap())),
	}

	for _, fd := range result.GetFiles() {
		cache.files[fd.GetFilePath()] = fd
	}

	for typeName, filePath := range result.GetTypeFileMap() {
		cache.typeMap[typeName] = []string{filePath}
	}

	slog.Debug("Loaded index store cache",
		"files", len(cache.files),
		"typeMapEntries", len(cache.typeMap),
	)
	return cache, nil
}

// TypeFileMultiMap returns the type→file mapping for dependency resolution.
func (c *IndexStoreCache) TypeFileMultiMap() map[string][]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.typeMap
}

// ReferencedTypes returns the referenced type names for a file.
func (c *IndexStoreCache) ReferencedTypes(path string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fd := c.fileDataLocked(path)
	if fd == nil {
		return nil
	}
	return fd.GetReferencedTypeNames()
}

// DefinedTypes returns the defined type names for a file.
func (c *IndexStoreCache) DefinedTypes(path string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fd := c.fileDataLocked(path)
	if fd == nil {
		return nil
	}
	return fd.GetDefinedTypeNames()
}

// FileData returns the per-file Index Store data for the given path.
// Returns nil if the file is not in the cache.
func (c *IndexStoreCache) FileData(path string) *pb.IndexFileData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fileDataLocked(path)
}

// FileModuleName returns the Swift module name for the given file path.
// Returns empty string if the file is not in the cache or has no module info.
func (c *IndexStoreCache) FileModuleName(path string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fd := c.fileDataLocked(path)
	if fd == nil {
		return ""
	}
	return fd.GetModuleName()
}
