package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"github.com/k-kohey/axe/internal/procgroup"
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
	cmd := procgroup.Command(ctx, binPath, "parse", path)
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

// nameKey identifies a member source by type name, member name, and kind.
type nameKey struct {
	typeName string
	name     string
	kind     pb.MemberSourceKind
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

	// Also build a fallback map: (typeName, name, kind) → MemberSource
	// Used when line numbers don't match exactly (e.g. minor offset differences).
	//
	// TODO: fallback は (typeName, name, kind) あたり最初の1件しか保持しない。
	// 同一型にオーバーロードされたメソッド（例: fetch(id:) と fetch(query:)）が
	// ある場合、行番号ずれ時に誤った MemberSource を返す可能性がある。
	// 現実的には行番号が一致する exact match で解決されるため実害は薄いが、
	// 根本的に解決するなら fallback をリスト化して selector 等で絞り込む必要がある。
	fallbackMap := make(map[nameKey]*pb.MemberSource, len(parserResult.GetMemberSources()))
	for _, ms := range parserResult.GetMemberSources() {
		key := nameKey{typeName: ms.GetTypeName(), name: ms.GetName(), kind: ms.GetKind()}
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
				ms := lookupMemberSource(sourceMap, fallbackMap, typeName, idxMember, pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY)
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
				ms := lookupMemberSource(sourceMap, fallbackMap, typeName, idxMember, pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD)
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
// falling back to (typeName, name, kind) if the exact line match is not found.
// expectedKind ensures that a property lookup never accidentally returns a
// method source (or vice versa) when different members share the same name.
//
// Index Store はメソッド名をセレクター形式 ("greet(name:)") で返すが、
// パーサーはベース名 ("greet") で返す。メソッドの場合はベース名に正規化して比較する。
// NOTE: この正規化は暫定対応。本質的には USR ベースの結合に移行し、
// 名前の文字列比較自体を不要にすべき。
func lookupMemberSource(
	sourceMap map[memberKey]*pb.MemberSource,
	fallbackMap map[nameKey]*pb.MemberSource,
	typeName string,
	idxMember *pb.IndexMemberInfo,
	expectedKind pb.MemberSourceKind,
) *pb.MemberSource {
	memberName := idxMember.GetName()

	// Index Store のメソッド名はセレクター形式 ("greet(name:)") だが、
	// パーサーのキーはベース名 ("greet")。メソッドの場合はベース名に正規化する。
	if expectedKind == pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD {
		memberName = selectorBaseName(memberName)
	}

	// Try exact match first (typeName + line + name), then validate kind.
	key := memberKey{typeName: typeName, line: idxMember.GetLine(), name: memberName}
	if ms, ok := sourceMap[key]; ok && ms.GetKind() == expectedKind {
		return ms
	}

	// Fallback: match by (typeName, name, kind).
	nk := nameKey{typeName: typeName, name: memberName, kind: expectedKind}
	if ms, ok := fallbackMap[nk]; ok {
		return ms
	}

	return nil
}

// selectorBaseName extracts the base name from a Swift selector.
// e.g. "greet(name:)" → "greet", "refresh()" → "refresh", "greet" → "greet"
func selectorBaseName(selector string) string {
	if base, _, ok := strings.Cut(selector, "("); ok {
		return base
	}
	return selector
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
