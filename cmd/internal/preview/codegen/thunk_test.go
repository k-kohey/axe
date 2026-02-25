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

func TestGenerateThunks_MultiFile(t *testing.T) {
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

	thunkPaths, err := GenerateThunks(files, "MyApp", thunkDir, "", targetPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Expect 3 files: per-file for HogeView, per-file for FugaView, main.
	if len(thunkPaths) != 3 {
		t.Fatalf("thunkPaths count = %d, want 3", len(thunkPaths))
	}

	// Read per-file thunks.
	hogeThunk, err := os.ReadFile(thunkPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	hogeContent := string(hogeThunk)

	fugaThunk, err := os.ReadFile(thunkPaths[1])
	if err != nil {
		t.Fatal(err)
	}
	fugaContent := string(fugaThunk)

	mainThunk, err := os.ReadFile(thunkPaths[2])
	if err != nil {
		t.Fatal(err)
	}
	mainContent := string(mainThunk)

	// Per-file thunk for HogeView should only have HogeView's @_private import.
	if !strings.Contains(hogeContent, `@_private(sourceFile: "HogeView.swift") import MyApp`) {
		t.Errorf("HogeView thunk missing its @_private import\n\nGot:\n%s", hogeContent)
	}
	if strings.Contains(hogeContent, `@_private(sourceFile: "FugaView.swift")`) {
		t.Errorf("HogeView thunk should NOT contain FugaView's @_private import\n\nGot:\n%s", hogeContent)
	}
	if !strings.Contains(hogeContent, `extension HogeView {`) {
		t.Errorf("HogeView thunk missing extension\n\nGot:\n%s", hogeContent)
	}
	if strings.Contains(hogeContent, `extension FugaView {`) {
		t.Errorf("HogeView thunk should NOT contain FugaView extension\n\nGot:\n%s", hogeContent)
	}

	// Per-file thunk for FugaView should only have FugaView's @_private import.
	if !strings.Contains(fugaContent, `@_private(sourceFile: "FugaView.swift") import MyApp`) {
		t.Errorf("FugaView thunk missing its @_private import\n\nGot:\n%s", fugaContent)
	}
	if strings.Contains(fugaContent, `@_private(sourceFile: "HogeView.swift")`) {
		t.Errorf("FugaView thunk should NOT contain HogeView's @_private import\n\nGot:\n%s", fugaContent)
	}
	if !strings.Contains(fugaContent, `extension FugaView {`) {
		t.Errorf("FugaView thunk missing extension\n\nGot:\n%s", fugaContent)
	}

	// Main thunk should contain preview wrapper and refresh, but no @_private.
	if strings.Contains(mainContent, `@_private`) {
		t.Errorf("main thunk should NOT contain @_private import\n\nGot:\n%s", mainContent)
	}
	if !strings.Contains(mainContent, `@testable import MyApp`) {
		t.Errorf("main thunk missing @testable import\n\nGot:\n%s", mainContent)
	}
	if !strings.Contains(mainContent, `struct _AxePreviewWrapper: View {`) {
		t.Errorf("main thunk missing _AxePreviewWrapper\n\nGot:\n%s", mainContent)
	}
	if !strings.Contains(mainContent, `@_cdecl("axe_preview_refresh")`) {
		t.Errorf("main thunk missing axe_preview_refresh\n\nGot:\n%s", mainContent)
	}
}

func TestGenerateThunks_SingleFile(t *testing.T) {
	// helper: create dirs and generate thunks from a single fileThunkData.
	generate := func(t *testing.T, srcFileName, srcContent string, ftd analysis.FileThunkData, moduleName string) (perFileContent string, mainContent string) {
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

		thunkPaths, err := GenerateThunks(files, moduleName, thunkDir, "", srcPath, 0)
		if err != nil {
			t.Fatal(err)
		}

		if len(thunkPaths) != 2 {
			t.Fatalf("thunkPaths count = %d, want 2 (per-file + main)", len(thunkPaths))
		}

		perFile, err := os.ReadFile(thunkPaths[0])
		if err != nil {
			t.Fatal(err)
		}
		main, err := os.ReadFile(thunkPaths[1])
		if err != nil {
			t.Fatal(err)
		}
		return string(perFile), string(main)
	}

	t.Run("Basic", func(t *testing.T) {
		perFile, _ := generate(t, "HogeView.swift",
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
			if !strings.Contains(perFile, c) {
				t.Errorf("per-file thunk missing %q\n\nGot:\n%s", c, perFile)
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

		thunkPaths, err := GenerateThunks(files, "MyApp", thunkDir, "", srcPath, 0)
		if err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(thunkPaths[0])
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
		perFile, _ := generate(t, "HogeView.swift",
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

		if !strings.Contains(perFile, `@_dynamicReplacement(for: backgroundColor) private var __preview__backgroundColor: Color {`) {
			t.Errorf("thunk missing backgroundColor replacement\n\nGot:\n%s", perFile)
		}
		if !strings.Contains(perFile, `@_dynamicReplacement(for: body) private var __preview__body: some View {`) {
			t.Errorf("thunk missing body replacement\n\nGot:\n%s", perFile)
		}
	})

	t.Run("PreviewWrapper", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hello\")\n    }\n}\n\n#Preview {\n    @Previewable @State var someModel = SomeModel()\n    HogeView()\n        .environment(someModel)\n}\n"
		_, mainContent := generate(t, "HogeView.swift", srcContent,
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
			if !strings.Contains(mainContent, c) {
				t.Errorf("main thunk missing %q\n\nGot:\n%s", c, mainContent)
			}
		}
		if strings.Contains(mainContent, "DebugReplaceableView") {
			t.Errorf("main thunk should not contain DebugReplaceableView\n\nGot:\n%s", mainContent)
		}
	})

	t.Run("PreviewBindingConversion", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    @Binding var isOn: Bool\n    var body: some View {\n        Toggle(\"Toggle\", isOn: $isOn)\n    }\n}\n\n#Preview {\n    @Previewable @Binding var isOn = true\n    HogeView(isOn: $isOn)\n}\n"
		_, mainContent := generate(t, "HogeView.swift", srcContent,
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

		if !strings.Contains(mainContent, "@State var isOn = true") {
			t.Errorf("main thunk should convert @Binding to @State\n\nGot:\n%s", mainContent)
		}
		if strings.Contains(mainContent, "@Binding") {
			t.Errorf("main thunk should not contain @Binding\n\nGot:\n%s", mainContent)
		}
	})

	t.Run("NoPreview", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hello\")\n    }\n}\n"
		_, mainContent := generate(t, "HogeView.swift", srcContent,
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

		if strings.Contains(mainContent, "_AxePreviewWrapper") {
			t.Errorf("main thunk should not contain _AxePreviewWrapper without #Preview\n\nGot:\n%s", mainContent)
		}
		if !strings.Contains(mainContent, `@_cdecl("axe_preview_refresh")`) {
			t.Errorf("main thunk missing axe_preview_refresh\n\nGot:\n%s", mainContent)
		}
	})

	t.Run("WithMethods", func(t *testing.T) {
		perFile, _ := generate(t, "HogeView.swift",
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
			if !strings.Contains(perFile, c) {
				t.Errorf("per-file thunk missing %q\n\nGot:\n%s", c, perFile)
			}
		}
	})

	t.Run("MultipleViews", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct HogeView: View {\n    var body: some View {\n        TextField(\"\", text: .constant(\"\"))\n    }\n}\n\nstruct FugaViewView: View {\n    var body: some View {\n        SecureField(\"\", text: .constant(\"\"))\n    }\n}\n\n#Preview(\"title\") {\n    HogeView()\n}\n"
		perFile, mainContent := generate(t, "Views.swift", srcContent,
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

		perFileChecks := []string{
			`extension HogeView {`,
			`extension FugaViewView {`,
			`@_dynamicReplacement(for: body) private var __preview__body: some View {`,
			`TextField("", text: .constant(""))`,
			`SecureField("", text: .constant(""))`,
		}
		for _, c := range perFileChecks {
			if !strings.Contains(perFile, c) {
				t.Errorf("per-file thunk missing %q\n\nGot:\n%s", c, perFile)
			}
		}

		mainChecks := []string{
			`@testable import MyApp`,
			`struct _AxePreviewWrapper: View {`,
		}
		for _, c := range mainChecks {
			if !strings.Contains(mainContent, c) {
				t.Errorf("main thunk missing %q\n\nGot:\n%s", c, mainContent)
			}
		}
	})

	t.Run("NestedViews", func(t *testing.T) {
		srcContent := "import SwiftUI\n\nstruct OuterView: View {\n    struct InnerView: View {\n        var body: some View {\n            Text(\"Inner\")\n        }\n    }\n    var body: some View {\n        InnerView()\n    }\n}\n\n#Preview {\n    OuterView()\n}\n"
		perFile, mainContent := generate(t, "OuterView.swift", srcContent,
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

		if !strings.Contains(perFile, `extension OuterView.InnerView {`) {
			t.Errorf("per-file thunk missing nested extension\n\nGot:\n%s", perFile)
		}
		if !strings.Contains(perFile, `extension OuterView {`) {
			t.Errorf("per-file thunk missing outer extension\n\nGot:\n%s", perFile)
		}
		if !strings.Contains(mainContent, `@testable import MyApp`) {
			t.Errorf("main thunk missing @testable import\n\nGot:\n%s", mainContent)
		}
	})

	t.Run("ExtraImports", func(t *testing.T) {
		perFile, _ := generate(t, "HogeView.swift",
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

		if !strings.Contains(perFile, `import SomeFramework`) {
			t.Errorf("per-file thunk missing extra import from fileThunkData.Imports\n\nGot:\n%s", perFile)
		}
		if !strings.Contains(perFile, `@_private(sourceFile: "HogeView.swift") import MyModule`) {
			t.Errorf("per-file thunk missing @_private import\n\nGot:\n%s", perFile)
		}
	})
}

func TestGenerateThunks_DuplicateBasenames(t *testing.T) {
	dir := t.TempDir()
	thunkDir := filepath.Join(dir, "thunk")

	// Two files with the same basename from different directories.
	dir1 := filepath.Join(dir, "a")
	dir2 := filepath.Join(dir, "b")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}

	srcContent := "import SwiftUI\nstruct V: View { var body: some View { Text(\"Hi\") } }\n"
	src1 := filepath.Join(dir1, "V.swift")
	src2 := filepath.Join(dir2, "V.swift")
	if err := os.WriteFile(src1, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []analysis.FileThunkData{
		{
			FileName: "V.swift",
			AbsPath:  src1,
			Types: []analysis.TypeInfo{
				{Name: "V1", Kind: "struct", InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{{Name: "body", TypeExpr: "some View", BodyLine: 2, Source: "Text(\"Hi\")"}}},
			},
		},
		{
			FileName: "V.swift",
			AbsPath:  src2,
			Types: []analysis.TypeInfo{
				{Name: "V2", Kind: "struct", InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{{Name: "body", TypeExpr: "some View", BodyLine: 2, Source: "Text(\"Hi\")"}}},
			},
		},
	}

	thunkPaths, err := GenerateThunks(files, "MyApp", thunkDir, "", src1, 0)
	if err != nil {
		t.Fatal(err)
	}

	// 2 per-file + 1 main = 3.
	if len(thunkPaths) != 3 {
		t.Fatalf("thunkPaths count = %d, want 3", len(thunkPaths))
	}

	// Verify the filenames are unique.
	names := make(map[string]bool)
	for _, p := range thunkPaths {
		base := filepath.Base(p)
		if names[base] {
			t.Errorf("duplicate thunk filename: %s", base)
		}
		names[base] = true
	}
}

func TestGenerateThunks_CrossModuleDependency(t *testing.T) {
	dir := t.TempDir()
	thunkDir := filepath.Join(dir, "thunk")

	// Target file in MainApp
	targetContent := "import SwiftUI\nstruct HogeView: View {\n    var body: some View { HelperView() }\n}\n#Preview { HogeView() }\n"
	targetPath := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(targetPath, []byte(targetContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dependency file in HelperLib (different module)
	depContent := "import SwiftUI\nstruct HelperView: View {\n    var body: some View { Text(\"Helper\") }\n}\n"
	depPath := filepath.Join(dir, "HelperView.swift")
	if err := os.WriteFile(depPath, []byte(depContent), 0o644); err != nil {
		t.Fatal(err)
	}

	files := []analysis.FileThunkData{
		{
			FileName: "HogeView.swift",
			AbsPath:  targetPath,
			// No ModuleName → falls back to global "MainApp"
			Types: []analysis.TypeInfo{
				{
					Name:           "HogeView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 3, Source: "        HelperView()"},
					},
				},
			},
		},
		{
			FileName:   "HelperView.swift",
			AbsPath:    depPath,
			ModuleName: "HelperLib", // Cross-module dependency
			Types: []analysis.TypeInfo{
				{
					Name:           "HelperView",
					Kind:           "struct",
					InheritedTypes: []string{"View"},
					Properties: []analysis.PropertyInfo{
						{Name: "body", TypeExpr: "some View", BodyLine: 3, Source: "        Text(\"Helper\")"},
					},
				},
			},
		},
	}

	thunkPaths, err := GenerateThunks(files, "MainApp", thunkDir, "", targetPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(thunkPaths) != 3 {
		t.Fatalf("thunkPaths count = %d, want 3", len(thunkPaths))
	}

	// Target file thunk should import MainApp (fallback from empty ModuleName).
	hogeThunk, err := os.ReadFile(thunkPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	hogeContent := string(hogeThunk)
	if !strings.Contains(hogeContent, `@_private(sourceFile: "HogeView.swift") import MainApp`) {
		t.Errorf("target thunk should import MainApp\n\nGot:\n%s", hogeContent)
	}

	// Cross-module dependency thunk should import HelperLib (not MainApp).
	helperThunk, err := os.ReadFile(thunkPaths[1])
	if err != nil {
		t.Fatal(err)
	}
	helperContent := string(helperThunk)
	if !strings.Contains(helperContent, `@_private(sourceFile: "HelperView.swift") import HelperLib`) {
		t.Errorf("cross-module dep thunk should import HelperLib\n\nGot:\n%s", helperContent)
	}
	if strings.Contains(helperContent, `import MainApp`) {
		t.Errorf("cross-module dep thunk should NOT import MainApp\n\nGot:\n%s", helperContent)
	}
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
