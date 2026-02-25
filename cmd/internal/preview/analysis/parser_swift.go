package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
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

// memberKey identifies a member source by type name, line, and member name.
type memberKey struct {
	typeName string
	line     int32
	name     string
}

// nameKey identifies a member source by type name and member name only.
type nameKey struct {
	typeName string
	name     string
}

// combineWithIndexStore combines Index Store member metadata with parser source text
// to produce TypeInfo values. The Index Store provides filtering (instance/computed/non-init)
// and the parser provides source text.
func combineWithIndexStore(indexData *pb.IndexFileData, parserResult *pb.ParseResult) []TypeInfo {
	// Build short→qualified name mapping from parser output.
	// The parser emits qualified names (e.g. "OuterView.InnerView") while the
	// Index Store uses short names (e.g. "InnerView"). We need the qualified
	// name for correct extension generation in thunk templates.
	qualifiedNames := make(map[string]string) // shortName → qualifiedName
	for _, ms := range parserResult.GetMemberSources() {
		qn := ms.GetTypeName()
		shortName := qn
		if idx := strings.LastIndex(qn, "."); idx >= 0 {
			shortName = qn[idx+1:]
		}
		// First occurrence wins to avoid ambiguity.
		if _, exists := qualifiedNames[shortName]; !exists {
			qualifiedNames[shortName] = qn
		}
	}

	// Build lookup: (typeName, line, name) → MemberSource
	sourceMap := make(map[memberKey]*pb.MemberSource, len(parserResult.GetMemberSources()))
	for _, ms := range parserResult.GetMemberSources() {
		key := memberKey{typeName: ms.GetTypeName(), line: ms.GetLine(), name: ms.GetName()}
		sourceMap[key] = ms
	}

	// Also build a name-only fallback map: (typeName, name) → MemberSource
	// Used when line numbers don't match exactly (e.g. minor offset differences).
	fallbackMap := make(map[nameKey]*pb.MemberSource, len(parserResult.GetMemberSources()))
	for _, ms := range parserResult.GetMemberSources() {
		key := nameKey{typeName: ms.GetTypeName(), name: ms.GetName()}
		// Only store the first occurrence per (typeName, name) to avoid ambiguity.
		if _, exists := fallbackMap[key]; !exists {
			fallbackMap[key] = ms
		}
	}

	var types []TypeInfo
	for _, idxType := range indexData.GetTypes() {
		// Resolve qualified name: use the parser's qualified name if available,
		// otherwise fall back to the Index Store's short name.
		typeName := idxType.GetName()
		if qn, ok := qualifiedNames[typeName]; ok {
			typeName = qn
		}

		// Use parser-derived access level (accurate) over Index Store's (unreliable).
		// The Index Store SDK does not populate SymbolProperty access control flags
		// (rawValue is 0 for most symbols), so its access_level is always "internal".
		accessLevel := idxType.GetAccessLevel()
		if al, ok := parserResult.GetTypeAccessLevels()[typeName]; ok {
			accessLevel = al
		}

		ti := TypeInfo{
			Name:           typeName,
			Kind:           protoKindToString[idxType.GetKind()],
			AccessLevel:    accessLevel,
			InheritedTypes: idxType.GetInheritedTypes(),
		}

		for _, idxMember := range idxType.GetMembers() {
			switch idxMember.GetKind() {
			case pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY:
				if !idxMember.GetIsComputed() {
					continue // stored property — skip
				}
				ms := lookupMemberSource(sourceMap, fallbackMap, typeName, idxMember)
				if ms == nil {
					slog.Debug("No parser source for computed property",
						"type", typeName, "member", idxMember.GetName(),
						"line", idxMember.GetLine())
					continue
				}
				ti.Properties = append(ti.Properties, PropertyInfo{
					Name:     ms.GetName(),
					TypeExpr: ms.GetTypeExpr(),
					BodyLine: int(ms.GetBodyLine()),
					Source:   ms.GetSource(),
				})

			case pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD:
				ms := lookupMemberSource(sourceMap, fallbackMap, typeName, idxMember)
				if ms == nil {
					slog.Debug("No parser source for instance method",
						"type", typeName, "member", idxMember.GetName(),
						"line", idxMember.GetLine())
					continue
				}
				ti.Methods = append(ti.Methods, MethodInfo{
					Name:      ms.GetName(),
					Selector:  ms.GetSelector(),
					Signature: ms.GetSignature(),
					BodyLine:  int(ms.GetBodyLine()),
					Source:    ms.GetSource(),
				})

			default:
				// static, constructor, unknown — skip
				continue
			}
		}

		if len(ti.Properties) > 0 || len(ti.Methods) > 0 {
			types = append(types, ti)
		}
	}

	return types
}

// lookupMemberSource tries to find a MemberSource by (typeName, line, name),
// falling back to (typeName, name) if the exact line match is not found.
func lookupMemberSource(
	sourceMap map[memberKey]*pb.MemberSource,
	fallbackMap map[nameKey]*pb.MemberSource,
	typeName string,
	idxMember *pb.IndexMemberInfo,
) *pb.MemberSource {
	memberName := idxMember.GetName()

	// Try exact match first (typeName + line + name).
	key := memberKey{typeName: typeName, line: idxMember.GetLine(), name: memberName}
	if ms, ok := sourceMap[key]; ok {
		return ms
	}

	// Fallback: match by (typeName, name) only.
	nk := nameKey{typeName: typeName, name: memberName}
	if ms, ok := fallbackMap[nk]; ok {
		return ms
	}

	return nil
}

// SourceFile parses types and imports from a Swift source file.
// It requires an Index Store cache for semantic filtering.
// It requires at least one View type with a body property.
func SourceFile(path string, cache *IndexStoreCache) ([]TypeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing source file: %w", err)
	}

	var types []TypeInfo
	if cache != nil {
		indexData := cache.FileData(path)
		if indexData != nil {
			types = combineWithIndexStore(indexData, result)
		}
	}

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

// DependencyFile parses types and imports from a dependency Swift file.
// Unlike SourceFile, it does not require a body property or View conformance.
// It returns all types (with computed properties/methods) found in the file.
func DependencyFile(path string, cache *IndexStoreCache) ([]TypeInfo, []string, error) {
	result, err := swiftParse(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing dependency file: %w", err)
	}

	var types []TypeInfo
	if cache != nil {
		indexData := cache.FileData(path)
		if indexData != nil {
			types = combineWithIndexStore(indexData, result)
		}
	}

	return types, result.GetImports(), nil
}

// FilterPrivateCollisions removes ambiguous type definitions and members
// that reference them from the thunk output.
//
// ambiguousNames contains type names defined in multiple files across the
// entire module (from the Index Store). These are necessarily private/fileprivate
// types (public/internal names must be unique within a module).
// This parameter is critical because tracked files are only a subset of the
// module — a collision may exist with a non-tracked file that is still visible
// via -enable-private-imports.
//
// Additionally, per-file access level checks detect collisions within tracked
// files as a fallback (e.g. when the Index Store cache is unavailable).
//
// Filtering:
//  1. Type definitions whose name is ambiguous are removed from ALL files.
//  2. Members whose type annotations reference an ambiguous name are removed.
//     The original implementation is used instead (no dynamic replacement).
func FilterPrivateCollisions(files []FileThunkData, targetPath string, ambiguousNames map[string]bool) (kept []FileThunkData, excludedPaths []string) {
	// Start with module-wide ambiguous names from the Index Store.
	collidingNames := make(map[string]bool, len(ambiguousNames))
	for name := range ambiguousNames {
		collidingNames[name] = true
	}

	// Also detect collisions within tracked files via access level checks
	// (fallback for when Index Store data is incomplete).
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
	namePaths := make(map[string]map[string]bool)
	for _, nf := range privates {
		if namePaths[nf.name] == nil {
			namePaths[nf.name] = make(map[string]bool)
		}
		namePaths[nf.name][nf.path] = true
	}
	for name, paths := range namePaths {
		if len(paths) > 1 {
			collidingNames[name] = true
		}
	}

	if len(collidingNames) == 0 {
		return files, nil
	}

	// Phase 1: Remove ambiguous type definitions from ALL files.
	//          "extension DataFormatter" is ambiguous when both private types
	//          are visible via -enable-private-imports.
	// Phase 2: Remove members referencing ambiguous types from ALL files.
	for _, f := range files {
		var filteredTypes []TypeInfo
		for _, t := range f.Types {
			// Phase 1: Remove ambiguous type definitions.
			if collidingNames[t.Name] {
				isPrivate := t.AccessLevel == "private" || t.AccessLevel == "fileprivate"
				// Only remove private/fileprivate types or types whose name
				// matches a module-wide ambiguous name (which must be private).
				if isPrivate || ambiguousNames[t.Name] {
					slog.Debug("Excluding ambiguous type",
						"path", f.AbsPath, "type", t.Name)
					continue
				}
			}

			// Phase 2: Remove members that reference ambiguous type names.
			hadMembers := len(t.Properties) > 0 || len(t.Methods) > 0
			t = filterCollidingMembers(t, collidingNames, f.AbsPath)
			if hadMembers && len(t.Properties) == 0 && len(t.Methods) == 0 {
				slog.Debug("Excluding type after all members filtered",
					"path", f.AbsPath, "type", t.Name)
				continue
			}
			filteredTypes = append(filteredTypes, t)
		}

		if len(filteredTypes) == 0 {
			excludedPaths = append(excludedPaths, f.AbsPath)
		} else {
			f.Types = filteredTypes
			kept = append(kept, f)
		}
	}
	return kept, excludedPaths
}

// filterCollidingMembers removes properties and methods from a type whose
// type expressions or signatures reference any of the colliding type names.
func filterCollidingMembers(t TypeInfo, collidingNames map[string]bool, path string) TypeInfo {
	var props []PropertyInfo
	for _, p := range t.Properties {
		if referencesCollidingType(p.TypeExpr, collidingNames) {
			slog.Debug("Excluding property referencing colliding type",
				"path", path, "type", t.Name, "property", p.Name, "typeExpr", p.TypeExpr)
			continue
		}
		props = append(props, p)
	}
	t.Properties = props

	var methods []MethodInfo
	for _, m := range t.Methods {
		if referencesCollidingType(m.Signature, collidingNames) {
			slog.Debug("Excluding method referencing colliding type",
				"path", path, "type", t.Name, "method", m.Name, "signature", m.Signature)
			continue
		}
		methods = append(methods, m)
	}
	t.Methods = methods

	return t
}

// referencesCollidingType checks if a type expression or signature string
// contains any of the colliding type names as whole-word matches.
// Uses word-boundary matching to avoid false positives where a colliding
// name is a substring of an unrelated identifier (e.g. "View" in "isViewable").
func referencesCollidingType(expr string, collidingNames map[string]bool) bool {
	for name := range collidingNames {
		pattern := `\b` + regexp.QuoteMeta(name) + `\b`
		if matched, _ := regexp.MatchString(pattern, expr); matched {
			return true
		}
	}
	return false
}
