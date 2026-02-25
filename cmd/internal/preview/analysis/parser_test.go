package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

// --- combineWithIndexStore unit tests ---

func TestCombineWithIndexStore_ComputedProperty(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:           "HogeView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "HogeView", Line: 4, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if types[0].Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", types[0].Name)
	}
	if len(types[0].Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(types[0].Properties))
	}
	if types[0].Properties[0].Name != "body" {
		t.Errorf("Property name = %q, want body", types[0].Properties[0].Name)
	}
	if types[0].Properties[0].TypeExpr != "some View" {
		t.Errorf("TypeExpr = %q, want some View", types[0].Properties[0].TypeExpr)
	}
}

func TestCombineWithIndexStore_InstanceMethod(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "HogeView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "greet", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 8},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "HogeView", Line: 8, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD, Name: "greet", Selector: "greet(name:)", Signature: "(name: String) -> String", BodyLine: 9, Source: `return "Hello"`},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Name != "greet" {
		t.Errorf("Method.Name = %q, want greet", m.Name)
	}
	if m.Selector != "greet(name:)" {
		t.Errorf("Method.Selector = %q, want greet(name:)", m.Selector)
	}
	if m.Signature != "(name: String) -> String" {
		t.Errorf("Method.Signature = %q, want (name: String) -> String", m.Signature)
	}
}

func TestCombineWithIndexStore_SkipsStoredProperty(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "MyView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "count", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: false, Line: 3},
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 5},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "MyView", Line: 5, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", TypeExpr: "some View", BodyLine: 6, Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	// Only body (computed) should be included, count (stored) should be skipped.
	if len(types[0].Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1 (stored property should be skipped)", len(types[0].Properties))
	}
	if types[0].Properties[0].Name != "body" {
		t.Errorf("Property name = %q, want body", types[0].Properties[0].Name)
	}
}

func TestCombineWithIndexStore_SkipsStaticMember(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "MyView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "create", Kind: pb.MemberKind_MEMBER_KIND_STATIC_METHOD, Line: 3},
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 5},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "MyView", Line: 3, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD, Name: "create", Source: "MyView()"},
			{TypeName: "MyView", Line: 5, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", TypeExpr: "some View", Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	// Static method should be skipped.
	if len(types[0].Methods) != 0 {
		t.Errorf("Methods count = %d, want 0 (static should be skipped)", len(types[0].Methods))
	}
}

func TestCombineWithIndexStore_SkipsConstructor(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "MyView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "init", Kind: pb.MemberKind_MEMBER_KIND_CONSTRUCTOR, Line: 3},
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 5},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "MyView", Line: 3, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD, Name: "init", Source: "self.x = 0"},
			{TypeName: "MyView", Line: 5, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", TypeExpr: "some View", Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	// init should be skipped.
	if len(types[0].Methods) != 0 {
		t.Errorf("Methods count = %d, want 0 (constructor should be skipped)", len(types[0].Methods))
	}
}

func TestCombineWithIndexStore_NameFallback(t *testing.T) {
	// Line number mismatch — should fall back to name-only lookup.
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "MyView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 10},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			// Line 5 from parser doesn't match line 10 from index store.
			{TypeName: "MyView", Line: 5, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", TypeExpr: "some View", BodyLine: 6, Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if len(types[0].Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1 (should fallback to name match)", len(types[0].Properties))
	}
}

func TestCombineWithIndexStore_MultipleTypes(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:           "HogeView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
			{
				Name:           "FugaView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 10},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "HogeView", Line: 4, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", Source: "Text(\"Hello\")"},
			{TypeName: "FugaView", Line: 10, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", Source: "Text(\"World\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 2 {
		t.Fatalf("types count = %d, want 2", len(types))
	}
	if types[0].Name != "HogeView" {
		t.Errorf("types[0].Name = %q, want HogeView", types[0].Name)
	}
	if types[1].Name != "FugaView" {
		t.Errorf("types[1].Name = %q, want FugaView", types[1].Name)
	}
}

func TestCombineWithIndexStore_NoMemberSource(t *testing.T) {
	// Index Store says there's a computed property, but parser didn't extract source.
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "MyView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: nil, // No member sources from parser.
	}

	types := combineWithIndexStore(indexData, parserResult)
	// Type has no properties (no matching source), so it should be empty.
	if len(types) != 0 {
		t.Errorf("types count = %d, want 0 (no matching member source)", len(types))
	}
}

func TestCombineWithIndexStore_InheritedTypes(t *testing.T) {
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:           "MyView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "public",
				InheritedTypes: []string{"View", "Identifiable"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "MyView", Line: 4, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", Source: "Text(\"Hello\")"},
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if !types[0].IsView() {
		t.Error("expected IsView() to be true")
	}
	if types[0].AccessLevel != "public" {
		t.Errorf("AccessLevel = %q, want public", types[0].AccessLevel)
	}
	if len(types[0].InheritedTypes) != 2 {
		t.Errorf("InheritedTypes = %v, want [View Identifiable]", types[0].InheritedTypes)
	}
}

func TestCombineWithIndexStore_ParserAccessLevelOverridesIndexStore(t *testing.T) {
	// Index Store reports "internal" (broken), but parser knows the type is "private".
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "DataFormatter",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal", // Index Store always reports this
				Members: []*pb.IndexMemberInfo{
					{Name: "format", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 3},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "DataFormatter", Line: 3, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_METHOD, Name: "format", Source: `"formatted"`},
		},
		TypeAccessLevels: map[string]string{
			"DataFormatter": "private",
		},
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if types[0].AccessLevel != "private" {
		t.Errorf("AccessLevel = %q, want %q (parser should override Index Store)", types[0].AccessLevel, "private")
	}
}

func TestCombineWithIndexStore_FallbackToIndexStoreAccessLevel(t *testing.T) {
	// When parser doesn't have access level info, fall back to Index Store's value.
	indexData := &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "SomeView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "public",
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
		},
	}
	parserResult := &pb.ParseResult{
		MemberSources: []*pb.MemberSource{
			{TypeName: "SomeView", Line: 4, Kind: pb.MemberSourceKind_MEMBER_SOURCE_KIND_PROPERTY, Name: "body", Source: "Text(\"\")"},
		},
		// No TypeAccessLevels — fallback to Index Store.
	}

	types := combineWithIndexStore(indexData, parserResult)
	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if types[0].AccessLevel != "public" {
		t.Errorf("AccessLevel = %q, want %q (should fallback to Index Store)", types[0].AccessLevel, "public")
	}
}

// --- SourceFile / DependencyFile integration tests ---
// These require the axe-parser binary. They are integration tests that actually
// parse Swift files and combine with a mock IndexStoreCache.

// buildMockCache creates an IndexStoreCache with the given file data.
func buildMockCache(path string, indexData *pb.IndexFileData) *IndexStoreCache {
	return &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			path: indexData,
		},
		typeMap: map[string][]string{},
	}
}

func TestParseSourceFile(t *testing.T) {
	src := `import SwiftUI
import SomeFramework

struct HogeView: View {
    var body: some View {
        Map()
            .edgesIgnoringSafeArea(.all)
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:           "HogeView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 5},
				},
			},
		},
	})

	types, imports, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	ti := types[0]
	if ti.Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", ti.Name)
	}
	if len(ti.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(ti.Properties))
	}

	body := ti.Properties[0]
	if body.Name != "body" {
		t.Errorf("Property name = %q, want body", body.Name)
	}
	if len(imports) != 1 || imports[0] != "import SomeFramework" {
		t.Errorf("Imports = %v, want [import SomeFramework]", imports)
	}
}

func TestParseSourceFile_NoView(t *testing.T) {
	src := `import SwiftUI

struct NotAView {
    var name: String
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "NotAView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// No View conformance in index data.
	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:        "NotAView",
				Kind:        pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel: "internal",
			},
		},
	})

	_, _, err := SourceFile(path, cache)
	if err == nil {
		t.Fatal("expected error for file without View struct")
	}
}

func TestParseSourceFile_NilCache(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// With nil cache, SourceFile can't find types → error.
	_, _, err := SourceFile(path, nil)
	if err == nil {
		t.Fatal("expected error when cache is nil")
	}
}

func TestParseSourceFile_MultipleProperties(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var backgroundColor: Color {
        Color.blue
    }
    var body: some View {
        Text("Hello")
            .background(backgroundColor)
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name:           "HogeView",
				Kind:           pb.TypeKind_TYPE_KIND_STRUCT,
				AccessLevel:    "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "backgroundColor", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 7},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	ti := types[0]
	if ti.Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", ti.Name)
	}
	if len(ti.Properties) != 2 {
		t.Fatalf("Properties count = %d, want 2", len(ti.Properties))
	}
	if ti.Properties[0].Name != "backgroundColor" {
		t.Errorf("Properties[0].Name = %q, want backgroundColor", ti.Properties[0].Name)
	}
	if ti.Properties[1].Name != "body" {
		t.Errorf("Properties[1].Name = %q, want body", ti.Properties[1].Name)
	}
}

func TestParseSourceFile_MultipleViews(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        TextField("", text: .constant(""))
    }
}

struct FugaView: View {
    var body: some View {
        SecureField("", text: .constant(""))
    }
}

struct PiyoView: View {
    var body: some View {
        Text("Pick")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "Views.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members:        []*pb.IndexMemberInfo{{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4}},
			},
			{
				Name: "FugaView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members:        []*pb.IndexMemberInfo{{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 10}},
			},
			{
				Name: "PiyoView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members:        []*pb.IndexMemberInfo{{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 16}},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 3 {
		t.Fatalf("types count = %d, want 3", len(types))
	}
	names := []string{types[0].Name, types[1].Name, types[2].Name}
	want := []string{"HogeView", "FugaView", "PiyoView"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("types[%d].Name = %q, want %q", i, n, want[i])
		}
	}
	// Each type should have a body property
	for i, ti := range types {
		if len(ti.Properties) != 1 || ti.Properties[0].Name != "body" {
			t.Errorf("types[%d] missing body property", i)
		}
	}
}

func TestParsePreviewBlocks(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := PreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 1 {
		t.Fatalf("blocks count = %d, want 1", len(blocks))
	}
	if blocks[0].StartLine != 9 {
		t.Errorf("StartLine = %d, want 9", blocks[0].StartLine)
	}
	if blocks[0].Title != "" {
		t.Errorf("Title = %q, want empty", blocks[0].Title)
	}
}

func TestParsePreviewBlocks_Named(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview("Light Mode") {
    MyView()
}

#Preview("Dark Mode") {
    MyView()
        .preferredColorScheme(.dark)
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := PreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 2 {
		t.Fatalf("blocks count = %d, want 2", len(blocks))
	}
	if blocks[0].Title != "Light Mode" {
		t.Errorf("blocks[0].Title = %q, want %q", blocks[0].Title, "Light Mode")
	}
	if blocks[1].Title != "Dark Mode" {
		t.Errorf("blocks[1].Title = %q, want %q", blocks[1].Title, "Dark Mode")
	}
	if !strings.Contains(blocks[1].Source, ".preferredColorScheme(.dark)") {
		t.Errorf("blocks[1].Source missing expected content: %q", blocks[1].Source)
	}
}

func TestParsePreviewBlocks_NamedWithTraits(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview("Landscape", traits: .landscapeLeft) {
    MyView()
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := PreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 1 {
		t.Fatalf("blocks count = %d, want 1", len(blocks))
	}
	if blocks[0].Title != "Landscape" {
		t.Errorf("Title = %q, want %q", blocks[0].Title, "Landscape")
	}
}

func TestSelectPreview_Empty(t *testing.T) {
	_, err := SelectPreview(nil, "")
	if err == nil {
		t.Fatal("expected error for empty blocks")
	}
}

func TestSelectPreview_DefaultFirst(t *testing.T) {
	blocks := []PreviewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
		{StartLine: 5, Title: "B", Source: "ViewB()"},
	}
	b, err := SelectPreview(blocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "A" {
		t.Errorf("Title = %q, want %q", b.Title, "A")
	}
}

func TestSelectPreview_ByIndex(t *testing.T) {
	blocks := []PreviewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
		{StartLine: 5, Title: "B", Source: "ViewB()"},
	}
	b, err := SelectPreview(blocks, "1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "B" {
		t.Errorf("Title = %q, want %q", b.Title, "B")
	}
}

func TestSelectPreview_ByTitle(t *testing.T) {
	blocks := []PreviewBlock{
		{StartLine: 1, Title: "Light", Source: "ViewA()"},
		{StartLine: 5, Title: "Dark", Source: "ViewB()"},
	}
	b, err := SelectPreview(blocks, "Dark")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Dark" {
		t.Errorf("Title = %q, want %q", b.Title, "Dark")
	}
}

func TestSelectPreview_IndexOutOfRange(t *testing.T) {
	blocks := []PreviewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
	}
	_, err := SelectPreview(blocks, "5")
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestSelectPreview_TitleNotFound(t *testing.T) {
	blocks := []PreviewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
	}
	_, err := SelectPreview(blocks, "NonExistent")
	if err == nil {
		t.Fatal("expected error for unknown title")
	}
}

func TestTransformPreviewBlock_NoPreviewable(t *testing.T) {
	pb := PreviewBlock{
		StartLine: 1,
		Source:    "    MyView()",
	}
	tp := TransformPreviewBlock(pb)
	if len(tp.Properties) != 0 {
		t.Errorf("Properties count = %d, want 0", len(tp.Properties))
	}
	if !strings.Contains(tp.BodySource, "MyView()") {
		t.Errorf("BodySource = %q, want to contain MyView()", tp.BodySource)
	}
}

func TestTransformPreviewBlock_WithState(t *testing.T) {
	pb := PreviewBlock{
		StartLine: 1,
		Source:    "    @Previewable @State var count = 0\n    HogeView(count: $count)",
	}
	tp := TransformPreviewBlock(pb)
	if len(tp.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var count = 0" {
		t.Errorf("Property Source = %q, want %q", tp.Properties[0].Source, "@State var count = 0")
	}
	if !strings.Contains(tp.BodySource, "HogeView(count: $count)") {
		t.Errorf("BodySource = %q, want to contain HogeView(count: $count)", tp.BodySource)
	}
}

func TestTransformPreviewBlock_BindingToState(t *testing.T) {
	pb := PreviewBlock{
		StartLine: 1,
		Source:    "    @Previewable @Binding var isOn: Bool\n    HogeView(isOn: $isOn)",
	}
	tp := TransformPreviewBlock(pb)
	if len(tp.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var isOn: Bool" {
		t.Errorf("Property Source = %q, want %q", tp.Properties[0].Source, "@State var isOn: Bool")
	}
}

func TestTransformPreviewBlock_MultiplePreviewables(t *testing.T) {
	pb := PreviewBlock{
		StartLine: 1,
		Source:    "    @Previewable @State var name = \"World\"\n    @Previewable @State var count = 42\n    MyView(name: name, count: count)",
	}
	tp := TransformPreviewBlock(pb)
	if len(tp.Properties) != 2 {
		t.Fatalf("Properties count = %d, want 2", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var name = \"World\"" {
		t.Errorf("Properties[0].Source = %q", tp.Properties[0].Source)
	}
	if tp.Properties[1].Source != "@State var count = 42" {
		t.Errorf("Properties[1].Source = %q", tp.Properties[1].Source)
	}
}

func TestParseSourceFile_WithMethods(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "greet", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 8},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	ti := types[0]
	if len(ti.Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(ti.Methods))
	}
	m := ti.Methods[0]
	if m.Name != "greet" {
		t.Errorf("Method.Name = %q, want greet", m.Name)
	}
	if m.Selector != "greet(name:)" {
		t.Errorf("Method.Selector = %q, want greet(name:)", m.Selector)
	}
	if m.Signature != "(name: String) -> String" {
		t.Errorf("Method.Signature = %q, want (name: String) -> String", m.Signature)
	}
	if !strings.Contains(m.Source, `return "Hello, \(name)"`) {
		t.Errorf("Method.Source = %q, want to contain return statement", m.Source)
	}
}

func TestParseSourceFile_SkipStaticMethod(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    static func create() -> HogeView {
        HogeView()
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "create", Kind: pb.MemberKind_MEMBER_KIND_STATIC_METHOD, Line: 8},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 0 {
		t.Errorf("Methods count = %d, want 0 (static should be skipped)", len(types[0].Methods))
	}
}

func TestParseSourceFile_MethodNoParams(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func refresh() {
        print("refreshing")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "refresh", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 8},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "refresh()" {
		t.Errorf("Selector = %q, want refresh()", m.Selector)
	}
	if m.Signature != "()" {
		t.Errorf("Signature = %q, want ()", m.Signature)
	}
}

func TestParseSourceFile_MethodUnderscoreLabel(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Calc")
    }

    func add(_ a: Int, _ b: Int) -> Int {
        a + b
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "add", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 8},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "add(_:_:)" {
		t.Errorf("Selector = %q, want add(_:_:)", m.Selector)
	}
}

func TestParseSourceFile_MultiLineSignature(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func configure(
        title: String,
        count: Int
    ) -> String {
        "\(title): \(count)"
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
					{Name: "configure", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_METHOD, Line: 8},
				},
			},
		},
	})

	types, _, err := SourceFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "configure(title:count:)" {
		t.Errorf("Selector = %q, want configure(title:count:)", m.Selector)
	}
	if !strings.Contains(m.Signature, "-> String") {
		t.Errorf("Signature = %q, want to contain -> String", m.Signature)
	}
}

// --- computeSkeleton tests ---

// writeTemp is a helper that writes content to a temp .swift file and returns its path.
// It also resets the parse cache to ensure the new content is re-parsed.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetCache()
	return path
}

func TestComputeSkeleton_BodyOnlyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("World")
            .foregroundColor(.red)
    }
}

#Preview {
    MyView()
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("body-only change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_ImportAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI
import SomeFramework

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("import addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_StoredPropertyAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    @State var count = 0

    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("stored property addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_StructAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI

struct FugaView: View {
    var body: some View {
        Image(systemName: "star")
    }
}

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("struct addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_PreviewBodyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
        .preferredColorScheme(.dark)
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("#Preview body change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_PreviewAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}

#Preview("Dark") {
    MyView()
        .preferredColorScheme(.dark)
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("#Preview addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_MethodBodyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hi, \(name)!"
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("method body change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_MethodSignatureChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String, loud: Bool) -> String {
        return "Hello, \(name)"
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := Skeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("method signature change should produce different skeleton hash")
	}
}

// --- parseDependencyFile tests ---

func TestParseDependencyFile_ViewFile(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Child")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetCache()

	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "HogeView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				InheritedTypes: []string{"View"},
				Members: []*pb.IndexMemberInfo{
					{Name: "body", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: true, Line: 4},
				},
			},
		},
	})

	types, _, err := DependencyFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if types[0].Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", types[0].Name)
	}
}

func TestParseDependencyFile_NonViewFile(t *testing.T) {
	// parseDependencyFile should not fail on files without View conformance.
	src := `import Foundation

struct NotAView {
    var name: String
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "NotAView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetCache()

	// No computed properties in index data.
	cache := buildMockCache(path, &pb.IndexFileData{
		Types: []*pb.IndexTypeInfo{
			{
				Name: "NotAView", Kind: pb.TypeKind_TYPE_KIND_STRUCT, AccessLevel: "internal",
				Members: []*pb.IndexMemberInfo{
					{Name: "name", Kind: pb.MemberKind_MEMBER_KIND_INSTANCE_PROPERTY, IsComputed: false, Line: 4},
				},
			},
		},
	})

	types, _, err := DependencyFile(path, cache)
	if err != nil {
		t.Fatal(err)
	}

	// No computed properties, so no types should be returned.
	if len(types) != 0 {
		t.Errorf("types count = %d, want 0 for struct without computed properties", len(types))
	}
}

func TestParseDependencyFile_NilCache(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ResetCache()

	// With nil cache, no types are produced (but no error either).
	types, _, err := DependencyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 0 {
		t.Errorf("types count = %d, want 0 with nil cache", len(types))
	}
}
