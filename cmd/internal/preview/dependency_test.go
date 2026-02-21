package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveDependencies_Basic(t *testing.T) {
	dir := t.TempDir()

	target := filepath.Join(dir, "ContentView.swift")
	dep := filepath.Join(dir, "ChildView.swift")
	unrelated := filepath.Join(dir, "AppDelegate.swift")

	if err := os.WriteFile(target, []byte(`import SwiftUI

struct ContentView: View {
    var child: ChildView

    var body: some View {
        child
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(dep, []byte(`import SwiftUI

struct ChildView: View {
    var body: some View {
        Text("Child")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(unrelated, []byte(`import UIKit

class AppDelegate: NSObject {
    func setup() {}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	gitInit(t, dir)
	resetParseCache()

	deps, err := resolveDependencies(target, dir)
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
	dir := t.TempDir()

	target := filepath.Join(dir, "Simple.swift")
	if err := os.WriteFile(target, []byte(`import SwiftUI

struct SimpleView: View {
    var body: some View {
        Text("Hello")
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	gitInit(t, dir)
	resetParseCache()

	deps, err := resolveDependencies(target, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0, got %v", len(deps), deps)
	}
}

func TestResolveDependencies_SelfDefinedType(t *testing.T) {
	dir := t.TempDir()

	target := filepath.Join(dir, "Combined.swift")
	if err := os.WriteFile(target, []byte(`import SwiftUI

struct MyModel {
    var name: String
}

struct CombinedView: View {
    var model: MyModel
    var body: some View {
        Text(model.name)
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	gitInit(t, dir)
	resetParseCache()

	deps, err := resolveDependencies(target, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps) != 0 {
		t.Errorf("deps count = %d, want 0 (self-defined type should not produce deps)", len(deps))
	}
}

// gitInit initializes a git repo and stages all files.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "add", "."},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
		}
	}
}
