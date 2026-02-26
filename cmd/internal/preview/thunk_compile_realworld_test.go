package preview

// Real-world project pattern tests for thunk generation.
//
// These test patterns commonly found in production SwiftUI projects
// that may not be covered by simple fixtures.

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
// Pattern 1: Generic View (struct MyView<T>: View)
//
// Generic types are common in reusable UI components.
// ============================================================

const fixtureGenericView = `import SwiftUI

struct GenericCard<Content: View>: View {
    let content: Content

    var headerText: String {
        "Header"
    }

    var body: some View {
        VStack {
            Text(headerText)
            content
        }
    }
}

struct GenericCardHost: View {
    var body: some View {
        GenericCard(content: Text("Hello"))
    }
}

#Preview {
    GenericCardHost()
}
`

func TestRealWorld_GenericView(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"GenericCard.swift": fixtureGenericView},
		"GenericCard.swift",
	)
}

// Check that computed property inside generic type gets thunk
func TestRealWorld_GenericView_PropertyInThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"GenericCard.swift": fixtureGenericView},
		"GenericCard.swift",
	)

	foundHeader := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "__preview__headerText") {
			foundHeader = true
		}
	}
	if !foundHeader {
		t.Error("computed property 'headerText' inside generic type GenericCard<Content> is not in thunk")
	}
}

// ============================================================
// Pattern 2: Extension-only file (no type declaration, only extensions)
//
// Common pattern: extension MyView { ... } in a separate file.
// The file has no struct/class declaration — only extensions.
// ============================================================

const fixtureExtensionOnlyBase = `import SwiftUI

struct ProfileView: View {
    var body: some View {
        Text(displayName)
    }
}
`

const fixtureExtensionOnlyExt = `import SwiftUI

extension ProfileView {
    var displayName: String {
        "John Doe"
    }
}
`

func TestRealWorld_ExtensionOnlyFile(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// The extension-only file has a computed property but no type declaration.
	// Can the thunk generator handle it as a dependency?
	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"ProfileView.swift":     fixtureExtensionOnlyBase,
			"ProfileView+Ext.swift": fixtureExtensionOnlyExt,
		},
		"ProfileView.swift",
	)

	foundDisplayName := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "__preview__displayName") {
			foundDisplayName = true
		}
	}
	if !foundDisplayName {
		t.Error("computed property 'displayName' from extension-only file is not in thunk")
	}
}

// ============================================================
// Pattern 3: Enum conforming to View
//
// While unusual, enums can conform to View with a computed body.
// ============================================================

const fixtureEnumView = `import SwiftUI

enum StatusBadge: View {
    case success
    case failure

    var color: Color {
        switch self {
        case .success: return .green
        case .failure: return .red
        }
    }

    var body: some View {
        Circle()
            .fill(color)
            .frame(width: 10, height: 10)
    }
}

struct StatusView: View {
    var body: some View {
        StatusBadge.success
    }
}

#Preview {
    StatusView()
}
`

func TestRealWorld_EnumView(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"StatusView.swift": fixtureEnumView},
		"StatusView.swift",
	)
}

// ============================================================
// Pattern 4: View with conditional compilation across platform
//
// #if os(iOS) / #if canImport(UIKit) etc.
// Common in cross-platform projects.
// ============================================================

const fixtureConditionalPlatform = `import SwiftUI

struct PlatformView: View {
    var platformName: String {
        #if os(iOS)
        "iOS"
        #elseif os(macOS)
        "macOS"
        #else
        "Unknown"
        #endif
    }

    var body: some View {
        Text(platformName)
    }
}

#Preview {
    PlatformView()
}
`

func TestRealWorld_ConditionalPlatform(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"PlatformView.swift": fixtureConditionalPlatform},
		"PlatformView.swift",
	)
}

// ============================================================
// Pattern 5: View using existential types (any Protocol)
//
// Swift 5.7+ existential syntax.
// ============================================================

const fixtureExistentialType = `import SwiftUI

protocol Displayable {
    var displayText: String { get }
}

struct Item: Displayable {
    var displayText: String { "Item" }
}

struct ExistentialView: View {
    var item: any Displayable = Item()

    var formattedText: String {
        item.displayText.uppercased()
    }

    var body: some View {
        Text(formattedText)
    }
}

#Preview {
    ExistentialView()
}
`

func TestRealWorld_ExistentialType(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"ExistentialView.swift": fixtureExistentialType},
		"ExistentialView.swift",
	)
}

// ============================================================
// Pattern 6: View with primary associated type (some Collection<Int>)
//
// Swift 5.7+ primary associated type syntax.
// ============================================================

const fixturePrimaryAssocType = `import SwiftUI

struct NumberListView: View {
    var numbers: some Collection<Int> {
        [1, 2, 3, 4, 5]
    }

    var body: some View {
        ForEach(Array(numbers), id: \.self) { n in
            Text("\(n)")
        }
    }
}

#Preview {
    NumberListView()
}
`

func TestRealWorld_PrimaryAssociatedType(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"NumberListView.swift": fixturePrimaryAssocType},
		"NumberListView.swift",
	)
}

// ============================================================
// Pattern 7: nonisolated computed property
//
// nonisolated is a decl modifier like mutating.
// The thunk may not preserve it.
// ============================================================

const fixtureNonisolated = `import SwiftUI

@MainActor
struct IsolatedView: View {
    nonisolated var identifier: String {
        "view-id"
    }

    var body: some View {
        Text(identifier)
    }
}

#Preview {
    IsolatedView()
}
`

func TestRealWorld_NonisolatedProperty(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"IsolatedView.swift": fixtureNonisolated},
		"IsolatedView.swift",
	)
}

// ============================================================
// Pattern 8: Large View with many computed properties (10+)
//
// Tests performance and correctness at scale.
// ============================================================

const fixtureManyProperties = `import SwiftUI

struct DashboardView: View {
    var metric1: String { "100" }
    var metric2: String { "200" }
    var metric3: String { "300" }
    var metric4: String { "400" }
    var metric5: String { "500" }
    var metric6: String { "600" }
    var metric7: String { "700" }
    var metric8: String { "800" }
    var metric9: String { "900" }
    var metric10: String { "1000" }

    var summary: String {
        "\(metric1)-\(metric10)"
    }

    var body: some View {
        VStack {
            Text(metric1)
            Text(metric2)
            Text(metric3)
            Text(metric4)
            Text(metric5)
            Text(summary)
        }
    }
}

#Preview {
    DashboardView()
}
`

func TestRealWorld_ManyComputedProperties(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"DashboardView.swift": fixtureManyProperties},
		"DashboardView.swift",
	)

	// Count how many __preview__ replacements are generated.
	// Expect 12: metric1-10 + summary + body
	count := 0
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		count += strings.Count(string(data), "__preview__")
	}
	expected := 12 // 10 metrics + summary + body
	if count != expected {
		t.Errorf("expected %d __preview__ replacements, got %d", expected, count)
	}
}

// ============================================================
// Pattern 9: View using SwiftData @Model (if available)
//
// @Model is a macro that generates conformance and storage.
// ============================================================

const fixtureSwiftDataModel = `import SwiftUI
import SwiftData

@Model
class TodoItem {
    var title: String
    var isDone: Bool

    init(title: String, isDone: Bool = false) {
        self.title = title
        self.isDone = isDone
    }

    var displayTitle: String {
        isDone ? "✓ \(title)" : title
    }
}

struct TodoRowView: View {
    var item: TodoItem

    var body: some View {
        Text(item.displayTitle)
    }
}
`

func TestRealWorld_SwiftDataModel(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"TodoRowView.swift": fixtureSwiftDataModel},
		"TodoRowView.swift",
	)
}

// ============================================================
// Pattern 10: View with access control modifiers
//
// public, internal, fileprivate, private on computed properties.
// ============================================================

const fixtureAccessControl = `import SwiftUI

public struct PublicView: View {
    public var publicProp: String {
        "public"
    }
    internal var internalProp: String {
        "internal"
    }
    fileprivate var fileprivateProp: String {
        "fileprivate"
    }
    private var privateProp: String {
        "private"
    }
    public var body: some View {
        VStack {
            Text(publicProp)
            Text(internalProp)
            Text(fileprivateProp)
            Text(privateProp)
        }
    }
}

#Preview {
    PublicView()
}
`

func TestRealWorld_AccessControlModifiers(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"PublicView.swift": fixtureAccessControl},
		"PublicView.swift",
	)
}

// Check all access levels produce thunks
func TestRealWorld_AccessControlModifiers_AllInThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"PublicView.swift": fixtureAccessControl},
		"PublicView.swift",
	)

	expected := []string{"__preview__publicProp", "__preview__internalProp",
		"__preview__fileprivateProp", "__preview__privateProp", "__preview__body"}
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		for _, name := range expected {
			if !strings.Contains(content, name) {
				t.Errorf("missing %s in thunk", name)
			}
		}
	}
}

// ============================================================
// Pattern 11: View with protocol conformance in extension
//
// Protocol methods defined in extension.
// ============================================================

const fixtureProtocolExtension = `import SwiftUI

protocol Configurable {
    var configuration: String { get }
}

struct ConfigView: View {
    var body: some View {
        Text(configuration)
    }
}

extension ConfigView: Configurable {
    var configuration: String {
        "Default Config"
    }
}

#Preview {
    ConfigView()
}
`

func TestRealWorld_ProtocolConformanceInExtension(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"ConfigView.swift": fixtureProtocolExtension},
		"ConfigView.swift",
	)

	foundConfig := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "__preview__configuration") {
			foundConfig = true
		}
	}
	if !foundConfig {
		t.Error("computed property 'configuration' from protocol extension is not in thunk")
	}
}

// ============================================================
// Pattern 12: Very long file path
//
// Projects with deep directory nesting may produce long paths.
// #sourceLocation directive must handle them.
// ============================================================

const fixtureLongPath = `import SwiftUI

struct LongPathView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    LongPathView()
}
`

func TestRealWorld_VeryLongFilePath(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Create a deeply nested directory to simulate long paths
	longDir := filepath.Join(t.TempDir(),
		"Features", "Authentication", "Presentation", "Views", "Components", "Subcomponents")
	if err := os.MkdirAll(longDir, 0o755); err != nil {
		t.Fatal(err)
	}

	parseDir := longDir
	moduleSrcDir := t.TempDir()

	writeFixtureFile(t, parseDir, "LongPathView.swift", fixtureLongPath)
	stripped := stripPreviewBlocks(fixtureLongPath)
	moduleSrcPath := writeFixtureFile(t, moduleSrcDir, "LongPathView.swift", stripped)
	moduleDir, cache := buildFixtureModule(t, []string{moduleSrcPath}, compileTestModuleName, sdk)

	parsePath := filepath.Join(parseDir, "LongPathView.swift")

	remappedFiles := make(map[string]*pb.IndexFileData)
	if data := cache.FileData(moduleSrcPath); data != nil {
		remappedFiles[parsePath] = data
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	types, imports, err := analysis.SourceFile(parsePath, remappedCache)
	if err != nil {
		t.Fatal(err)
	}

	files := []analysis.FileThunkData{{
		FileName:   "LongPathView.swift",
		AbsPath:    parsePath,
		Types:      types,
		Imports:    imports,
		ModuleName: remappedCache.FileModuleName(parsePath),
	}}

	thunkDir := filepath.Join(t.TempDir(), "thunk")
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, thunkDir, "", parsePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	typecheckGeneratedThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
}

// ============================================================
// Pattern 13: View with #available check
//
// Runtime availability checks are common in production.
// ============================================================

const fixtureAvailabilityCheck = `import SwiftUI

struct AvailView: View {
    var styledText: some View {
        if #available(iOS 17.0, *) {
            Text("Modern")
                .fontDesign(.rounded)
        } else {
            Text("Legacy")
        }
    }

    var body: some View {
        styledText
    }
}

#Preview {
    AvailView()
}
`

func TestRealWorld_AvailabilityCheck(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"AvailView.swift": fixtureAvailabilityCheck},
		"AvailView.swift",
	)
}

// ============================================================
// Pattern 14: View with multiple #Preview blocks (named)
//
// Real projects often have multiple named previews.
// ============================================================

const fixtureMultipleNamedPreviews = `import SwiftUI

struct MultiPreviewTarget: View {
    var style: String = "default"

    var styledLabel: String {
        "[\(style)]"
    }

    var body: some View {
        Text(styledLabel)
    }
}

#Preview("Default") {
    MultiPreviewTarget()
}

#Preview("Custom") {
    MultiPreviewTarget(style: "custom")
}

#Preview("Large") {
    MultiPreviewTarget(style: "large")
        .font(.largeTitle)
}
`

func TestRealWorld_MultipleNamedPreviews(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Default behavior selects first preview.
	// Verify it compiles without issues.
	runThunkCompileTest(t, sdk,
		map[string]string{"MultiPreviewTarget.swift": fixtureMultipleNamedPreviews},
		"MultiPreviewTarget.swift",
	)
}

// ============================================================
// Pattern 15: View with @ViewBuilder function
//
// @ViewBuilder on a function (not property).
// Since methods are not included (Root Cause A), this tests
// whether the @ViewBuilder attribute causes additional issues.
// ============================================================

const fixtureViewBuilderFunc = `import SwiftUI

struct BuilderFuncView: View {
    @ViewBuilder
    var header: some View {
        Text("Header")
            .font(.headline)
    }

    @ViewBuilder
    var footer: some View {
        Text("Footer")
            .font(.footnote)
    }

    var body: some View {
        VStack {
            header
            Spacer()
            footer
        }
    }
}

#Preview {
    BuilderFuncView()
}
`

func TestRealWorld_ViewBuilderProperties(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"BuilderFuncView.swift": fixtureViewBuilderFunc},
		"BuilderFuncView.swift",
	)
}

// Check that @ViewBuilder properties get thunks
func TestRealWorld_ViewBuilderProperties_InThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"BuilderFuncView.swift": fixtureViewBuilderFunc},
		"BuilderFuncView.swift",
	)

	foundHeader := false
	foundFooter := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if strings.Contains(content, "__preview__header") {
			foundHeader = true
		}
		if strings.Contains(content, "__preview__footer") {
			foundFooter = true
		}
	}
	if !foundHeader {
		t.Error("@ViewBuilder property 'header' is not in thunk")
	}
	if !foundFooter {
		t.Error("@ViewBuilder property 'footer' is not in thunk")
	}
}

// ============================================================
// Pattern 16: private computed property returning some View
//
// @_private(sourceFile:) で private メンバーにアクセスする際、
// opaque return type (some View) の具体型解決が別コンパイル単位で正しく動作するか確認。
// SwiftUI で頻出する ViewBuilder 分割パターン。
// ============================================================

const fixturePrivateSomeView = `import SwiftUI

struct PrivateSomeViewTest: View {
    private var header: some View {
        Text("Header")
            .font(.headline)
    }

    private var content: some View {
        VStack {
            Text("Line 1")
            Text("Line 2")
        }
    }

    var body: some View {
        VStack {
            header
            content
        }
    }
}

#Preview {
    PrivateSomeViewTest()
}
`

func TestRealWorld_PrivateSomeViewComputedProperty(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"PrivateSomeViewTest.swift": fixturePrivateSomeView},
		"PrivateSomeViewTest.swift",
	)
}

func TestRealWorld_PrivateSomeViewComputedProperty_InThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"PrivateSomeViewTest.swift": fixturePrivateSomeView},
		"PrivateSomeViewTest.swift",
	)

	foundHeader := false
	foundContent := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if strings.Contains(content, "__preview__header") {
			foundHeader = true
		}
		if strings.Contains(content, "__preview__content") {
			foundContent = true
		}
	}
	if !foundHeader {
		t.Error("private var header: some View is missing from thunk")
	}
	if !foundContent {
		t.Error("private var content: some View is missing from thunk")
	}
}

// ============================================================
// Pattern 17: @ViewBuilder + private + some View
// ============================================================

const fixturePrivateViewBuilderSomeView = `import SwiftUI

struct PrivateViewBuilderTest: View {
    @ViewBuilder
    private var conditionalContent: some View {
        if Bool.random() {
            Text("A")
        } else {
            Text("B")
        }
    }

    var body: some View {
        conditionalContent
    }
}

#Preview {
    PrivateViewBuilderTest()
}
`

func TestRealWorld_PrivateViewBuilderSomeView(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"PrivateViewBuilderTest.swift": fixturePrivateViewBuilderSomeView},
		"PrivateViewBuilderTest.swift",
	)
}
