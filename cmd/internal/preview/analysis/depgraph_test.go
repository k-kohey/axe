package analysis

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestBuildTransitiveDeps_SingleLevel(t *testing.T) {
	target := filepath.Join("/project", "ContentView.swift")
	dep := filepath.Join("/project", "ChildView.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"ChildView"}, defined: []string{"ContentView"}},
		dep:    {referenced: nil, defined: []string{"ChildView"}},
	}}
	typeMap := map[string][]string{
		"ContentView": {target},
		"ChildView":   {dep},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
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

func TestBuildTransitiveDeps_Transitive(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"CType"}, defined: []string{"BType"}},
		c: {referenced: nil, defined: []string{"CType"}},
	}}
	typeMap := map[string][]string{
		"AType": {a},
		"BType": {b},
		"CType": {c},
	}

	graph, err := BuildTransitiveDeps(context.Background(), a, typeMap, parser)
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
}

func TestBuildTransitiveDeps_Cycle(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"AType"}, defined: []string{"BType"}},
	}}
	typeMap := map[string][]string{
		"AType": {a},
		"BType": {b},
	}

	graph, err := BuildTransitiveDeps(context.Background(), a, typeMap, parser)
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

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: nil, defined: []string{"Simple"}},
	}}
	typeMap := map[string][]string{
		"Simple": {target},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(graph.All) != 1 {
		t.Errorf("graph size = %d, want 1 (target only)", len(graph.All))
	}
}

func TestBuildTransitiveDeps_UnknownTypeSkipped(t *testing.T) {
	target := filepath.Join("/project", "View.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"UnknownFrameworkType"}, defined: []string{"MyView"}},
	}}
	// typeMap doesn't contain UnknownFrameworkType — it's a framework type.
	typeMap := map[string][]string{
		"MyView": {target},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
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
	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"Other"}, defined: []string{"View"}},
	}}
	typeMap := map[string][]string{
		"View":  {target},
		"Other": {filepath.Join("/project", "Other.swift")},
	}

	_, err := BuildTransitiveDeps(ctx, target, typeMap, parser)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestBuildTransitiveDeps_EmptyTypeMap(t *testing.T) {
	target := filepath.Join("/project", "Alone.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"SomeType"}, defined: []string{"AloneView"}},
	}}
	typeMap := map[string][]string{}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
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

func TestBuildTransitiveDeps_ParseErrorOnDependency(t *testing.T) {
	target := filepath.Join("/project", "Root.swift")
	goodDep := filepath.Join("/project", "Good.swift")
	badDep := filepath.Join("/project", "Bad.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target:  {referenced: []string{"GoodType", "BadType"}, defined: []string{"RootType"}},
		goodDep: {referenced: nil, defined: []string{"GoodType"}},
		badDep:  {err: fmt.Errorf("syntax error in dependency")},
	}}
	typeMap := map[string][]string{
		"RootType": {target},
		"GoodType": {goodDep},
		"BadType":  {badDep},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.All[filepath.Clean(target)] {
		t.Error("graph should include target file")
	}
	if !graph.All[filepath.Clean(goodDep)] {
		t.Error("graph should include good dependency")
	}
	if !graph.All[filepath.Clean(badDep)] {
		t.Error("graph should include bad dependency path (added before parse attempt)")
	}
	if len(graph.All) != 3 {
		t.Errorf("graph size = %d, want 3", len(graph.All))
	}
}

// cancellingParser cancels the context on the Nth ParseTypes call.
type cancellingParser struct {
	inner     *mockParser
	cancel    context.CancelFunc
	callCount int
	cancelAt  int
}

func (p *cancellingParser) ParseTypes(path string) ([]string, []string, error) {
	p.callCount++
	if p.callCount >= p.cancelAt {
		p.cancel()
	}
	return p.inner.ParseTypes(path)
}

func TestBuildTransitiveDeps_MidTraversalContextCancel(t *testing.T) {
	a := filepath.Join("/project", "A.swift")
	b := filepath.Join("/project", "B.swift")
	c := filepath.Join("/project", "C.swift")

	inner := &mockParser{results: map[string]mockParseResult{
		a: {referenced: []string{"BType"}, defined: []string{"AType"}},
		b: {referenced: []string{"CType"}, defined: []string{"BType"}},
		c: {referenced: nil, defined: []string{"CType"}},
	}}
	typeMap := map[string][]string{
		"AType": {a},
		"BType": {b},
		"CType": {c},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel on the second call to ParseTypes (when processing B),
	// so C should not be discovered.
	parser := &cancellingParser{inner: inner, cancel: cancel, cancelAt: 2}

	_, err := BuildTransitiveDeps(ctx, a, typeMap, parser)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestBuildTransitiveDeps_NonCleanPaths(t *testing.T) {
	target := filepath.Join("/project", "src", "Root.swift")
	// typeMap entry uses a non-clean path with ".." segments.
	dep := filepath.Join("/project", "src", "..", "src", "Dep.swift")
	depClean := filepath.Clean(dep)

	parser := &mockParser{results: map[string]mockParseResult{
		target:   {referenced: []string{"DepType"}, defined: []string{"RootType"}},
		dep:      {referenced: []string{"DepType"}, defined: []string{"DepType"}},
		depClean: {referenced: nil, defined: []string{"DepType"}},
	}}
	typeMap := map[string][]string{
		"RootType": {target},
		"DepType":  {dep},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
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

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"Product"}, defined: []string{"MyView"}},
		depA:   {referenced: nil, defined: []string{"Product"}},
		depB:   {referenced: nil, defined: []string{"Product"}},
	}}
	// Same type name maps to multiple files.
	typeMap := map[string][]string{
		"MyView":  {target},
		"Product": {depA, depB},
	}

	graph, err := BuildTransitiveDeps(context.Background(), target, typeMap, parser)
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
}
