package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

func TestReplacementModuleName(t *testing.T) {
	tests := []struct {
		moduleName     string
		sourceFileName string
		counter        int
		want           string
	}{
		{"MyModule", "ContentView.swift", 0, "MyModule_PreviewReplacement_ContentView_0"},
		{"MyApp", "HomeView.swift", 3, "MyApp_PreviewReplacement_HomeView_3"},
		// Should use only the base name, not subdirectory path
		{"App", "Views/HogeView.swift", 1, "App_PreviewReplacement_HogeView_1"},
	}
	for _, tt := range tests {
		got := ReplacementModuleName(tt.moduleName, tt.sourceFileName, tt.counter)
		if got != tt.want {
			t.Errorf("ReplacementModuleName(%q, %q, %d) = %q, want %q",
				tt.moduleName, tt.sourceFileName, tt.counter, got, tt.want)
		}
	}
}

func TestFilterPrivateCollisions_NoCollision(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []analysis.TypeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_CollisionBetweenDeps(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []analysis.TypeInfo{
				{Name: "DepAView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "SharedHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []analysis.TypeInfo{
				{Name: "DepBView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "SharedHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	// Type-level filtering: only SharedHelper is removed, not the entire file.
	if len(kept) != 3 {
		t.Errorf("kept = %d, want 3 (all files kept, only colliding types removed)", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty (files not excluded, types pruned)", excluded)
	}
	// Verify that the colliding type was removed from deps.
	for _, f := range kept {
		if f.AbsPath == "/path/Target.swift" {
			continue
		}
		for _, typ := range f.Types {
			if typ.Name == "SharedHelper" {
				t.Errorf("SharedHelper should have been removed from %s", f.AbsPath)
			}
		}
		if len(f.Types) != 1 {
			t.Errorf("%s types = %d, want 1 (only non-colliding type)", f.AbsPath, len(f.Types))
		}
	}
}

func TestFilterPrivateCollisions_CollisionWithTarget(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "Helper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []analysis.TypeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "Helper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	// Colliding private types removed from ALL files (including target).
	// With -enable-private-imports, "extension Helper" is ambiguous.
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
	// Target should only have TargetView (Helper removed).
	if len(kept[0].Types) != 1 || kept[0].Types[0].Name != "TargetView" {
		t.Errorf("Target types = %v, want [TargetView]", kept[0].Types)
	}
	// Dep should only have DepView (Helper removed).
	if len(kept[1].Types) != 1 || kept[1].Types[0].Name != "DepView" {
		t.Errorf("Dep types = %v, want [DepView]", kept[1].Types)
	}
}

func TestFilterPrivateCollisions_NoPrivateTypes(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []analysis.TypeInfo{
				{Name: "ViewA", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []analysis.TypeInfo{
				{Name: "ViewB", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 3 {
		t.Errorf("kept = %d, want 3", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_PrivateButNoCollision(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "TargetHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []analysis.TypeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "DepHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2 (different private names → no collision)", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_FileprivateCollision(t *testing.T) {
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "MainView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []analysis.TypeInfo{
				{Name: "Label", Kind: "struct", AccessLevel: "fileprivate"},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []analysis.TypeInfo{
				{Name: "Label", Kind: "struct", AccessLevel: "fileprivate"},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 1 {
		t.Errorf("kept = %d, want 1", len(kept))
	}
	if len(excluded) != 2 {
		t.Errorf("excluded = %d, want 2 (fileprivate collision)", len(excluded))
	}
}

func TestFilterPrivateCollisions_TypeLevelPreservesNonColliding(t *testing.T) {
	// Regression test: private type collision should only remove the specific
	// colliding types, preserving other types in the same file.
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "ExportView.swift",
			AbsPath:  "/path/ExportView.swift",
			Types: []analysis.TypeInfo{
				{Name: "ExportView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "DataHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "ShareView.swift",
			AbsPath:  "/path/ShareView.swift",
			Types: []analysis.TypeInfo{
				{Name: "ShareView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "DataHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 3 {
		t.Fatalf("kept = %d, want 3 (all files kept)", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}

	// Verify ExportView.swift only has ExportView (DataHelper removed).
	for _, f := range kept {
		switch f.AbsPath {
		case "/path/ExportView.swift":
			if len(f.Types) != 1 || f.Types[0].Name != "ExportView" {
				t.Errorf("ExportView.swift types = %v, want [ExportView]", f.Types)
			}
		case "/path/ShareView.swift":
			if len(f.Types) != 1 || f.Types[0].Name != "ShareView" {
				t.Errorf("ShareView.swift types = %v, want [ShareView]", f.Types)
			}
		}
	}
}

func TestFilterPrivateCollisions_MemberReferencingCollidingType(t *testing.T) {
	// Regression test: even after removing colliding private type definitions,
	// members that reference those types (e.g. "var formatter: DataFormatter")
	// must also be removed. With -enable-private-imports, both private types
	// are visible, making type references ambiguous.
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "TargetView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"hello\")"},
					},
				},
			},
		},
		{
			FileName: "ExportView.swift",
			AbsPath:  "/path/ExportView.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "ExportView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"export\")"},
						{Name: "formatter", TypeExpr: "DataFormatter", Source: "DataFormatter()"},
					},
				},
				{Name: "DataFormatter", Kind: "struct", AccessLevel: "private",
					Properties: []analysis.PropertyInfo{
						{Name: "text", TypeExpr: "String", Source: "\"\""},
					},
				},
			},
		},
		{
			FileName: "ShareView.swift",
			AbsPath:  "/path/ShareView.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "ShareView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"share\")"},
						{Name: "formatter", TypeExpr: "DataFormatter", Source: "DataFormatter()"},
					},
				},
				{Name: "DataFormatter", Kind: "struct", AccessLevel: "private",
					Properties: []analysis.PropertyInfo{
						{Name: "text", TypeExpr: "String", Source: "\"\""},
					},
				},
			},
		},
	}

	kept, excluded := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
	if len(kept) != 3 {
		t.Fatalf("kept = %d, want 3", len(kept))
	}

	for _, f := range kept {
		switch f.AbsPath {
		case "/path/Target.swift":
			// Target should be unchanged.
			if len(f.Types) != 1 {
				t.Errorf("Target types = %d, want 1", len(f.Types))
			}
			if len(f.Types[0].Properties) != 1 || f.Types[0].Properties[0].Name != "body" {
				t.Errorf("Target properties = %v, want [body]", f.Types[0].Properties)
			}
		case "/path/ExportView.swift":
			// DataFormatter type removed (Phase 1).
			// ExportView.formatter removed (Phase 2: TypeExpr references DataFormatter).
			// ExportView.body kept.
			if len(f.Types) != 1 || f.Types[0].Name != "ExportView" {
				t.Errorf("ExportView.swift types = %v, want [ExportView]", f.Types)
			}
			if len(f.Types[0].Properties) != 1 || f.Types[0].Properties[0].Name != "body" {
				t.Errorf("ExportView properties = %v, want [body]", f.Types[0].Properties)
			}
		case "/path/ShareView.swift":
			if len(f.Types) != 1 || f.Types[0].Name != "ShareView" {
				t.Errorf("ShareView.swift types = %v, want [ShareView]", f.Types)
			}
			if len(f.Types[0].Properties) != 1 || f.Types[0].Properties[0].Name != "body" {
				t.Errorf("ShareView properties = %v, want [body]", f.Types[0].Properties)
			}
		}
	}
}

func TestFilterPrivateCollisions_MethodReferencingCollidingType(t *testing.T) {
	// Methods whose signature references a colliding type should also be filtered.
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "TargetView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"hi\")"},
					},
				},
			},
		},
		{
			FileName: "ViewA.swift",
			AbsPath:  "/path/ViewA.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "ViewA", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"a\")"},
					},
					Methods: []analysis.MethodInfo{
						{Name: "format", Signature: "(data: Formatter) -> String", Source: "\"\""},
						{Name: "greet", Signature: "() -> String", Source: "\"hello\""},
					},
				},
				{Name: "Formatter", Kind: "struct", AccessLevel: "private",
					Properties: []analysis.PropertyInfo{
						{Name: "value", TypeExpr: "String", Source: "\"\""},
					},
				},
			},
		},
		{
			FileName: "ViewB.swift",
			AbsPath:  "/path/ViewB.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "ViewB", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"b\")"},
					},
					Methods: []analysis.MethodInfo{
						{Name: "format", Signature: "(input: Formatter) -> Formatter", Source: "Formatter()"},
					},
				},
				{Name: "Formatter", Kind: "struct", AccessLevel: "private",
					Properties: []analysis.PropertyInfo{
						{Name: "value", TypeExpr: "String", Source: "\"\""},
					},
				},
			},
		},
	}

	kept, _ := analysis.FilterPrivateCollisions(files, "/path/Target.swift", nil)
	if len(kept) != 3 {
		t.Fatalf("kept = %d, want 3", len(kept))
	}

	for _, f := range kept {
		switch f.AbsPath {
		case "/path/ViewA.swift":
			if len(f.Types) != 1 || f.Types[0].Name != "ViewA" {
				t.Errorf("ViewA types = %v, want [ViewA]", f.Types)
			}
			// "format" removed (sig contains "Formatter"), "greet" kept.
			if len(f.Types[0].Methods) != 1 || f.Types[0].Methods[0].Name != "greet" {
				t.Errorf("ViewA methods = %v, want [greet]", f.Types[0].Methods)
			}
		case "/path/ViewB.swift":
			if len(f.Types) != 1 || f.Types[0].Name != "ViewB" {
				t.Errorf("ViewB types = %v, want [ViewB]", f.Types)
			}
			// "format" removed (sig contains "Formatter").
			if len(f.Types[0].Methods) != 0 {
				t.Errorf("ViewB methods = %v, want empty", f.Types[0].Methods)
			}
		}
	}
}

func TestFilterPrivateCollisions_AmbiguousNamesFromIndexStore(t *testing.T) {
	// When the colliding type is only in ONE tracked file but the Index Store
	// reports it as ambiguous (defined in another non-tracked file), the type
	// and referencing members must still be filtered.
	files := []analysis.FileThunkData{
		{
			FileName: "ExportView.swift",
			AbsPath:  "/path/ExportView.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "ExportView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"export\")"},
						{Name: "formatter", TypeExpr: "DataFormatter", Source: "DataFormatter()"},
					},
				},
				{
					Name: "DataFormatter", Kind: "struct", AccessLevel: "private",
					Properties: []analysis.PropertyInfo{
						{Name: "csvString", TypeExpr: "String", Source: "\"\""},
					},
				},
			},
		},
	}

	// Index Store reports DataFormatter is defined in multiple files
	// (including a non-tracked file like ShareView.swift).
	ambiguousNames := map[string]bool{"DataFormatter": true}

	kept, _ := analysis.FilterPrivateCollisions(files, "/path/ExportView.swift", ambiguousNames)
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}

	// DataFormatter type removed, ExportView.formatter removed.
	f := kept[0]
	if len(f.Types) != 1 || f.Types[0].Name != "ExportView" {
		t.Errorf("types = %v, want [ExportView]", f.Types)
	}
	if len(f.Types[0].Properties) != 1 || f.Types[0].Properties[0].Name != "body" {
		t.Errorf("properties = %v, want [body]", f.Types[0].Properties)
	}
}

func TestFilterPrivateCollisions_WordBoundaryMatching(t *testing.T) {
	// Verify that word-boundary matching prevents false positives where
	// a colliding type name is a substring of an unrelated identifier.
	// e.g. "Item" as a colliding name should NOT match "isItemizable" or "MenuItem".
	files := []analysis.FileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []analysis.TypeInfo{
				{
					Name: "TargetView", Kind: "struct", AccessLevel: "internal",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", Source: "Text(\"hi\")"},
						{Name: "menuItem", TypeExpr: "MenuItem", Source: "MenuItem()"},
						{Name: "isItemizable", TypeExpr: "Bool", Source: "true"},
						{Name: "item", TypeExpr: "Item", Source: "Item()"},
						{Name: "items", TypeExpr: "[Item]", Source: "[]"},
					},
					Methods: []analysis.MethodInfo{
						{Name: "processItem", Signature: "(item: Item) -> String", Source: "\"\""},
						{Name: "getMenuItems", Signature: "() -> [MenuItem]", Source: "[]"},
					},
				},
			},
		},
	}

	ambiguousNames := map[string]bool{"Item": true}
	kept, _ := analysis.FilterPrivateCollisions(files, "/path/Target.swift", ambiguousNames)
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}

	tv := kept[0].Types[0]

	// "body" kept (no reference to Item).
	// "menuItem" kept (MenuItem ≠ Item — different word boundary).
	// "isItemizable" kept (Bool type, no reference).
	// "item" removed (TypeExpr is exactly "Item").
	// "items" removed (TypeExpr "[Item]" contains word "Item").
	var propNames []string
	for _, p := range tv.Properties {
		propNames = append(propNames, p.Name)
	}
	wantProps := []string{"body", "menuItem", "isItemizable"}
	if len(propNames) != len(wantProps) {
		t.Fatalf("properties = %v, want %v", propNames, wantProps)
	}
	for i, name := range wantProps {
		if propNames[i] != name {
			t.Errorf("property[%d] = %q, want %q", i, propNames[i], name)
		}
	}

	// "processItem" removed (signature contains word "Item").
	// "getMenuItems" kept (MenuItem ≠ Item — different word boundary).
	var methodNames []string
	for _, m := range tv.Methods {
		methodNames = append(methodNames, m.Name)
	}
	wantMethods := []string{"getMenuItems"}
	if len(methodNames) != len(wantMethods) {
		t.Fatalf("methods = %v, want %v", methodNames, wantMethods)
	}
	for i, name := range wantMethods {
		if methodNames[i] != name {
			t.Errorf("method[%d] = %q, want %q", i, methodNames[i], name)
		}
	}
}

func TestGenerateCombinedThunk_MultiFile(t *testing.T) {
	dir := t.TempDir()
	thunkDir := filepath.Join(dir, "thunk")

	// Create target source file with #Preview
	targetContent := `import SwiftUI

struct HogeView: View {
    var body: some View {
        FugaView()
    }
}

#Preview {
    HogeView()
}
`
	targetPath := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(targetPath, []byte(targetContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create dependency source file
	depContent := `import SwiftUI

struct FugaView: View {
    var body: some View {
        Text("Child")
    }
}
`
	depPath := filepath.Join(dir, "FugaView.swift")
	if err := os.WriteFile(depPath, []byte(depContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []analysis.FileThunkData{
		{
			FileName: "HogeView.swift",
			AbsPath:  targetPath,
			Types: []analysis.TypeInfo{
				{
					Name:           "HogeView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        FugaView()"},
					},
				},
			},
		},
		{
			FileName: "FugaView.swift",
			AbsPath:  depPath,
			Types: []analysis.TypeInfo{
				{
					Name:           "FugaView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Child\")"},
					},
				},
			},
		},
	}

	thunkPath, err := GenerateCombinedThunk(files, "MyApp", thunkDir, "", targetPath)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(thunkPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	checks := []string{
		// Per-file @_private imports
		`@_private(sourceFile: "HogeView.swift") import MyApp`,
		`@_private(sourceFile: "FugaView.swift") import MyApp`,
		`import SwiftUI`,
		// Both extensions should be present
		`extension HogeView {`,
		`extension FugaView {`,
		// Dynamic replacements for both types
		`@_dynamicReplacement(for: body) private var __preview__body: some View {`,
		// #sourceLocation should point to correct file for each type
		`#sourceLocation(file: "` + targetPath + `"`,
		`#sourceLocation(file: "` + depPath + `"`,
		// Preview wrapper should only import target view
		`import struct MyApp.HogeView`,
		`struct _AxePreviewWrapper: View {`,
		`HogeView()`,
		`@_cdecl("axe_preview_refresh")`,
	}

	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("combined thunk missing %q\n\nGot:\n%s", c, content)
		}
	}

	// Verify FugaView is NOT in the preview wrapper imports
	// (it's a dependency, not the target file)
	if strings.Contains(content, `import struct MyApp.FugaView`) {
		t.Errorf("combined thunk should not import FugaView for preview wrapper\n\nGot:\n%s", content)
	}
}

func TestGenerateCombinedThunk_SingleFile(t *testing.T) {
	// helper: create dirs and generate a combined thunk from a single fileThunkData.
	generate := func(t *testing.T, srcFileName, srcContent string, ftd analysis.FileThunkData, moduleName string) string {
		t.Helper()
		dir := t.TempDir()
		thunkDir := filepath.Join(dir, "thunk")

		srcPath := filepath.Join(dir, srcFileName)
		if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(srcPath, []byte(srcContent), 0o644); err != nil {
			t.Fatal(err)
		}

		ftd.AbsPath = srcPath
		files := []analysis.FileThunkData{ftd}

		thunkPath, err := GenerateCombinedThunk(files, moduleName, thunkDir, "", srcPath)
		if err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(thunkPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	t.Run("Basic", func(t *testing.T) {
		content := generate(t, "HogeView.swift",
			"import SwiftUI\nstruct HogeView: View {\n    var body: some View { Text(\"Hello\") }\n}\n",
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Hello\")\n            .padding()"},
						},
					},
				},
				Imports: []string{"import SomeFramework"},
			},
			"MyModule",
		)

		checks := []string{
			`@_private(sourceFile: "HogeView.swift") import MyModule`,
			`import SwiftUI`,
			`import SomeFramework`,
			`extension HogeView {`,
			`@_dynamicReplacement(for: body) private var __preview__body: some View {`,
			`#sourceLocation(file: "`,
			`Text("Hello")`,
			`.padding()`,
			`#sourceLocation()`,
		}
		for _, c := range checks {
			if !strings.Contains(content, c) {
				t.Errorf("thunk missing %q\n\nGot:\n%s", c, content)
			}
		}
	})

	t.Run("PathEscaping", func(t *testing.T) {
		dir := t.TempDir()
		thunkDir := filepath.Join(dir, "thunk")

		weirdDir := filepath.Join(dir, `path with "quotes"`)
		if err := os.MkdirAll(weirdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		srcPath := filepath.Join(weirdDir, `My\View.swift`)
		if err := os.WriteFile(srcPath, []byte("import SwiftUI\nstruct MyView: View {\n    var body: some View { Text(\"Hi\") }\n}\n#Preview { MyView() }\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		files := []analysis.FileThunkData{
			{
				FileName: `My\View.swift`,
				AbsPath:  srcPath,
				Types: []analysis.TypeInfo{
					{
						Name:           "MyView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 3, Source: "        Text(\"Hi\")"},
						},
					},
				},
			},
		}

		thunkPath, err := GenerateCombinedThunk(files, "MyApp", thunkDir, "", srcPath)
		if err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(thunkPath)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)

		if strings.Contains(content, `"quotes"`) {
			t.Errorf("thunk contains unescaped quotes in path\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `\"quotes\"`) {
			t.Errorf("thunk missing escaped quotes in path\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `My\\View.swift`) {
			t.Errorf("thunk missing escaped backslash in path\n\nGot:\n%s", content)
		}
	})

	t.Run("MultipleProperties", func(t *testing.T) {
		content := generate(t, "HogeView.swift",
			"import SwiftUI\nstruct HogeView: View {\n    var body: some View { Text(\"Hi\") }\n}\n",
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "backgroundColor", TypeExpr: "Color", BodyLine: 4, Source: "        Color.blue"},
							{Name: "body", TypeExpr: "some View", BodyLine: 7, Source: "        Text(\"Hello\")"},
						},
					},
				},
			},
			"MyApp",
		)

		if !strings.Contains(content, `@_dynamicReplacement(for: backgroundColor) private var __preview__backgroundColor: Color {`) {
			t.Errorf("thunk missing backgroundColor replacement\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `@_dynamicReplacement(for: body) private var __preview__body: some View {`) {
			t.Errorf("thunk missing body replacement\n\nGot:\n%s", content)
		}
	})

	t.Run("PreviewWrapper", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hello\")\n    }\n}\n\n#Preview {\n    @Previewable @State var someModel = SomeModel()\n    HogeView()\n        .environment(someModel)\n}\n"
		content := generate(t, "HogeView.swift", srcContent,
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Hello\")"},
						},
					},
				},
			},
			"MyModule",
		)

		checks := []string{
			`struct _AxePreviewWrapper: View {`,
			`@State var someModel = SomeModel()`,
			`var body: some View {`,
			`HogeView()`,
			`.environment(someModel)`,
			`UIHostingController(rootView: AnyView(_AxePreviewWrapper()))`,
			`window.rootViewController = hc`,
			`import UIKit`,
		}
		for _, c := range checks {
			if !strings.Contains(content, c) {
				t.Errorf("thunk missing %q\n\nGot:\n%s", c, content)
			}
		}
		if strings.Contains(content, "DebugReplaceableView") {
			t.Errorf("thunk should not contain DebugReplaceableView\n\nGot:\n%s", content)
		}
	})

	t.Run("PreviewBindingConversion", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    @Binding var isOn: Bool\n    var body: some View {\n        Toggle(\"Toggle\", isOn: $isOn)\n    }\n}\n\n#Preview {\n    @Previewable @Binding var isOn = true\n    HogeView(isOn: $isOn)\n}\n"
		content := generate(t, "HogeView.swift", srcContent,
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 6, Source: "        Toggle(\"Toggle\", isOn: $isOn)"},
						},
					},
				},
			},
			"MyApp",
		)

		if !strings.Contains(content, "@State var isOn = true") {
			t.Errorf("thunk should convert @Binding to @State\n\nGot:\n%s", content)
		}
		if strings.Contains(content, "@Binding") {
			t.Errorf("thunk should not contain @Binding\n\nGot:\n%s", content)
		}
	})

	t.Run("NoPreview", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hello\")\n    }\n}\n"
		content := generate(t, "HogeView.swift", srcContent,
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Hello\")"},
						},
					},
				},
			},
			"MyApp",
		)

		if strings.Contains(content, "_AxePreviewWrapper") {
			t.Errorf("thunk should not contain _AxePreviewWrapper without #Preview\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `@_cdecl("axe_preview_refresh")`) {
			t.Errorf("thunk missing axe_preview_refresh\n\nGot:\n%s", content)
		}
	})

	t.Run("WithMethods", func(t *testing.T) {
		content := generate(t, "HogeView.swift",
			"import SwiftUI\nstruct HogeView: View {\n    var body: some View { Text(\"Hi\") }\n    func greet(name: String) -> String { \"Hello\" }\n}\n",
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Hi\")"},
						},
						Methods: []analysis.MethodInfo{
							{
								Name:      "greet",
								Selector:  "greet(name:)",
								Signature: "(name: String) -> String",
								BodyLine:  8,
								Source:    "        return \"Hello, \\(name)\"",
							},
						},
					},
				},
			},
			"MyApp",
		)

		checks := []string{
			`@_dynamicReplacement(for: greet(name:))`,
			`private func __preview__greet(name: String) -> String {`,
			`#sourceLocation(file: "`,
			`return "Hello, \(name)"`,
			`#sourceLocation()`,
		}
		for _, c := range checks {
			if !strings.Contains(content, c) {
				t.Errorf("thunk missing %q\n\nGot:\n%s", c, content)
			}
		}
	})

	t.Run("MultipleViews", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        TextField(\"\", text: .constant(\"\"))\n    }\n}\n\nstruct FugaViewView: View {\n    var body: some View {\n        SecureField(\"\", text: .constant(\"\"))\n    }\n}\n\n#Preview(\"title\") {\n    HogeView()\n}\n"
		content := generate(t, "Views.swift", srcContent,
			analysis.FileThunkData{
				FileName: "Views.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        TextField(\"\", text: .constant(\"\"))"},
						},
					},
					{
						Name:           "FugaViewView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 11, Source: "        SecureField(\"\", text: .constant(\"\"))"},
						},
					},
				},
			},
			"MyApp",
		)

		checks := []string{
			`extension HogeView {`,
			`extension FugaViewView {`,
			`@_dynamicReplacement(for: body) private var __preview__body: some View {`,
			`TextField("", text: .constant(""))`,
			`SecureField("", text: .constant(""))`,
			`import struct MyApp.HogeView`,
			`import struct MyApp.FugaViewView`,
			`struct _AxePreviewWrapper: View {`,
		}
		for _, c := range checks {
			if !strings.Contains(content, c) {
				t.Errorf("thunk missing %q\n\nGot:\n%s", c, content)
			}
		}
	})

	t.Run("NestedViews", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct OuterView: View {\n    struct InnerView: View {\n        var body: some View {\n            Text(\"Inner\")\n        }\n    }\n    var body: some View {\n        InnerView()\n    }\n}\n\n#Preview {\n    OuterView()\n}\n"
		content := generate(t, "OuterView.swift", srcContent,
			analysis.FileThunkData{
				FileName: "OuterView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "OuterView.InnerView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 6, Source: "            Text(\"Inner\")"},
						},
					},
					{
						Name:           "OuterView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 11, Source: "        InnerView()"},
						},
					},
				},
			},
			"MyApp",
		)

		if !strings.Contains(content, `extension OuterView.InnerView {`) {
			t.Errorf("thunk missing nested extension\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `extension OuterView {`) {
			t.Errorf("thunk missing outer extension\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `import struct MyApp.OuterView`) {
			t.Errorf("thunk missing top-level import\n\nGot:\n%s", content)
		}
		if strings.Contains(content, `import struct MyApp.OuterView.InnerView`) {
			t.Errorf("thunk should not contain nested import struct\n\nGot:\n%s", content)
		}
	})

	t.Run("ExtraImports", func(t *testing.T) {
		content := generate(t, "HogeView.swift",
			"import SwiftUI\nimport SomeFramework\nstruct HogeView: View {\n    var body: some View { Text(\"Hello\") }\n}\n",
			analysis.FileThunkData{
				FileName: "HogeView.swift",
				Types: []analysis.TypeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []analysis.PropertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 4, Source: "        Text(\"Hello\")"},
						},
					},
				},
				Imports: []string{"import SomeFramework"},
			},
			"MyModule",
		)

		if !strings.Contains(content, `import SomeFramework`) {
			t.Errorf("thunk missing extra import from fileThunkData.Imports\n\nGot:\n%s", content)
		}
		if !strings.Contains(content, `@_private(sourceFile: "HogeView.swift") import MyModule`) {
			t.Errorf("thunk missing @_private import\n\nGot:\n%s", content)
		}
	})
}

func TestTypeInfo_IsView(t *testing.T) {
	tests := []struct {
		name string
		ti   analysis.TypeInfo
		want bool
	}{
		{"View conformance", analysis.TypeInfo{InheritedTypes: []string{"View"}}, true},
		{"SwiftUI.View conformance", analysis.TypeInfo{InheritedTypes: []string{"SwiftUI.View"}}, true},
		{"Multiple protocols with View", analysis.TypeInfo{InheritedTypes: []string{"Identifiable", "View"}}, true},
		{"No conformance", analysis.TypeInfo{InheritedTypes: []string{}}, false},
		{"Non-View protocol", analysis.TypeInfo{InheritedTypes: []string{"Codable"}}, false},
		{"Nil inherited types", analysis.TypeInfo{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ti.IsView(); got != tt.want {
				t.Errorf("isView() = %v, want %v", got, tt.want)
			}
		})
	}
}
