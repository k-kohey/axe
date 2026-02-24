package analysis

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

// newTestCache creates an IndexStoreCache from a map of file→(referenced, defined) types.
func newTestCache(data map[string]struct {
	referenced []string
	defined    []string
}) *IndexStoreCache {
	files := make(map[string]*pb.IndexFileData, len(data))
	typeMap := make(map[string][]string)

	for path, d := range data {
		files[path] = &pb.IndexFileData{
			FilePath:            path,
			ReferencedTypeNames: d.referenced,
			DefinedTypeNames:    d.defined,
		}
		for _, def := range d.defined {
			typeMap[def] = append(typeMap[def], path)
		}
	}

	return &IndexStoreCache{
		files:   files,
		typeMap: typeMap,
	}
}

func TestBuildTransitiveDeps_SingleLevel(t *testing.T) {
	target := filepath.Join("/project", "ContentView.swift")
	dep := filepath.Join("/project", "ChildView.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target: {referenced: []string{"ChildView"}, defined: []string{"ContentView"}},
		dep:    {referenced: nil, defined: []string{"ChildView"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), target, cache.TypeFileMultiMap(), cache)
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

	direct := graph.DirectDeps()
	if len(direct) != 1 {
		t.Fatalf("DirectDeps count = %d, want 1, got %v", len(direct), direct)
	}
	if direct[0] != filepath.Clean(dep) {
		t.Errorf("DirectDeps[0] = %q, want %q", direct[0], filepath.Clean(dep))
	}
}

func TestBuildTransitiveDeps_Transitive(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"CType"}, defined: []string{"BType"}},
		c: {referenced: nil, defined: []string{"CType"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), a, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[filepath.Clean(a)] {
		t.Error("graph should include A")
	}
	if !graph.All[filepath.Clean(b)] {
		t.Error("graph should include B (direct dep)")
	}
	if !graph.All[filepath.Clean(c)] {
		t.Error("graph should include C (transitive dep)")
	}
	if len(graph.All) != 3 {
		t.Errorf("graph size = %d, want 3", len(graph.All))
	}

	direct := graph.DirectDeps()
	if len(direct) != 1 {
		t.Fatalf("DirectDeps count = %d, want 1 (only B), got %v", len(direct), direct)
	}
	if direct[0] != filepath.Clean(b) {
		t.Errorf("DirectDeps[0] = %q, want %q", direct[0], filepath.Clean(b))
	}
}

func TestBuildTransitiveDeps_Cycle(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"AType"}, defined: []string{"BType"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), a, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	// Should not infinite loop; both files should be in the graph.
	if len(graph.All) != 2 {
		t.Errorf("graph size = %d, want 2", len(graph.All))
	}
}

func TestBuildTransitiveDeps_NoRefs(t *testing.T) {
	target := filepath.Join("/project", "Simple.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target: {referenced: nil, defined: []string{"Simple"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), target, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(graph.All) != 1 {
		t.Errorf("graph size = %d, want 1 (target only)", len(graph.All))
	}

	if len(graph.DirectDeps()) != 0 {
		t.Errorf("DirectDeps count = %d, want 0", len(graph.DirectDeps()))
	}
}

func TestBuildTransitiveDeps_UnknownTypeSkipped(t *testing.T) {
	target := filepath.Join("/project", "View.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target: {referenced: []string{"UnknownFrameworkType"}, defined: []string{"MyView"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), target, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	// UnknownFrameworkType not in typeMap, so only target is in graph.
	if len(graph.All) != 1 {
		t.Errorf("graph size = %d, want 1", len(graph.All))
	}
}

func TestBuildTransitiveDeps_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	target := filepath.Join("/project", "View.swift")
	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target:                                   {referenced: []string{"Other"}, defined: []string{"View"}},
		filepath.Join("/project", "Other.swift"): {referenced: nil, defined: []string{"Other"}},
	})

	_, err := BuildTransitiveDeps(ctx, target, cache.TypeFileMultiMap(), cache)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestBuildTransitiveDeps_EmptyTypeMap(t *testing.T) {
	target := filepath.Join("/project", "Alone.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target: {referenced: []string{"SomeType"}, defined: []string{"AloneView"}},
	})

	// Use empty typeMap to simulate the type not being found.
	graph, err := BuildTransitiveDeps(context.Background(), target, map[string][]string{}, cache)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[filepath.Clean(target)] {
		t.Error("graph should include target file")
	}
	if len(graph.All) != 1 {
		t.Errorf("graph size = %d, want 1 (target only with empty typeMap)", len(graph.All))
	}
}

func TestBuildTransitiveDeps_NonCleanPaths(t *testing.T) {
	target := filepath.Join("/project", "src", "Root.swift")
	// typeMap entry uses a non-clean path with ".." segments.
	dep := filepath.Join("/project", "src", "..", "src", "Dep.swift")
	depClean := filepath.Clean(dep)

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target:   {referenced: []string{"DepType"}, defined: []string{"RootType"}},
		depClean: {referenced: nil, defined: []string{"DepType"}},
	})

	typeMap := map[string][]string{
		"RootType": {target},
		"DepType":  {dep}, // non-clean path
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, cache)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[depClean] {
		t.Errorf("graph should contain cleaned path %q", depClean)
	}
	// Ensure no duplicate entry from the non-clean path.
	if len(graph.All) != 2 {
		t.Errorf("graph size = %d, want 2 (target + deduplicated dep)", len(graph.All))
	}
}

func TestBuildTransitiveDeps_DuplicateTypeName(t *testing.T) {
	target := filepath.Join("/project", "View.swift")
	depA := filepath.Join("/project", "features", "Product.swift")
	depB := filepath.Join("/project", "legacy", "Product.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		target: {referenced: []string{"Product"}, defined: []string{"MyView"}},
		depA:   {referenced: nil, defined: []string{"Product"}},
		depB:   {referenced: nil, defined: []string{"Product"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), target, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[filepath.Clean(depA)] {
		t.Error("graph should include features/Product.swift")
	}
	if !graph.All[filepath.Clean(depB)] {
		t.Error("graph should include legacy/Product.swift")
	}
	if len(graph.All) != 3 {
		t.Errorf("graph size = %d, want 3 (target + 2 Product files)", len(graph.All))
	}

	direct := graph.DirectDeps()
	if len(direct) != 2 {
		t.Fatalf("DirectDeps count = %d, want 2, got %v", len(direct), direct)
	}
	sort.Strings(direct)
	expected := []string{filepath.Clean(depA), filepath.Clean(depB)}
	sort.Strings(expected)
	for i := range expected {
		if direct[i] != expected[i] {
			t.Errorf("DirectDeps[%d] = %q, want %q", i, direct[i], expected[i])
		}
	}
}

func TestBuildTransitiveDeps_DirectDepsMultiDepth(t *testing.T) {
	// A → B → C → D: only B should be a direct dependency of A.
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")
	d := filepath.Join("/project", "D.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"CType"}, defined: []string{"BType"}},
		c: {referenced: []string{"DType"}, defined: []string{"CType"}},
		d: {referenced: nil, defined: []string{"DType"}},
	})

	graph, err := BuildTransitiveDeps(context.Background(), a, cache.TypeFileMultiMap(), cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(graph.All) != 4 {
		t.Errorf("graph size = %d, want 4", len(graph.All))
	}

	direct := graph.DirectDeps()
	if len(direct) != 1 {
		t.Fatalf("DirectDeps count = %d, want 1, got %v", len(direct), direct)
	}
	if direct[0] != filepath.Clean(b) {
		t.Errorf("DirectDeps[0] = %q, want %q (only B is depth=1)", direct[0], filepath.Clean(b))
	}
}

func TestBuildTransitiveDeps_NilCache(t *testing.T) {
	target := filepath.Join("/project", "View.swift")
	typeMap := map[string][]string{
		"View":  {target},
		"Other": {filepath.Join("/project", "Other.swift")},
	}

	// With nil cache, no refs are found, so only the target is in the graph.
	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.All) != 1 {
		t.Errorf("graph size = %d, want 1 (nil cache returns no refs)", len(graph.All))
	}
}

func TestBuildTransitiveDeps_MidTraversalContextCancel(t *testing.T) {
	// Build a deep chain where context cancellation is detected during BFS.
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")

	cache := newTestCache(map[string]struct {
		referenced []string
		defined    []string
	}{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"CType"}, defined: []string{"BType"}},
		c: {referenced: nil, defined: []string{"CType"}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before traversal to ensure the context check triggers.
	cancel()

	_, err := BuildTransitiveDeps(ctx, a, cache.TypeFileMultiMap(), cache)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
