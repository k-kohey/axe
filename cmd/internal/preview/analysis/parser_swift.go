package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"google.golang.org/protobuf/encoding/protojson"
)

// parseCacheEntry holds a cached parse result for a single file.
type parseCacheEntry struct {
	modTime int64
	result  *pb.ParseResult
}

// parseCache caches results of swiftParse keyed by file path + modTime
// to avoid redundant subprocess invocations.
var parseCache struct {
	sync.Mutex
	entries map[string]*parseCacheEntry
}

// ResetCache clears the parse cache. Used in tests where files are
// overwritten rapidly and modTime may not change.
func ResetCache() {
	parseCache.Lock()
	parseCache.entries = nil
	parseCache.Unlock()
}

// swiftParse invokes the axe-parser CLI on the given file and returns the
// parsed result. Results are cached per file path + modification time.
func swiftParse(path string) (*pb.ParseResult, error) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "parse", path)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("axe-parser failed: %w\n%s", err, ee.Stderr)
		}
		return nil, fmt.Errorf("running axe-parser: %w", err)
	}

	var result pb.ParseResult
	if err := protojson.Unmarshal(out, &result); err != nil {
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

// protoKindToString maps proto TypeKind enum to the string representation
// used by the internal TypeInfo type.
var protoKindToString = map[pb.TypeKind]string{
	pb.TypeKind_TYPE_KIND_UNKNOWN: "unknown",
	pb.TypeKind_TYPE_KIND_STRUCT:  "struct",
	pb.TypeKind_TYPE_KIND_CLASS:   "class",
	pb.TypeKind_TYPE_KIND_ENUM:    "enum",
	pb.TypeKind_TYPE_KIND_ACTOR:   "actor",
}

// convertProtoType converts a protobuf TypeInfo to the internal TypeInfo.
func convertProtoType(pt *pb.TypeInfo) TypeInfo {
	ti := TypeInfo{
		Name:           pt.GetName(),
		Kind:           protoKindToString[pt.GetKind()],
		AccessLevel:    pt.GetAccessLevel(),
		InheritedTypes: pt.GetInheritedTypes(),
	}
	for _, pp := range pt.GetProperties() {
		ti.Properties = append(ti.Properties, PropertyInfo{
			Name:     pp.GetName(),
			TypeExpr: pp.GetTypeExpr(),
			BodyLine: int(pp.GetBodyLine()),
			Source:   pp.GetSource(),
		})
	}
	for _, pm := range pt.GetMethods() {
		ti.Methods = append(ti.Methods, MethodInfo{
			Name:      pm.GetName(),
			Selector:  pm.GetSelector(),
			Signature: pm.GetSignature(),
			BodyLine:  int(pm.GetBodyLine()),
			Source:    pm.GetSource(),
		})
	}
	return ti
}

// convertTypes converts parsed proto type info to internal types,
// filtering out types with no computed properties or methods.
func convertTypes(protoTypes []*pb.TypeInfo) []TypeInfo {
	var types []TypeInfo
	for _, pt := range protoTypes {
		if len(pt.GetProperties()) > 0 || len(pt.GetMethods()) > 0 {
			types = append(types, convertProtoType(pt))
		}
	}
	return types
}

// convertPreviewBlocks converts proto PreviewBlocks to internal PreviewBlocks.
func convertPreviewBlocks(protoBlocks []*pb.PreviewBlock) []PreviewBlock {
	blocks := make([]PreviewBlock, 0, len(protoBlocks))
	for _, block := range protoBlocks {
		blocks = append(blocks, PreviewBlock{
			StartLine: int(block.GetStartLine()),
			Title:     block.GetTitle(),
			Source:    block.GetSource(),
		})
	}
	return blocks
}

// SourceFile parses types and imports from a Swift source file.
// It requires at least one View type with a body property.
func SourceFile(path string) ([]TypeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing source file: %w", err)
	}

	types := convertTypes(result.GetTypes())

	// Require at least one View type with a body property.
	hasBody := false
	for _, t := range types {
		if !t.IsView() {
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
	return types, result.GetImports(), nil
}

// PreviewBlocks extracts all #Preview { ... } blocks from the source file.
func PreviewBlocks(path string) ([]PreviewBlock, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, fmt.Errorf("parsing preview blocks: %w", err)
	}

	blocks := convertPreviewBlocks(result.GetPreviews())
	for _, b := range blocks {
		slog.Debug("Found #Preview block", "line", b.StartLine, "title", b.Title)
	}

	return blocks, nil
}

// Skeleton computes a SHA-256 hash of the source file with body regions
// stripped out. Uses the swift-syntax AST for accurate body detection.
func Skeleton(path string) (string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return "", fmt.Errorf("computing skeleton: %w", err)
	}
	return result.GetSkeletonHash(), nil
}

// DefaultParser returns a SwiftFileParser backed by the axe-parser CLI.
func DefaultParser() SwiftFileParser { return defaultSwiftFileParser{} }

type defaultSwiftFileParser struct{}

func (defaultSwiftFileParser) ParseTypes(path string) ([]string, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, err
	}
	return result.GetReferencedTypes(), result.GetDefinedTypes(), nil
}

// DependencyFile parses types and imports from a dependency Swift file.
// Unlike SourceFile, it does not require a body property or View conformance.
// It returns all types (with computed properties/methods) found in the file.
func DependencyFile(path string) ([]TypeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing dependency file: %w", err)
	}

	types := convertTypes(result.GetTypes())
	return types, result.GetImports(), nil
}

// FilterPrivateCollisions removes dependency files whose private type names
// collide with private type names in other tracked files.
// The target file (identified by targetPath) is never removed.
// Removed files should not be tracked for hot-reload; changes to them will
// trigger a full rebuild via the untracked path.
func FilterPrivateCollisions(files []FileThunkData, targetPath string) (kept []FileThunkData, excludedPaths []string) {
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
