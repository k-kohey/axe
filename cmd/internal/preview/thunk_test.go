package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		got := replacementModuleName(tt.moduleName, tt.sourceFileName, tt.counter)
		if got != tt.want {
			t.Errorf("replacementModuleName(%q, %q, %d) = %q, want %q",
				tt.moduleName, tt.sourceFileName, tt.counter, got, tt.want)
		}
	}
}

func TestFilterPrivateCollisions_NoCollision(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []typeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_CollisionBetweenDeps(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []typeInfo{
				{Name: "DepAView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "SharedHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []typeInfo{
				{Name: "DepBView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "SharedHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	if len(kept) != 1 {
		t.Errorf("kept = %d, want 1 (target only)", len(kept))
	}
	if kept[0].AbsPath != "/path/Target.swift" {
		t.Errorf("kept[0] = %q, want Target.swift", kept[0].AbsPath)
	}
	if len(excluded) != 2 {
		t.Errorf("excluded = %d, want 2, got %v", len(excluded), excluded)
	}
}

func TestFilterPrivateCollisions_CollisionWithTarget(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "Helper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []typeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "Helper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	// Target should be kept, Dep should be excluded
	if len(kept) != 1 {
		t.Errorf("kept = %d, want 1", len(kept))
	}
	if kept[0].AbsPath != "/path/Target.swift" {
		t.Errorf("kept[0] = %q, want Target.swift", kept[0].AbsPath)
	}
	if len(excluded) != 1 || excluded[0] != "/path/Dep.swift" {
		t.Errorf("excluded = %v, want [/path/Dep.swift]", excluded)
	}
}

func TestFilterPrivateCollisions_NoPrivateTypes(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []typeInfo{
				{Name: "ViewA", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []typeInfo{
				{Name: "ViewB", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	if len(kept) != 3 {
		t.Errorf("kept = %d, want 3", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_PrivateButNoCollision(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "TargetView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "TargetHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
		{
			FileName: "Dep.swift",
			AbsPath:  "/path/Dep.swift",
			Types: []typeInfo{
				{Name: "DepView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
				{Name: "DepHelper", Kind: "struct", AccessLevel: "private"},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	if len(kept) != 2 {
		t.Errorf("kept = %d, want 2 (different private names → no collision)", len(kept))
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want empty", excluded)
	}
}

func TestFilterPrivateCollisions_FileprivateCollision(t *testing.T) {
	files := []fileThunkData{
		{
			FileName: "Target.swift",
			AbsPath:  "/path/Target.swift",
			Types: []typeInfo{
				{Name: "MainView", Kind: "struct", AccessLevel: "internal", InheritedTypes: []string{"View"}},
			},
		},
		{
			FileName: "DepA.swift",
			AbsPath:  "/path/DepA.swift",
			Types: []typeInfo{
				{Name: "Label", Kind: "struct", AccessLevel: "fileprivate"},
			},
		},
		{
			FileName: "DepB.swift",
			AbsPath:  "/path/DepB.swift",
			Types: []typeInfo{
				{Name: "Label", Kind: "struct", AccessLevel: "fileprivate"},
			},
		},
	}

	kept, excluded := filterPrivateCollisions(files, "/path/Target.swift")
	if len(kept) != 1 {
		t.Errorf("kept = %d, want 1", len(kept))
	}
	if len(excluded) != 2 {
		t.Errorf("excluded = %d, want 2 (fileprivate collision)", len(excluded))
	}
}

func TestGenerateCombinedThunk_MultiFile(t *testing.T) {
	dir := t.TempDir()
	dirs := previewDirs{
		Root:   dir,
		Build:  filepath.Join(dir, "build"),
		Thunk:  filepath.Join(dir, "thunk"),
		Loader: filepath.Join(dir, "loader"),
		Socket: filepath.Join(dir, "loader.sock"),
	}

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

	files := []fileThunkData{
		{
			FileName: "HogeView.swift",
			AbsPath:  targetPath,
			Types: []typeInfo{
				{
					Name:           "HogeView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []propertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        FugaView()"},
					},
				},
			},
		},
		{
			FileName: "FugaView.swift",
			AbsPath:  depPath,
			Types: []typeInfo{
				{
					Name:           "FugaView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []propertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Child\")"},
					},
				},
			},
		},
	}

	thunkPath, err := generateCombinedThunk(files, "MyApp", dirs, "", targetPath)
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
	generate := func(t *testing.T, srcFileName, srcContent string, ftd fileThunkData, moduleName string) string {
		t.Helper()
		dir := t.TempDir()
		dirs := previewDirs{
			Root:   dir,
			Build:  filepath.Join(dir, "build"),
			Thunk:  filepath.Join(dir, "thunk"),
			Loader: filepath.Join(dir, "loader"),
			Socket: filepath.Join(dir, "loader.sock"),
		}

		srcPath := filepath.Join(dir, srcFileName)
		if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(srcPath, []byte(srcContent), 0o644); err != nil {
			t.Fatal(err)
		}

		ftd.AbsPath = srcPath
		files := []fileThunkData{ftd}

		thunkPath, err := generateCombinedThunk(files, moduleName, dirs, "", srcPath)
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
		dirs := previewDirs{
			Root:   dir,
			Build:  filepath.Join(dir, "build"),
			Thunk:  filepath.Join(dir, "thunk"),
			Loader: filepath.Join(dir, "loader"),
			Socket: filepath.Join(dir, "loader.sock"),
		}

		weirdDir := filepath.Join(dir, `path with "quotes"`)
		if err := os.MkdirAll(weirdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		srcPath := filepath.Join(weirdDir, `My\View.swift`)
		if err := os.WriteFile(srcPath, []byte("import SwiftUI\nstruct MyView: View {\n    var body: some View { Text(\"Hi\") }\n}\n#Preview { MyView() }\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		files := []fileThunkData{
			{
				FileName: `My\View.swift`,
				AbsPath:  srcPath,
				Types: []typeInfo{
					{
						Name:           "MyView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 3, Source: "        Text(\"Hi\")"},
						},
					},
				},
			},
		}

		thunkPath, err := generateCombinedThunk(files, "MyApp", dirs, "", srcPath)
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        Text(\"Hi\")"},
						},
						Methods: []methodInfo{
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
			fileThunkData{
				FileName: "Views.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 5, Source: "        TextField(\"\", text: .constant(\"\"))"},
						},
					},
					{
						Name:           "FugaViewView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "OuterView.swift",
				Types: []typeInfo{
					{
						Name:           "OuterView.InnerView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
							{Name: "body", TypeExpr: "some View", BodyLine: 6, Source: "            Text(\"Inner\")"},
						},
					},
					{
						Name:           "OuterView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
			fileThunkData{
				FileName: "HogeView.swift",
				Types: []typeInfo{
					{
						Name:           "HogeView",
						Kind:           "struct",
						InheritedTypes: []string{"View"},
						Properties: []propertyInfo{
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
		ti   typeInfo
		want bool
	}{
		{"View conformance", typeInfo{InheritedTypes: []string{"View"}}, true},
		{"SwiftUI.View conformance", typeInfo{InheritedTypes: []string{"SwiftUI.View"}}, true},
		{"Multiple protocols with View", typeInfo{InheritedTypes: []string{"Identifiable", "View"}}, true},
		{"No conformance", typeInfo{InheritedTypes: []string{}}, false},
		{"Non-View protocol", typeInfo{InheritedTypes: []string{"Codable"}}, false},
		{"Nil inherited types", typeInfo{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ti.isView(); got != tt.want {
				t.Errorf("isView() = %v, want %v", got, tt.want)
			}
		})
	}
}
