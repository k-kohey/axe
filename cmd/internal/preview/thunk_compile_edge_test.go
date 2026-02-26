package preview

// Thunk generation edge-case / limitation tests.
//
// This file documents what the thunk generator can and cannot handle.
// Each fixture is labelled "Edge" (expected to work) or "Limit" (known limitation).
// "Limit" tests verify the behaviour is correct (skipped gracefully, no crash)
// even when thunk generation cannot produce a dynamic replacement.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"github.com/k-kohey/axe/internal/preview/codegen"
)

// ============================================================
// Fixtures: edge cases that SHOULD work
// ============================================================

// Edge 1: async method
const fixtureAsyncMethod = `import SwiftUI

struct AsyncView: View {
    var body: some View {
        Text("Hello")
    }

    func fetchData() async -> String {
        "fetched"
    }
}
`

// Edge 2: throwing method
const fixtureThrowingMethod = `import SwiftUI

struct ThrowingView: View {
    var body: some View {
        Text("Hello")
    }

    func loadData() throws -> String {
        "loaded"
    }
}
`

// Edge 3: async throws method
const fixtureAsyncThrowsMethod = `import SwiftUI

struct AsyncThrowsView: View {
    var body: some View {
        Text("Hello")
    }

    func fetchOrFail() async throws -> String {
        "ok"
    }
}
`

// Edge 4: method with default parameter value
const fixtureDefaultParam = `import SwiftUI

struct DefaultParamView: View {
    var body: some View {
        Text("Hello")
    }

    func configure(animated: Bool = true, delay: Double = 0.3) {
        print("configure")
    }
}
`

// Edge 5: method with closure parameter
const fixtureClosureParam = `import SwiftUI

struct ClosureParamView: View {
    var body: some View {
        Text("Hello")
    }

    func onTap(action: @escaping () -> Void) {
        action()
    }
}
`

// Edge 6: method with inout parameter
const fixtureInoutParam = `import SwiftUI

struct InoutView: View {
    var body: some View {
        Text("Hello")
    }

    func increment(value: inout Int) {
        value += 1
    }
}
`

// Edge 7: class-based View (rare but valid)
const fixtureClassView = `import SwiftUI

class ClassBasedView: ObservableObject {
    var formatted: String {
        "class-based"
    }
}
`

// Edge 8: deeply nested types (3 levels)
const fixtureDeeplyNested = `import SwiftUI

struct Level1: View {
    struct Level2: View {
        struct Level3: View {
            var body: some View {
                Text("Deep")
            }
        }
        var body: some View {
            Level3()
        }
    }
    var body: some View {
        Level2()
    }
}
`

// Edge 9: multiple protocol conformance
const fixtureMultiProtocol = `import SwiftUI

struct MultiProtoView: View, Identifiable {
    let id = UUID()
    var body: some View {
        Text("Hello")
    }
}
`

// Edge 10: fileprivate computed property
const fixtureFileprivateProperty = `import SwiftUI

struct FileprivateView: View {
    fileprivate var theme: Color {
        Color.blue
    }
    var body: some View {
        Text("Hello")
            .foregroundColor(theme)
    }
}
`

// Edge 11: property returning optional type
const fixtureOptionalProperty = `import SwiftUI

struct OptionalView: View {
    var optionalText: String? {
        nil
    }
    var body: some View {
        Text(optionalText ?? "default")
    }
}
`

// Edge 12: method with variadic parameter
const fixtureVariadicParam = `import SwiftUI

struct VariadicView: View {
    var body: some View {
        Text("Hello")
    }

    func log(messages: String...) {
        for m in messages {
            print(m)
        }
    }
}
`

// Edge 13: method returning Void explicitly
const fixtureExplicitVoid = `import SwiftUI

struct VoidReturnView: View {
    var body: some View {
        Text("Hello")
    }

    func doWork() -> Void {
        print("working")
    }
}
`

// Edge 14: method with where clause (non-generic but constrained extension)
// Note: This is NOT a generic method. The generic constraint is on the extension.
const fixtureWhereClauseExtension = `import SwiftUI

struct WhereView: View {
    var body: some View {
        Text("Hello")
    }
}

extension WhereView {
    func detail() -> String {
        "detail"
    }
}
`

// Edge 15: multiple computed properties with different return types
const fixtureMixedReturnTypes = `import SwiftUI

struct MixedView: View {
    var count: Int {
        42
    }
    var label: String {
        "hello"
    }
    var color: Color {
        .blue
    }
    var body: some View {
        Text("\(label): \(count)")
            .foregroundColor(color)
    }
}
`

// Edge 16: #Preview with NavigationStack
const fixturePreviewNavigation = `import SwiftUI

struct NavView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    NavigationStack {
        NavView()
    }
}
`

// Edge 17: #Preview with .environment modifier
const fixturePreviewEnvironment = `import SwiftUI

struct EnvView: View {
    @Environment(\.colorScheme) var colorScheme
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    EnvView()
        .environment(\.colorScheme, .dark)
}
`

// Edge 18: method with tuple return
const fixtureTupleReturn = `import SwiftUI

struct TupleView: View {
    var body: some View {
        Text("Hello")
    }

    func pair() -> (Int, String) {
        (1, "one")
    }
}
`

// Edge 19: method with @discardableResult
const fixtureDiscardableResult = `import SwiftUI

struct DiscardableView: View {
    var body: some View {
        Text("Hello")
    }

    @discardableResult
    func perform() -> Bool {
        true
    }
}
`

// Edge 20: #Preview with multiple @Previewable including @Binding
const fixturePreviewMixedPreviewable = `import SwiftUI

struct MixedPreviewableView: View {
    @Binding var text: String
    var body: some View {
        Text(text)
    }
}

#Preview {
    @Previewable @State var count = 0
    @Previewable @Binding var text = "hello"
    MixedPreviewableView(text: $text)
}
`

// Edge 21: computed property with complex generic return type
const fixtureGenericReturnType = `import SwiftUI

struct GenericReturnView: View {
    var items: [String] {
        ["a", "b", "c"]
    }
    var body: some View {
        ForEach(items, id: \.self) { item in
            Text(item)
        }
    }
}
`

// Edge 22: method with labeled tuple parameter
const fixtureLabeledTupleParam = `import SwiftUI

struct LabeledTupleView: View {
    var body: some View {
        Text("Hello")
    }

    func process(point: (x: Int, y: Int)) -> String {
        "\(point.x), \(point.y)"
    }
}
`

// ============================================================
// Fixtures: known limitations
// ============================================================

// Limit 1: explicit get/set computed property.
// MemberSourceExtractor skips these because #sourceLocation breaks
// the Swift parser when wrapping accessor keywords.
const fixtureLimitGetSet = `import SwiftUI

struct GetSetView: View {
    private var _title: String = "Hello"
    var title: String {
        get { _title }
        set { _title = newValue }
    }
    var body: some View {
        Text(title)
    }
}
`

// Limit 2: generic method.
// @_dynamicReplacement does not support generic parameters.
const fixtureLimitGenericMethod = `import SwiftUI

struct GenericMethodView: View {
    var body: some View {
        Text("Hello")
    }

    func transform<T: CustomStringConvertible>(_ value: T) -> String {
        value.description
    }
}
`

// Limit 3: subscript.
// MemberSourceExtractor does not extract subscripts at all.
const fixtureLimitSubscript = `import SwiftUI

struct SubscriptView: View {
    private let data = ["a", "b", "c"]

    subscript(index: Int) -> String {
        data[index]
    }

    var body: some View {
        Text(self[0])
    }
}
`

// Limit 4: static computed property.
// Index Store filters out static members (only INSTANCE_PROPERTY/INSTANCE_METHOD pass).
const fixtureLimitStaticProperty = `import SwiftUI

struct StaticPropView: View {
    static var defaultTitle: String {
        "Default"
    }
    var body: some View {
        Text(StaticPropView.defaultTitle)
    }
}
`

// Limit 5: willSet/didSet observer property.
// These are stored properties with observers, not computed properties.
// Index Store reports them as non-computed, so they are filtered out.
const fixtureLimitWillSetDidSet = `import SwiftUI

class ObserverModel: ObservableObject {
    @Published var count: Int = 0 {
        willSet { print("will set to \(newValue)") }
        didSet { print("did set from \(oldValue)") }
    }
}

struct ObserverView: View {
    @StateObject var model = ObserverModel()
    var body: some View {
        Text("\(model.count)")
    }
}
`

// Limit 6: overloaded methods with the same name and argument labels.
// The fallback map only keeps the first occurrence per (typeName, name, kind).
// When line numbers don't match exactly, the wrong overload may be selected.
const fixtureLimitOverloadedMethods = `import SwiftUI

struct OverloadView: View {
    var body: some View {
        Text("Hello")
    }

    func process(value: Int) -> String {
        "int: \(value)"
    }

    func process(value: String) -> String {
        "string: \(value)"
    }
}
`

// Limit 7: @ViewBuilder annotated computed property.
// The parser sees this as a regular computed property, but the generated thunk
// may not propagate the @ViewBuilder attribute, leading to type mismatches
// when the body contains multiple statements that rely on ViewBuilder.
const fixtureLimitViewBuilder = `import SwiftUI

struct ViewBuilderView: View {
    @ViewBuilder
    var content: some View {
        Text("Line 1")
        Text("Line 2")
    }
    var body: some View {
        content
    }
}
`

// Limit 8: actor type.
// While actors are parsed, @_dynamicReplacement may not work correctly
// with actor isolation because replacements bypass isolation checks.
const fixtureLimitActorStore = `import Foundation

actor DataStore {
    var formatted: String {
        "stored"
    }

    func fetch() -> String {
        "fetched"
    }
}
`

const fixtureLimitActorView = `import SwiftUI

struct ActorView: View {
    var body: some View {
        Text("Hello")
    }
}
`

// Limit 9: generic initializer.
// @_dynamicReplacement does not support generic parameters, so generic inits are skipped.
const fixtureLimitGenericInit = `import SwiftUI

struct GenericInitView: View {
    let text: String

    init<S: StringProtocol>(content: S) {
        self.text = String(content)
    }

    var body: some View {
        Text(text)
    }
}
`

// Limit 10: free function (not in a type).
// MemberSourceExtractor only extracts members within type/extension contexts.
const fixtureLimitFreeFunction = `import SwiftUI

func globalHelper() -> String {
    "global"
}

struct FreeFuncView: View {
    var body: some View {
        Text(globalHelper())
    }
}
`

// Limit 11: multiple unnamed #Preview blocks.
// Only the first one is selected by default. Others are ignored unless
// explicitly selected by index.
const fixtureLimitMultipleUnnamedPreviews = `import SwiftUI

struct MultiPreviewView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MultiPreviewView()
        .padding()
}

#Preview {
    MultiPreviewView()
        .background(Color.red)
}
`

// Limit 12: property with explicit type annotation using generics.
// e.g. `var items: Array<String>` — the TypeExpr should be preserved correctly.
const fixtureLimitGenericTypeAnnotation = `import SwiftUI

struct GenericTypeView: View {
    var items: Array<String> {
        ["hello", "world"]
    }
    var body: some View {
        ForEach(items, id: \.self) { Text($0) }
    }
}
`

// ============================================================
// Test: compilation of edge cases
// ============================================================

func TestThunkCompilation_EdgeCases(t *testing.T) {
	sdk := simulatorSDKPath(t)

	tests := []struct {
		name    string
		sources map[string]string
		target  string
	}{
		{
			name:    "AsyncMethod",
			sources: map[string]string{"AsyncView.swift": fixtureAsyncMethod},
			target:  "AsyncView.swift",
		},
		{
			name:    "ThrowingMethod",
			sources: map[string]string{"ThrowingView.swift": fixtureThrowingMethod},
			target:  "ThrowingView.swift",
		},
		{
			name:    "AsyncThrowsMethod",
			sources: map[string]string{"AsyncThrowsView.swift": fixtureAsyncThrowsMethod},
			target:  "AsyncThrowsView.swift",
		},
		{
			name:    "DefaultParam",
			sources: map[string]string{"DefaultParamView.swift": fixtureDefaultParam},
			target:  "DefaultParamView.swift",
		},
		{
			name:    "ClosureParam",
			sources: map[string]string{"ClosureParamView.swift": fixtureClosureParam},
			target:  "ClosureParamView.swift",
		},
		{
			name:    "InoutParam",
			sources: map[string]string{"InoutView.swift": fixtureInoutParam},
			target:  "InoutView.swift",
		},
		{
			name:    "DeeplyNestedTypes",
			sources: map[string]string{"Level1.swift": fixtureDeeplyNested},
			target:  "Level1.swift",
		},
		{
			name:    "MultiProtocol",
			sources: map[string]string{"MultiProtoView.swift": fixtureMultiProtocol},
			target:  "MultiProtoView.swift",
		},
		{
			name:    "FileprivateProperty",
			sources: map[string]string{"FileprivateView.swift": fixtureFileprivateProperty},
			target:  "FileprivateView.swift",
		},
		{
			name:    "OptionalProperty",
			sources: map[string]string{"OptionalView.swift": fixtureOptionalProperty},
			target:  "OptionalView.swift",
		},
		{
			name:    "VariadicParam",
			sources: map[string]string{"VariadicView.swift": fixtureVariadicParam},
			target:  "VariadicView.swift",
		},
		{
			name:    "ExplicitVoidReturn",
			sources: map[string]string{"VoidReturnView.swift": fixtureExplicitVoid},
			target:  "VoidReturnView.swift",
		},
		{
			name:    "WhereClauseExtension",
			sources: map[string]string{"WhereView.swift": fixtureWhereClauseExtension},
			target:  "WhereView.swift",
		},
		{
			name:    "MixedReturnTypes",
			sources: map[string]string{"MixedView.swift": fixtureMixedReturnTypes},
			target:  "MixedView.swift",
		},
		{
			name:    "PreviewNavigation",
			sources: map[string]string{"NavView.swift": fixturePreviewNavigation},
			target:  "NavView.swift",
		},
		{
			name:    "PreviewEnvironment",
			sources: map[string]string{"EnvView.swift": fixturePreviewEnvironment},
			target:  "EnvView.swift",
		},
		{
			name:    "TupleReturn",
			sources: map[string]string{"TupleView.swift": fixtureTupleReturn},
			target:  "TupleView.swift",
		},
		{
			name:    "DiscardableResult",
			sources: map[string]string{"DiscardableView.swift": fixtureDiscardableResult},
			target:  "DiscardableView.swift",
		},
		{
			name:    "PreviewMixedPreviewable",
			sources: map[string]string{"MixedPreviewableView.swift": fixturePreviewMixedPreviewable},
			target:  "MixedPreviewableView.swift",
		},
		{
			name:    "GenericReturnType",
			sources: map[string]string{"GenericReturnView.swift": fixtureGenericReturnType},
			target:  "GenericReturnView.swift",
		},
		{
			name:    "LabeledTupleParam",
			sources: map[string]string{"LabeledTupleView.swift": fixtureLabeledTupleParam},
			target:  "LabeledTupleView.swift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runThunkCompileTest(t, sdk, tt.sources, tt.target)
		})
	}
}

// ============================================================
// Test: known limitations — verify graceful handling
// ============================================================

func TestThunkCompilation_Limitations(t *testing.T) {
	sdk := simulatorSDKPath(t)

	t.Run("GetSetProperty_SkipsAccessor", func(t *testing.T) {
		t.Parallel()
		// get/set properties are skipped by the parser. The body property
		// still gets a thunk, but the get/set 'title' property does not.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"GetSetView.swift": fixtureLimitGetSet},
			"GetSetView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__title") {
				t.Error("get/set property 'title' should NOT be in thunk (known limitation)")
			}
			if !strings.Contains(content, "__preview__body") {
				t.Error("body property should still be in thunk")
			}
		}
	})

	t.Run("GenericMethod_Skipped", func(t *testing.T) {
		t.Parallel()
		// Generic methods are skipped because @_dynamicReplacement
		// does not support generic parameters.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"GenericMethodView.swift": fixtureLimitGenericMethod},
			"GenericMethodView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__transform") {
				t.Error("generic method 'transform' should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("Subscript_NotExtracted", func(t *testing.T) {
		t.Parallel()
		// Subscripts are never extracted by MemberSourceExtractor.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"SubscriptView.swift": fixtureLimitSubscript},
			"SubscriptView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "subscript") {
				t.Error("subscripts should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("StaticProperty_FilteredOut", func(t *testing.T) {
		t.Parallel()
		// Static members are filtered out by combineWithIndexStore
		// (only INSTANCE_PROPERTY/INSTANCE_METHOD pass).
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"StaticPropView.swift": fixtureLimitStaticProperty},
			"StaticPropView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__defaultTitle") {
				t.Error("static property 'defaultTitle' should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("WillSetDidSet_NotComputed", func(t *testing.T) {
		t.Parallel()
		// willSet/didSet are stored property observers, not computed properties.
		// Index Store reports isComputed=false, so they are filtered out.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"ObserverView.swift": fixtureLimitWillSetDidSet},
			"ObserverView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__count") {
				t.Error("willSet/didSet property should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("OverloadedMethods_BothPresent", func(t *testing.T) {
		t.Parallel()
		// Overloaded methods with the same argument label (value:) may cause
		// the Index Store to report them as a single member, or the fallback
		// map may collapse them. This test documents the current behavior.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"OverloadView.swift": fixtureLimitOverloadedMethods},
			"OverloadView.swift",
		)
		foundProcess := 0
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			foundProcess += strings.Count(content, "__preview__process")
			t.Logf("Thunk content:\n%s", content)
		}
		// Known limitation: overloaded methods with the same argument labels
		// may not all get thunks. Log the count for documentation.
		t.Logf("Found %d __preview__process occurrences (2 expected for full support, 0-1 is known limitation)", foundProcess)
	})

	t.Run("ViewBuilder_Compiles", func(t *testing.T) {
		t.Parallel()
		// @ViewBuilder properties may or may not compile as thunks depending on
		// whether the replacement preserves the builder attribute.
		// This test documents the current behavior.
		runThunkCompileTest(t, sdk,
			map[string]string{"ViewBuilderView.swift": fixtureLimitViewBuilder},
			"ViewBuilderView.swift",
		)
	})

	t.Run("GenericInit_Skipped", func(t *testing.T) {
		t.Parallel()
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"GenericInitView.swift": fixtureLimitGenericInit},
			"GenericInitView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__init") {
				t.Error("generic init should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("FreeFunction_NotExtracted", func(t *testing.T) {
		t.Parallel()
		// Free functions are outside type context and not extracted.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"FreeFuncView.swift": fixtureLimitFreeFunction},
			"FreeFuncView.swift",
		)
		for _, p := range thunkPaths {
			if strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			if strings.Contains(content, "__preview__globalHelper") {
				t.Error("free function should NOT be in thunk (known limitation)")
			}
		}
	})

	t.Run("MultipleUnnamedPreviews_FirstSelected", func(t *testing.T) {
		t.Parallel()
		// When multiple unnamed #Preview blocks exist, only the first is used.
		thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
			map[string]string{"MultiPreviewView.swift": fixtureLimitMultipleUnnamedPreviews},
			"MultiPreviewView.swift",
		)
		for _, p := range thunkPaths {
			if !strings.Contains(filepath.Base(p), "_main") {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			// First preview uses .padding(), second uses .background(Color.red).
			// Verify first was selected.
			if !strings.Contains(content, ".padding()") {
				t.Error("first #Preview block should be selected by default")
			}
			if strings.Contains(content, "Color.red") {
				t.Error("second #Preview block should NOT be in main thunk")
			}
		}
	})

	t.Run("GenericTypeAnnotation_Compiles", func(t *testing.T) {
		t.Parallel()
		// Properties with generic type annotations like Array<String>
		// should compile correctly.
		runThunkCompileTest(t, sdk,
			map[string]string{"GenericTypeView.swift": fixtureLimitGenericTypeAnnotation},
			"GenericTypeView.swift",
		)
	})

	t.Run("ClassView_DependencyCompiles", func(t *testing.T) {
		t.Parallel()
		// Class-based observable objects as dependency files should
		// produce valid thunks (computed properties are still replaceable).
		const classViewMain = `import SwiftUI

struct ClassUserView: View {
    @StateObject var model = ClassBasedView()
    var body: some View {
        Text(model.formatted)
    }
}

#Preview {
    ClassUserView()
}
`
		runThunkCompileTest(t, sdk,
			map[string]string{
				"ClassUserView.swift":  classViewMain,
				"ClassBasedView.swift": fixtureClassView,
			},
			"ClassUserView.swift",
		)
	})

	t.Run("Actor_DependencyCompiles", func(t *testing.T) {
		t.Parallel()
		// Actor types as dependencies: the thunk should compile
		// even though runtime behavior with actor isolation is uncertain.
		runThunkCompileTest(t, sdk,
			map[string]string{
				"ActorView.swift": fixtureLimitActorView,
				"DataStore.swift": fixtureLimitActorStore,
			},
			"ActorView.swift",
		)
	})
}

// ============================================================
// Test helpers
// ============================================================

// runThunkCompileTest generates and typechecks thunks for the given sources.
// Panics on failure. Reuses the pattern from TestThunkCompilation.
func runThunkCompileTest(t *testing.T, sdk string, sources map[string]string, target string) {
	t.Helper()
	runThunkCompileTestWithPaths(t, sdk, sources, target)
}

// runThunkCompileTestWithPaths generates and typechecks thunks, returning
// the thunk paths and thunk directory for further inspection.
func runThunkCompileTestWithPaths(t *testing.T, sdk string, sources map[string]string, target string) (thunkPaths []string, thunkDir string) {
	t.Helper()

	parseDir := t.TempDir()
	moduleSrcDir := t.TempDir()

	// Write full sources to parseDir (for axe-parser).
	for name, src := range sources {
		writeFixtureFile(t, parseDir, name, src)
	}

	// Write stripped sources (no #Preview) to moduleSrcDir and build module.
	var moduleSrcPaths []string
	for name, src := range sources {
		stripped := stripPreviewBlocks(src)
		moduleSrcPaths = append(moduleSrcPaths, writeFixtureFile(t, moduleSrcDir, name, stripped))
	}
	moduleDir, cache := buildFixtureModule(t, moduleSrcPaths, compileTestModuleName, sdk)

	// Generate thunks.
	targetPath := filepath.Join(parseDir, target)
	thunkDir = filepath.Join(t.TempDir(), "thunk")
	dirs := previewDirs{Thunk: thunkDir}
	_ = dirs

	// Build a remapped cache: moduleSrcDir paths → parseDir paths.
	remappedFiles := make(map[string]*pb.IndexFileData)
	for name := range sources {
		parsePath := filepath.Join(parseDir, name)
		modulePath := filepath.Join(moduleSrcDir, name)
		if data := cache.FileData(modulePath); data != nil {
			remappedFiles[parsePath] = data
		}
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	var files []analysis.FileThunkData
	for name := range sources {
		path := filepath.Join(parseDir, name)
		var types []analysis.TypeInfo
		var imports []string
		var err error
		if name == target {
			types, imports, err = analysis.SourceFile(path, remappedCache)
		} else {
			types, imports, err = analysis.DependencyFile(path, remappedCache)
		}
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, analysis.FileThunkData{
			FileName:   name,
			AbsPath:    path,
			Types:      types,
			Imports:    imports,
			ModuleName: remappedCache.FileModuleName(path),
		})
	}
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, thunkDir, "", targetPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Typecheck the generated thunks against the fixture module.
	typecheckGeneratedThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)

	return thunkPaths, thunkDir
}
