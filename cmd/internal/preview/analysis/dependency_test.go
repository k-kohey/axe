package analysis

import (
	"context"
	"path/filepath"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

func TestResolveTransitiveDependencies_Basic(t *testing.T) {
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

	graph, deps, err := ResolveTransitiveDependencies(context.Background(), target, cache)
	if err != nil {
		t.Fatal(err)
	}

	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1, got %v", len(deps), deps)
	}
	if filepath.Base(deps[0]) != "ChildView.swift" {
		t.Errorf("deps[0] = %q, want ChildView.swift", deps[0])
	}
}

func TestResolveTransitiveDependencies_NilCache(t *testing.T) {
	target := filepath.Join("/project", "View.swift")

	_, _, err := ResolveTransitiveDependencies(context.Background(), target, nil)
	if err == nil {
		t.Fatal("expected error for nil cache")
	}
}

func TestResolveTransitiveDependencies_NoRefs(t *testing.T) {
	target := filepath.Join("/project", "Simple.swift")

	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			target: {
				FilePath:            target,
				ReferencedTypeNames: nil,
				DefinedTypeNames:    []string{"SimpleView"},
			},
		},
		typeMap: map[string][]string{
			"SimpleView": {target},
		},
	}

	_, deps, err := ResolveTransitiveDependencies(context.Background(), target, cache)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0, got %v", len(deps), deps)
	}
}

func TestResolveTransitiveDependencies_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	target := filepath.Join("/project", "View.swift")

	cache := &IndexStoreCache{
		files: map[string]*pb.IndexFileData{
			target: {
				FilePath:            target,
				ReferencedTypeNames: []string{"Other"},
				DefinedTypeNames:    []string{"View"},
			},
		},
		typeMap: map[string][]string{
			"View":  {target},
			"Other": {filepath.Join("/project", "Other.swift")},
		},
	}

	_, _, err := ResolveTransitiveDependencies(ctx, target, cache)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
