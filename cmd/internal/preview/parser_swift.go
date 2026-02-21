package preview

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
)

// swiftParseResult mirrors the JSON output from the axe-parser CLI.
type swiftParseResult struct {
	Types           []swiftTypeInfo    `json:"types"`
	Imports         []string           `json:"imports"`
	Previews        []swiftPreviewInfo `json:"previews"`
	SkeletonHash    string             `json:"skeletonHash"`
	ReferencedTypes []string           `json:"referencedTypes"`
	DefinedTypes    []string           `json:"definedTypes"`
}

type swiftTypeInfo struct {
	Name           string              `json:"name"`
	Kind           string              `json:"kind"`
	AccessLevel    string              `json:"accessLevel"`
	InheritedTypes []string            `json:"inheritedTypes"`
	Properties     []swiftPropertyInfo `json:"properties"`
	Methods        []swiftMethodInfo   `json:"methods"`
}

type swiftPropertyInfo struct {
	Name     string `json:"name"`
	TypeExpr string `json:"typeExpr"`
	BodyLine int    `json:"bodyLine"`
	Source   string `json:"source"`
}

type swiftMethodInfo struct {
	Name      string `json:"name"`
	Selector  string `json:"selector"`
	Signature string `json:"signature"`
	BodyLine  int    `json:"bodyLine"`
	Source    string `json:"source"`
}

type swiftPreviewInfo struct {
	StartLine int    `json:"startLine"`
	Title     string `json:"title"`
	Source    string `json:"source"`
}

// parseCacheEntry holds a cached parse result for a single file.
type parseCacheEntry struct {
	modTime int64
	result  *swiftParseResult
}

// parseCache caches results of swiftParse keyed by file path + modTime
// to avoid redundant subprocess invocations.
var parseCache struct {
	sync.Mutex
	entries map[string]*parseCacheEntry
}

// resetParseCache clears the parse cache. Used in tests where files are
// overwritten rapidly and modTime may not change.
func resetParseCache() {
	parseCache.Lock()
	parseCache.entries = nil
	parseCache.Unlock()
}

// swiftParse invokes the axe-parser CLI on the given file and returns the
// parsed result. Results are cached per file path + modification time.
func swiftParse(path string) (*swiftParseResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	modTime := info.ModTime().UnixNano()

	parseCache.Lock()
	if entry, ok := parseCache.entries[path]; ok && entry.modTime == modTime && entry.result != nil {
		cached := entry.result
		parseCache.Unlock()
		slog.Debug("Swift parse cache hit", "path", path)
		return cached, nil
	}
	parseCache.Unlock()

	binPath, err := ensureSwiftParser()
	if err != nil {
		return nil, fmt.Errorf("ensuring swift parser: %w", err)
	}

	cmd := exec.Command(binPath, "parse", path)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("axe-parser failed: %w\n%s", err, ee.Stderr)
		}
		return nil, fmt.Errorf("running axe-parser: %w", err)
	}

	var result swiftParseResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("decoding axe-parser output: %w", err)
	}

	parseCache.Lock()
	if parseCache.entries == nil {
		parseCache.entries = make(map[string]*parseCacheEntry)
	}
	parseCache.entries[path] = &parseCacheEntry{modTime: modTime, result: &result}
	parseCache.Unlock()

	return &result, nil
}

// convertTypes converts parsed Swift type info to internal types,
// filtering out types with no computed properties or methods.
func convertTypes(swiftTypes []swiftTypeInfo) []typeInfo {
	var types []typeInfo
	for _, st := range swiftTypes {
		var props []propertyInfo
		for _, sp := range st.Properties {
			props = append(props, propertyInfo(sp))
		}
		var methods []methodInfo
		for _, sm := range st.Methods {
			methods = append(methods, methodInfo(sm))
		}
		if len(props) > 0 || len(methods) > 0 {
			types = append(types, typeInfo{
				Name:           st.Name,
				Kind:           st.Kind,
				AccessLevel:    st.AccessLevel,
				InheritedTypes: st.InheritedTypes,
				Properties:     props,
				Methods:        methods,
			})
		}
	}
	return types
}

// parseSourceFile parses types and imports from a Swift source file.
// It requires at least one View type with a body property.
func parseSourceFile(path string) ([]typeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing source file: %w", err)
	}

	types := convertTypes(result.Types)

	// Require at least one View type with a body property.
	hasBody := false
	for _, t := range types {
		if !t.isView() {
			continue
		}
		for _, p := range t.Properties {
			if p.Name == "body" {
				hasBody = true
				break
			}
		}
	}
	if !hasBody {
		return nil, nil, fmt.Errorf("no type conforming to View with body property found in %s", path)
	}

	slog.Debug("Parsed types", "count", len(types))
	return types, result.Imports, nil
}

// parsePreviewBlocks extracts all #Preview { ... } blocks from the source file.
func parsePreviewBlocks(path string) ([]previewBlock, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, fmt.Errorf("parsing preview blocks: %w", err)
	}

	var blocks []previewBlock
	for _, sp := range result.Previews {
		blocks = append(blocks, previewBlock(sp))
		slog.Debug("Found #Preview block", "line", sp.StartLine, "title", sp.Title)
	}

	return blocks, nil
}

// computeSkeleton computes a SHA-256 hash of the source file with body regions
// stripped out. Uses the swift-syntax AST for accurate body detection.
func computeSkeleton(path string) (string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return "", fmt.Errorf("computing skeleton: %w", err)
	}
	return result.SkeletonHash, nil
}

// parseDependencyFile parses types and imports from a dependency Swift file.
// Unlike parseSourceFile, it does not require a body property or View conformance.
// It returns all types (with computed properties/methods) found in the file.
func parseDependencyFile(path string) ([]typeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing dependency file: %w", err)
	}

	types := convertTypes(result.Types)
	return types, result.Imports, nil
}

// filterPrivateCollisions removes dependency files whose private type names
// collide with private type names in other tracked files.
// The target file (identified by targetPath) is never removed.
// Removed files should not be tracked for hot-reload; changes to them will
// trigger a full rebuild via the untracked path.
func filterPrivateCollisions(files []fileThunkData, targetPath string) (kept []fileThunkData, excludedPaths []string) {
	// Collect private view names per file.
	type nameFile struct {
		name string
		path string
	}
	var privates []nameFile
	for _, f := range files {
		for _, t := range f.Types {
			if t.AccessLevel == "private" || t.AccessLevel == "fileprivate" {
				privates = append(privates, nameFile{name: t.Name, path: f.AbsPath})
			}
		}
	}

	// Find names that appear in more than one file.
	namePaths := make(map[string]map[string]bool) // name → set of file paths
	for _, nf := range privates {
		if namePaths[nf.name] == nil {
			namePaths[nf.name] = make(map[string]bool)
		}
		namePaths[nf.name][nf.path] = true
	}

	// Collect non-target file paths that participate in collisions.
	excludeSet := make(map[string]bool)
	for name, paths := range namePaths {
		if len(paths) <= 1 {
			continue
		}
		for p := range paths {
			if p != targetPath {
				excludeSet[p] = true
				slog.Debug("Excluding dependency due to private type collision", "path", p, "type", name)
			}
		}
	}

	for _, f := range files {
		if excludeSet[f.AbsPath] {
			excludedPaths = append(excludedPaths, f.AbsPath)
		} else {
			kept = append(kept, f)
		}
	}
	return kept, excludedPaths
}
