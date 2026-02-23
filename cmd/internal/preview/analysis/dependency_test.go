package analysis

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// mockParser implements SwiftFileParser with predefined results per file.
type mockParser struct {
	results map[string]mockParseResult
}

type mockParseResult struct {
	referenced []string
	defined    []string
	err        error
}

func (m *mockParser) ParseTypes(path string) ([]string, []string, error) {
	r := m.results[path]
	return r.referenced, r.defined, r.err
}

// mockLister implements SwiftFileLister with a fixed file list.
type mockLister struct {
	files []string
	err   error
}

func (m *mockLister) SwiftFiles(_ context.Context, _ string) ([]string, error) {
	return m.files, m.err
}

func TestResolveDependencies_Basic(t *testing.T) {
	target := filepath.Join("/project", "ContentView.swift")
	dep := filepath.Join("/project", "ChildView.swift")
	unrelated := filepath.Join("/project", "AppDelegate.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target:    {referenced: []string{"ChildView"}, defined: []string{"ContentView"}},
		dep:       {referenced: nil, defined: []string{"ChildView"}},
		unrelated: {referenced: nil, defined: []string{"AppDelegate"}},
	}}
	lister := &mockLister{files: []string{target, dep, unrelated}}

	deps, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1, got %v", len(deps), deps)
	}
	if filepath.Base(deps[0]) != "ChildView.swift" {
		t.Errorf("deps[0] = %q, want ChildView.swift", deps[0])
	}
}

func TestResolveDependencies_NoRefs(t *testing.T) {
	target := filepath.Join("/project", "Simple.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: nil, defined: []string{"SimpleView"}},
	}}
	lister := &mockLister{files: []string{target}}

	deps, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0, got %v", len(deps), deps)
	}
}

func TestResolveDependencies_SelfDefinedType(t *testing.T) {
	target := filepath.Join("/project", "Combined.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"MyModel"}, defined: []string{"MyModel", "CombinedView"}},
	}}
	lister := &mockLister{files: []string{target}}

	deps, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0 (self-defined type should not produce deps)", len(deps))
	}
}

func TestResolveDependencies_ParserErrorOnTarget(t *testing.T) {
	target := filepath.Join("/project", "Bad.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {err: fmt.Errorf("syntax error")},
	}}
	lister := &mockLister{files: []string{target}}

	_, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveDependencies_ListerError(t *testing.T) {
	target := filepath.Join("/project", "View.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"Child"}, defined: []string{"View"}},
	}}
	lister := &mockLister{err: fmt.Errorf("git not available")}

	_, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveDependencies_ParserErrorOnCandidateSkips(t *testing.T) {
	target := filepath.Join("/project", "Root.swift")
	good := filepath.Join("/project", "Good.swift")
	bad := filepath.Join("/project", "Bad.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"GoodType"}, defined: []string{"Root"}},
		good:   {defined: []string{"GoodType"}},
		bad:    {err: fmt.Errorf("parse error")},
	}}
	lister := &mockLister{files: []string{target, bad, good}}

	deps, err := ResolveDependencies(context.Background(), target, "/project", lister, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1, got %v", len(deps), deps)
	}
	if filepath.Base(deps[0]) != "Good.swift" {
		t.Errorf("deps[0] = %q, want Good.swift", deps[0])
	}
}

func TestResolveDirectDepsFromTypeMap_Basic(t *testing.T) {
	target := filepath.Join("/project", "ContentView.swift")
	dep := filepath.Join("/project", "ChildView.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"ChildView", "Text"}, defined: []string{"ContentView"}},
	}}
	typeMap := map[string]string{
		"ContentView": target,
		"ChildView":   dep,
		// "Text" is not in typeMap (framework type), should be skipped.
	}

	deps, err := resolveDirectDepsFromTypeMap(target, typeMap, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1, got %v", len(deps), deps)
	}
	if filepath.Base(deps[0]) != "ChildView.swift" {
		t.Errorf("deps[0] = %q, want ChildView.swift", deps[0])
	}
}

func TestResolveDirectDepsFromTypeMap_SelfDefined(t *testing.T) {
	target := filepath.Join("/project", "Combined.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"MyModel"}, defined: []string{"MyModel", "CombinedView"}},
	}}
	typeMap := map[string]string{
		"MyModel":      target,
		"CombinedView": target,
	}

	deps, err := resolveDirectDepsFromTypeMap(target, typeMap, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0 (self-defined types should not produce deps)", len(deps))
	}
}

func TestResolveDirectDepsFromTypeMap_Deduplicates(t *testing.T) {
	target := filepath.Join("/project", "View.swift")
	dep := filepath.Join("/project", "Models.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"TypeA", "TypeB"}, defined: []string{"MyView"}},
	}}
	// Both types map to the same file.
	typeMap := map[string]string{
		"TypeA": dep,
		"TypeB": dep,
	}

	deps, err := resolveDirectDepsFromTypeMap(target, typeMap, parser)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1 (same file should be deduplicated), got %v", len(deps), deps)
	}
}

func TestResolveDependencies_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately before calling.

	target := filepath.Join("/project", "View.swift")
	other := filepath.Join("/project", "Other.swift")

	parser := &mockParser{results: map[string]mockParseResult{
		target: {referenced: []string{"OtherType"}, defined: []string{"ViewType"}},
		other:  {referenced: nil, defined: []string{"OtherType"}},
	}}
	lister := &mockLister{files: []string{target, other}}

	_, err := ResolveDependencies(ctx, target, "/project", lister, parser)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
