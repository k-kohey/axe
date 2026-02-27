package preview

import (
	"path/filepath"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
)

func TestRegression_MethodIncluded(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct GreetMethodView: View {
    var body: some View {
        Text(greet(name: "World"))
    }

    func greet(name: String) -> String {
        "Hello, \(name)"
    }
}
`
	thunkPaths, _ := generateThunksForTest(t, sdk,
		map[string]string{"GreetMethodView.swift": fixture},
		"GreetMethodView.swift",
	)
	if !thunkContains(t, thunkPaths, "__preview__greet") {
		t.Error("method 'greet(name:)' should be in thunk")
	}
}

func TestRegression_MethodNoArgs(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct RefreshMethodView: View {
    var body: some View {
        Text("Hello")
    }

    func refresh() {
        print("refreshing")
    }
}
`
	thunkPaths, _ := generateThunksForTest(t, sdk,
		map[string]string{"RefreshMethodView.swift": fixture},
		"RefreshMethodView.swift",
	)
	if !thunkContains(t, thunkPaths, "__preview__refresh") {
		t.Error("no-args method 'refresh()' should be in thunk")
	}
}

func TestRegression_OverloadedMethodsIncluded(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct OverloadMethodView: View {
    var body: some View {
        Text("Hello")
    }

    func log() {
        print("no params")
    }

    func log(message: String) {
        print(message)
    }

    func log(message: String, level: Int) {
        print("[\(level)] \(message)")
    }
}
`
	thunkPaths, _ := generateThunksForTest(t, sdk,
		map[string]string{"OverloadMethodView.swift": fixture},
		"OverloadMethodView.swift",
	)
	count := thunkContainsCount(t, thunkPaths, "__preview__log")
	if count < 3 {
		t.Fatalf("expected 3 overloaded log replacements, got %d", count)
	}
}

func TestRegression_ShortNameCollision_TwoParents(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct FeatureX {
    struct Card: View {
        var body: some View { Text("X") }
    }
}

struct FeatureY {
    struct Card: View {
        var body: some View { Text("Y") }
    }
}

struct CardsView: View {
    var body: some View {
        VStack {
            FeatureX.Card()
            FeatureY.Card()
        }
    }
}

#Preview {
    CardsView()
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"CardsView.swift": fixture},
		"CardsView.swift",
	)
	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("expected compile success, got: %v", err)
	}
}

func TestRegression_ShortNameCollision_ParentChild(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct Container {
    struct Container: View {
        var label: String { "Inner" }
        var body: some View { Text(label) }
    }
    var dummy: String { "outer" }
}

struct WrapperView: View {
    var body: some View {
        Container.Container()
    }
}

#Preview {
    WrapperView()
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"WrapperView.swift": fixture},
		"WrapperView.swift",
	)
	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("expected compile success, got: %v", err)
	}
	if !thunkContains(t, thunkPaths, "extension Container.Container") {
		t.Error("missing extension Container.Container in thunk")
	}
}

func TestRegression_ShortNameCollision_Triple(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct SectionA {
    struct Cell: View {
        var body: some View { Text("A") }
    }
}

struct SectionB {
    struct Cell: View {
        var body: some View { Text("B") }
    }
}

struct SectionC {
    struct Cell: View {
        var body: some View { Text("C") }
    }
}

struct TableView: View {
    var body: some View {
        VStack {
            SectionA.Cell()
            SectionB.Cell()
            SectionC.Cell()
        }
    }
}

#Preview {
    TableView()
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"TableView.swift": fixture},
		"TableView.swift",
	)
	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("expected compile success, got: %v", err)
	}
}

func TestRegression_ShortNameCollision_DifferentDepth(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct Screens {
    struct Content: View {
        var body: some View { Text("Screens.Content") }
    }
}

struct Settings {
    struct Advanced {
        struct Content: View {
            var body: some View { Text("Settings.Advanced.Content") }
        }
    }
}

struct AppView: View {
    var body: some View {
        VStack {
            Screens.Content()
            Settings.Advanced.Content()
        }
    }
}

#Preview {
    AppView()
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"AppView.swift": fixture},
		"AppView.swift",
	)
	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("expected compile success, got: %v", err)
	}
}

func TestRegression_ShortNameCollision_ExtensionNested(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixture := `import SwiftUI

struct Theme {
    struct Style {
        var color: Color { .blue }
    }
}

struct CardView: View {
    var body: some View {
        Text("Card").foregroundColor(Theme.Style().color)
    }
}

extension CardView {
    struct Style {
        var font: String { "body" }
    }
}

#Preview {
    CardView()
}
`
	thunkPaths, _ := generateThunksForTest(t, sdk,
		map[string]string{"CardView.swift": fixture},
		"CardView.swift",
	)
	if !thunkContains(t, thunkPaths, "extension CardView.Style") {
		t.Fatal("expected extension CardView.Style in thunk")
	}
}

func TestRegression_ExtensionOnlyTarget(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixtureBase := `import SwiftUI

struct ExtOnlyTarget: View {
    var body: some View {
        Text(extra)
    }
}
`
	fixtureExt := `import SwiftUI

extension ExtOnlyTarget {
    var extra: String { "Extension" }
}

#Preview {
    ExtOnlyTarget()
}
`
	parseDir := t.TempDir()
	moduleSrcDir := t.TempDir()
	writeFixtureFile(t, parseDir, "ExtOnlyTarget.swift", fixtureBase)
	writeFixtureFile(t, parseDir, "ExtOnlyTarget+Preview.swift", fixtureExt)
	modBase := writeFixtureFile(t, moduleSrcDir, "ExtOnlyTarget.swift", stripPreviewBlocks(fixtureBase))
	modExt := writeFixtureFile(t, moduleSrcDir, "ExtOnlyTarget+Preview.swift", stripPreviewBlocks(fixtureExt))
	_, cache := buildFixtureModule(t, []string{modBase, modExt}, compileTestModuleName, sdk)

	parseExt := filepath.Join(parseDir, "ExtOnlyTarget+Preview.swift")
	remappedFiles := make(map[string]*pb.IndexFileData)
	if d := cache.FileData(modExt); d != nil {
		remappedFiles[parseExt] = d
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})
	if _, _, err := analysis.SourceFile(parseExt, remappedCache); err != nil {
		t.Fatalf("expected extension-only target to be accepted, got: %v", err)
	}
}

func TestRegression_DepOnlyImport(t *testing.T) {
	sdk := simulatorSDKPath(t)
	fixtureTarget := `import SwiftUI

struct ImportMergeHost: View {
    var body: some View {
        MapDepView()
    }
}

#Preview {
    VStack {
        ImportMergeHost()
        Map()
    }
}
`
	fixtureDep := `import SwiftUI
import MapKit

struct MapDepView: View {
    var body: some View {
        Map()
    }
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{
			"ImportMergeHost.swift": fixtureTarget,
			"MapDepView.swift":      fixtureDep,
		},
		"ImportMergeHost.swift",
	)
	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("expected compile success with dep-only import merge, got: %v", err)
	}
}

// Regression: #if canImport(X) 内の import が thunk に反映される。
// ImportCollector が条件付き import を `#if canImport(X)\nimport X\n#endif`
// の形で保持し、thunk コンパイラが canImport を評価して解決する。
func TestRegression_ConditionalImportInThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	fixture := `import SwiftUI
#if canImport(MapKit)
import MapKit
#endif

struct ConditionalImportView: View {
    var region: MKCoordinateRegion {
        MKCoordinateRegion()
    }

    var body: some View {
        Text("Lat: \(region.center.latitude)")
    }
}

#Preview {
    ConditionalImportView()
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"ConditionalImportView.swift": fixture},
		"ConditionalImportView.swift",
	)

	if !thunkContains(t, thunkPaths, "#if canImport(MapKit)") {
		t.Error("per-file thunk should contain #if canImport(MapKit)")
	}

	if err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk); err != nil {
		t.Fatalf("per-file thunk should compile but failed: %v", err)
	}
}
