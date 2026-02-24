package analysis

import (
	"path/filepath"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

func TestIndexStoreCache_ReferencedTypes(t *testing.T) {
	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			"/project/A.swift": {
				FilePath:            "/project/A.swift",
				ReferencedTypeNames: []string{"BType", "CType"},
				DefinedTypeNames:    []string{"AType"},
			},
		},
		typeMap: map[string][]string{},
	}

	refs := cache.ReferencedTypes("/project/A.swift")
	if len(refs) != 2 {
		t.Fatalf("ReferencedTypes count = %d, want 2", len(refs))
	}
	if refs[0] != "BType" || refs[1] != "CType" {
		t.Errorf("ReferencedTypes = %v, want [BType CType]", refs)
	}
}

func TestIndexStoreCache_DefinedTypes(t *testing.T) {
	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			"/project/A.swift": {
				FilePath:            "/project/A.swift",
				ReferencedTypeNames: []string{},
				DefinedTypeNames:    []string{"AType", "AHelper"},
			},
		},
		typeMap: map[string][]string{},
	}

	defs := cache.DefinedTypes("/project/A.swift")
	if len(defs) != 2 {
		t.Fatalf("DefinedTypes count = %d, want 2", len(defs))
	}
}

func TestIndexStoreCache_ReferencedTypes_UnknownFile(t *testing.T) {
	cache := &IndexStoreCache{
		files:   map[string]*pb.IndexFileData{},
		typeMap: map[string][]string{},
	}

	refs := cache.ReferencedTypes("/project/Unknown.swift")
	if refs != nil {
		t.Errorf("expected nil for unknown file, got %v", refs)
	}
}

func TestIndexStoreCache_TypeFileMultiMap(t *testing.T) {
	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{},
		typeMap: map[string][]string{
			"FooView":  {"/project/Foo.swift"},
			"BarModel": {"/project/Bar.swift"},
		},
	}

	tm := cache.TypeFileMultiMap()
	if len(tm) != 2 {
		t.Fatalf("TypeFileMultiMap size = %d, want 2", len(tm))
	}
	if tm["FooView"][0] != "/project/Foo.swift" {
		t.Errorf("FooView = %v, want /project/Foo.swift", tm["FooView"])
	}
}

func TestBuildTransitiveDeps_WithCache(t *testing.T) {
	// Test BFS using IndexStoreCache instead of SwiftFileParser.
	target := filepath.Join("/project", "ContentView.swift")
	dep := filepath.Join("/project", "ChildView.swift")

	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			target: {
				FilePath:            target,
				ReferencedTypeNames: []string{"ChildView"},
				DefinedTypeNames:    []string{"ContentView"},
			},
			dep: {
				FilePath:            dep,
				ReferencedTypeNames: nil,
				DefinedTypeNames:    []string{"ChildView"},
			},
		},
		typeMap: map[string][]string{
			"ContentView": {target},
			"ChildView":   {dep},
		},
	}

	graph, err := BuildTransitiveDeps(
		t.Context(), target, cache.TypeFileMultiMap(), cache,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[filepath.Clean(target)] {
		t.Error("graph should include target file")
	}
	if !graph.All[filepath.Clean(dep)] {
		t.Error("graph should include dependency file")
	}
	if len(graph.All) != 2 {
		t.Errorf("graph size = %d, want 2", len(graph.All))
	}
}

func TestBuildTransitiveDeps_CacheTransitive(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")

	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			a: {
				FilePath:            a,
				ReferencedTypeNames: []string{"BType"},
				DefinedTypeNames:    []string{"AType"},
			},
			b: {
				FilePath:            b,
				ReferencedTypeNames: []string{"CType"},
				DefinedTypeNames:    []string{"BType"},
			},
			c: {
				FilePath:            c,
				ReferencedTypeNames: nil,
				DefinedTypeNames:    []string{"CType"},
			},
		},
		typeMap: map[string][]string{
			"AType": {a},
			"BType": {b},
			"CType": {c},
		},
	}

	graph, err := BuildTransitiveDeps(
		t.Context(), a, cache.TypeFileMultiMap(), cache,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(graph.All) != 3 {
		t.Errorf("graph size = %d, want 3", len(graph.All))
	}

	direct := graph.DirectDeps()
	if len(direct) != 1 {
		t.Fatalf("DirectDeps count = %d, want 1, got %v", len(direct), direct)
	}
	if direct[0] != filepath.Clean(b) {
		t.Errorf("DirectDeps[0] = %q, want %q", direct[0], filepath.Clean(b))
	}
}
