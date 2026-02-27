package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
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

type parserMemberLineKey struct {
	line int32
	kind pb.MemberSourceKind
}

type parserTypeMemberKey struct {
	typeName string
	baseName string
	kind     pb.MemberSourceKind
}

type parserIndexes struct {
	byLineKind     map[parserMemberLineKey][]*pb.MemberSource
	byTypeAndBase  map[parserTypeMemberKey][]*pb.MemberSource
	byShortAndBase map[parserTypeMemberKey][]*pb.MemberSource
}

func buildParserIndexes(parserResult *pb.ParseResult) parserIndexes {
	idx := parserIndexes{
		byLineKind:     make(map[parserMemberLineKey][]*pb.MemberSource, len(parserResult.GetMemberSources())),
		byTypeAndBase:  make(map[parserTypeMemberKey][]*pb.MemberSource, len(parserResult.GetMemberSources())),
		byShortAndBase: make(map[parserTypeMemberKey][]*pb.MemberSource, len(parserResult.GetMemberSources())),
	}

	for _, ms := range parserResult.GetMemberSources() {
		kind := ms.GetKind()
		base := parserMemberBaseName(ms)

		idx.byLineKind[parserMemberLineKey{line: ms.GetLine(), kind: kind}] = append(
			idx.byLineKind[parserMemberLineKey{line: ms.GetLine(), kind: kind}],
			ms,
		)

		typeName := ms.GetTypeName()
		idx.byTypeAndBase[parserTypeMemberKey{typeName: typeName, baseName: base, kind: kind}] = append(
			idx.byTypeAndBase[parserTypeMemberKey{typeName: typeName, baseName: base, kind: kind}],
			ms,
		)

		short := shortTypeName(typeName)
		idx.byShortAndBase[parserTypeMemberKey{typeName: short, baseName: base, kind: kind}] = append(
			idx.byShortAndBase[parserTypeMemberKey{typeName: short, baseName: base, kind: kind}],
			ms,
		)
	}

	return idx
}

// combineWithIndexStore combines Index Store member metadata with parser source text
// to produce TypeInfo values. The Index Store provides filtering (instance/computed/non-init)
// and the parser provides source text.
func combineWithIndexStore(indexData *pb.IndexFileData, parserResult *pb.ParseResult) []TypeInfo {
	indexes := buildParserIndexes(parserResult)

	var types []TypeInfo
	for _, idxType := range indexData.GetTypes() {
		ti := TypeInfo{
			Name:           idxType.GetName(),
			Kind:           protoKindToString[idxType.GetKind()],
			AccessLevel:    idxType.GetAccessLevel(),
			InheritedTypes: idxType.GetInheritedTypes(),
		}

		resolvedTypeCounts := map[string]int{}
		usedForType := map[*pb.MemberSource]struct{}{}
		for _, idxMember := range idxType.GetMembers() {
			switch idxMember.GetKind() {
			case pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY:
				if !idxMember.GetIsComputed() {
					continue // stored property — skip
				}
				ms := lookupMemberSource(indexes, idxType.GetName(), idxMember, usedForType)
				if ms == nil {
					slog.Debug("No parser source for computed property",
						"type", idxType.GetName(), "member", idxMember.GetName(),
						"line", idxMember.GetLine())
					continue
				}
				resolvedTypeCounts[ms.GetTypeName()]++
				ti.Properties = append(ti.Properties, PropertyInfo{
					Name:     ms.GetName(),
					TypeExpr: ms.GetTypeExpr(),
					BodyLine: int(ms.GetBodyLine()),
					Source:   ms.GetSource(),
				})

			case pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD:
				ms := lookupMemberSource(indexes, idxType.GetName(), idxMember, usedForType)
				if ms == nil {
					slog.Debug("No parser source for instance method",
						"type", idxType.GetName(), "member", idxMember.GetName(),
						"line", idxMember.GetLine())
					continue
				}
				resolvedTypeCounts[ms.GetTypeName()]++
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
			if resolvedTypeName := dominantTypeName(resolvedTypeCounts); resolvedTypeName != "" {
				ti.Name = resolvedTypeName
			}
			if al, ok := parserResult.GetTypeAccessLevels()[ti.Name]; ok {
				ti.AccessLevel = al
			}
			types = append(types, ti)
		}
	}

	return types
}

// lookupMemberSource resolves parser source for an index member.
// It prefers line+kind matches (index-store driven), then uses normalized
// base names as a fallback when line numbers drift (e.g. #Preview stripping).
func lookupMemberSource(
	indexes parserIndexes,
	indexTypeName string,
	idxMember *pb.IndexMemberInfo,
	used map[*pb.MemberSource]struct{},
) *pb.MemberSource {
	kind, ok := memberSourceKindForIndexMember(idxMember.GetKind())
	if !ok {
		return nil
	}

	base := indexMemberBaseName(idxMember)
	lineKey := parserMemberLineKey{line: idxMember.GetLine(), kind: kind}
	if cands, ok := indexes.byLineKind[lineKey]; ok {
		if ms := pickMemberCandidate(cands, used, indexTypeName, base); ms != nil {
			return ms
		}
	}

	typeKey := parserTypeMemberKey{typeName: indexTypeName, baseName: base, kind: kind}
	if cands, ok := indexes.byTypeAndBase[typeKey]; ok {
		if ms := pickMemberCandidate(cands, used, indexTypeName, base); ms != nil {
			return ms
		}
	}

	shortKey := parserTypeMemberKey{typeName: shortTypeName(indexTypeName), baseName: base, kind: kind}
	if cands, ok := indexes.byShortAndBase[shortKey]; ok {
		if ms := pickMemberCandidate(cands, used, indexTypeName, base); ms != nil {
			return ms
		}
	}

	slog.Debug("Member source lookup unresolved",
		"indexType", indexTypeName,
		"member", idxMember.GetName(),
		"line", idxMember.GetLine(),
		"kind", kind.String(),
	)
	return nil
}

func memberSourceKindForIndexMember(kind pb.MemberKind) (pb.MemberSourceKind, bool) {
	switch kind {
	case pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY:
		return pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, true
	case pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD:
		return pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD, true
	default:
		return pb.MemberSourceKind_MEMBER_SOURCE_KIND_UNKNOWN, false
	}
}

func parserMemberBaseName(ms *pb.MemberSource) string { return ms.GetName() }

func indexMemberBaseName(idxMember *pb.IndexMemberInfo) string {
	name := idxMember.GetName()
	if idxMember.GetKind() != pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD {
		return name
	}
	if i := strings.Index(name, "("); i > 0 {
		return name[:i]
	}
	return name
}

func shortTypeName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

func dominantTypeName(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	type candidate struct {
		name  string
		count int
	}
	items := make([]candidate, 0, len(counts))
	for name, count := range counts {
		items = append(items, candidate{name: name, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})
	return items[0].name
}

func pickMemberCandidate(
	candidates []*pb.MemberSource,
	used map[*pb.MemberSource]struct{},
	indexTypeName string,
	indexBaseName string,
) *pb.MemberSource {
	if len(candidates) == 0 {
		return nil
	}

	filtered := make([]*pb.MemberSource, 0, len(candidates))
	for _, ms := range candidates {
		if _, already := used[ms]; already {
			continue
		}
		filtered = append(filtered, ms)
	}
	if len(filtered) == 0 {
		return nil
	}

	byBase := make([]*pb.MemberSource, 0, len(filtered))
	for _, ms := range filtered {
		if parserMemberBaseName(ms) == indexBaseName {
			byBase = append(byBase, ms)
		}
	}
	if len(byBase) == 1 {
		used[byBase[0]] = struct{}{}
		return byBase[0]
	}
	if len(byBase) > 1 {
		for _, ms := range byBase {
			if shortTypeName(ms.GetTypeName()) == shortTypeName(indexTypeName) {
				used[ms] = struct{}{}
				return ms
			}
		}
		used[byBase[0]] = struct{}{}
		return byBase[0]
	}

	for _, ms := range filtered {
		if shortTypeName(ms.GetTypeName()) == shortTypeName(indexTypeName) {
			used[ms] = struct{}{}
			return ms
		}
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
		if len(result.GetPreviews()) == 0 {
			return nil, nil, fmt.Errorf("no type conforming to View with body property found in %s", path)
		}
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
