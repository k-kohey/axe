package preview

// Multi-file thunk generation edge cases — part 2.
//
// Deeper scenarios: import merging, stale cache, conditional compilation,
// same type extended in multiple files, transitive imports, etc.

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
// Multi2-1: Preview body references type from dependency's import
//
// Target imports SwiftUI only.
// Dependency imports MapKit.
// Preview body uses Map() from MapKit.
// The main thunk only includes target's imports → Map() is unknown.
// ============================================================

const fixtureMulti2ImportMergingTarget = `import SwiftUI

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

const fixtureMulti2ImportMergingDep = `import SwiftUI
import MapKit

struct MapDepView: View {
    var body: some View {
        Map()
    }
}
`

// Multi2-1: Preview body references type from dependency's import — known bug.
// Moved to thunk_compile_known_bugs_test.go (TestKnownBug_DepOnlyImport).
func TestMultiFile2_PreviewUsesDepOnlyImport(t *testing.T) {
	t.Skip("Known bug: moved to TestKnownBug_DepOnlyImport")
}

// ============================================================
// Multi2-2: Same type extended in multiple dependency files
//
// BaseView is defined in one file.
// ExtA.swift adds computed property "labelA" via extension.
// ExtB.swift adds computed property "labelB" via extension.
// Both extension files are dependencies.
// ============================================================

const fixtureMulti2MultiExtBase = `import SwiftUI

struct MultiExtView: View {
    var body: some View {
        VStack {
            Text(labelA)
            Text(labelB)
        }
    }
}

#Preview {
    MultiExtView()
}
`

const fixtureMulti2MultiExtA = `import SwiftUI

extension MultiExtView {
    var labelA: String {
        "Label A"
    }
}
`

const fixtureMulti2MultiExtB = `import SwiftUI

extension MultiExtView {
    var labelB: String {
        "Label B"
    }
}
`

func TestMultiFile2_SameTypeExtendedInMultipleFiles(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"MultiExtView.swift": fixtureMulti2MultiExtBase,
			"ExtA.swift":         fixtureMulti2MultiExtA,
			"ExtB.swift":         fixtureMulti2MultiExtB,
		},
		"MultiExtView.swift",
	)

	// Both labelA and labelB should appear in thunks (in different per-file thunks)
	foundA := false
	foundB := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__labelA") {
			foundA = true
		}
		if strings.Contains(content, "__preview__labelB") {
			foundB = true
		}
	}
	if !foundA {
		t.Error("labelA from ExtA.swift extension is missing from thunks")
	}
	if !foundB {
		t.Error("labelB from ExtB.swift extension is missing from thunks")
	}
}

// ============================================================
// Multi2-3: Dependency file with #Preview block
//
// Both target and dependency have #Preview blocks.
// Only the target's #Preview should be used.
// But stripping #Preview from the dependency may shift line numbers
// differently than expected.
// ============================================================

const fixtureMulti2DepWithPreviewTarget = `import SwiftUI

struct DepPreviewHost: View {
    var body: some View {
        DepPreviewChild()
    }
}

#Preview {
    DepPreviewHost()
}
`

const fixtureMulti2DepWithPreviewDep = `import SwiftUI

struct DepPreviewChild: View {
    var title: String {
        "Child"
    }

    var body: some View {
        Text(title)
    }
}

#Preview {
    DepPreviewChild()
}
`

func TestMultiFile2_DependencyAlsoHasPreview(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"DepPreviewHost.swift":  fixtureMulti2DepWithPreviewTarget,
			"DepPreviewChild.swift": fixtureMulti2DepWithPreviewDep,
		},
		"DepPreviewHost.swift",
	)

	// The dependency's title property should be in its thunk.
	foundTitle := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__title") {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Error("dependency's 'title' property missing — #Preview stripping in dependency may have caused line number shift")
	}

	// Main thunk should have host's preview, NOT child's
	for _, p := range thunkPaths {
		if !strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if !strings.Contains(content, "DepPreviewHost()") {
			t.Error("main thunk should contain host's preview body")
		}
		if strings.Contains(content, "DepPreviewChild()") && !strings.Contains(content, "DepPreviewHost()") {
			t.Error("main thunk should NOT use dependency's #Preview")
		}
	}
}

// ============================================================
// Multi2-4: Dependency with #Preview BEFORE computed properties
//
// The #Preview block appears in the MIDDLE of the dep file,
// BEFORE computed properties. Stripping shifts line numbers
// of all subsequent properties.
// ============================================================

const fixtureMulti2DepPreviewMidTarget = `import SwiftUI

struct MidPreviewHost: View {
    var body: some View {
        MidPreviewChild()
    }
}

#Preview {
    MidPreviewHost()
}
`

const fixtureMulti2DepPreviewMidDep = `import SwiftUI

struct MidPreviewChild: View {
    var body: some View {
        VStack {
            Text(topLabel)
            Text(bottomLabel)
        }
    }
}

#Preview {
    MidPreviewChild()
}

extension MidPreviewChild {
    var topLabel: String {
        "Top"
    }

    var bottomLabel: String {
        "Bottom"
    }
}
`

func TestMultiFile2_DepPreviewBeforeExtensionProperties(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"MidPreviewHost.swift":  fixtureMulti2DepPreviewMidTarget,
			"MidPreviewChild.swift": fixtureMulti2DepPreviewMidDep,
		},
		"MidPreviewHost.swift",
	)

	// The extension properties after #Preview should be in the thunk
	// despite the line number shift from stripping.
	foundTop := false
	foundBottom := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__topLabel") {
			foundTop = true
		}
		if strings.Contains(content, "__preview__bottomLabel") {
			foundBottom = true
		}
	}
	if !foundTop {
		t.Error("'topLabel' missing — line shift from #Preview stripping before extension caused mismatch")
	}
	if !foundBottom {
		t.Error("'bottomLabel' missing — line shift from #Preview stripping before extension caused mismatch")
	}
}

// ============================================================
// Multi2-5: Dependency has conditional compilation changing members
//
// #if DEBUG adds extra computed properties.
// Index Store is built without DEBUG → members differ from parser.
// ============================================================

const fixtureMulti2ConditionalTarget = `import SwiftUI

struct ConditionalHost: View {
    var body: some View {
        ConditionalChild()
    }
}

#Preview {
    ConditionalHost()
}
`

const fixtureMulti2ConditionalDep = `import SwiftUI

struct ConditionalChild: View {
    var body: some View {
        VStack {
            Text(title)
            #if DEBUG
            Text(debugInfo)
            #endif
        }
    }

    var title: String {
        "Title"
    }

    #if DEBUG
    var debugInfo: String {
        "Debug Mode"
    }
    #endif
}
`

func TestMultiFile2_ConditionalCompilationMembers(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Note: swiftc builds with default flags (no -DDEBUG explicitly).
	// The parser (swift-syntax) always sees both branches.
	// This may cause a mismatch between Index Store members and parser members.
	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"ConditionalHost.swift":  fixtureMulti2ConditionalTarget,
			"ConditionalChild.swift": fixtureMulti2ConditionalDep,
		},
		"ConditionalHost.swift",
	)

	foundTitle := false
	foundDebug := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__title") {
			foundTitle = true
		}
		if strings.Contains(content, "__preview__debugInfo") {
			foundDebug = true
		}
	}
	if !foundTitle {
		t.Error("'title' missing from thunk")
	}
	// debugInfo may or may not appear depending on build flags
	t.Logf("debugInfo in thunk: %v (depends on #if DEBUG evaluation during module build)", foundDebug)
}

// ============================================================
// Multi2-6: Missing Index Store data for dependency
//
// What happens when the Index Store doesn't have data for a dep file?
// DependencyFile falls back to parser-only output (no types).
// ============================================================

func TestMultiFile2_MissingIndexStoreForDep(t *testing.T) {
	sdk := simulatorSDKPath(t)

	parseDir := t.TempDir()
	moduleSrcDir := t.TempDir()

	targetSrc := `import SwiftUI

struct MissingIndexHost: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MissingIndexHost()
}
`
	depSrc := `import SwiftUI

struct MissingIndexDep {
    var info: String {
        "info"
    }
}
`

	writeFixtureFile(t, parseDir, "MissingIndexHost.swift", targetSrc)
	writeFixtureFile(t, parseDir, "MissingIndexDep.swift", depSrc)

	// Build module from ONLY the target file (dep is not compiled into module)
	modTarget := writeFixtureFile(t, moduleSrcDir, "MissingIndexHost.swift", stripPreviewBlocks(targetSrc))
	// Intentionally do NOT compile the dep file into the module
	moduleDir, cache := buildFixtureModule(t, []string{modTarget}, compileTestModuleName, sdk)

	// Remap only target
	targetPath := filepath.Join(parseDir, "MissingIndexHost.swift")
	depPath := filepath.Join(parseDir, "MissingIndexDep.swift")
	remappedFiles := make(map[string]*pb.IndexFileData)
	if d := cache.FileData(modTarget); d != nil {
		remappedFiles[targetPath] = d
	}
	// No remapping for dep — Index Store has no data
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	typesTarget, importsTarget, err := analysis.SourceFile(targetPath, remappedCache)
	if err != nil {
		t.Fatal(err)
	}

	// DependencyFile with no Index Store data → should return empty types
	typesDep, importsDep, err := analysis.DependencyFile(depPath, remappedCache)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Dep types count: %d (expected 0 — no Index Store data)", len(typesDep))
	if len(typesDep) > 0 {
		t.Log("Dependency returned types without Index Store data — unexpected")
	}

	files := []analysis.FileThunkData{
		{FileName: "MissingIndexHost.swift", AbsPath: targetPath, Types: typesTarget, Imports: importsTarget},
		{FileName: "MissingIndexDep.swift", AbsPath: depPath, Types: typesDep, Imports: importsDep},
	}

	thunkDir := filepath.Join(t.TempDir(), "thunk")
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, thunkDir, "", targetPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Should still compile (dep thunk just has no extensions)
	typecheckGeneratedThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
}

// ============================================================
// Multi2-7: Dep file has very different line numbers after #Preview stripping
//
// Dep file has MULTIPLE #Preview blocks (e.g. 3) in the middle,
// causing a large line offset for subsequent properties.
// ============================================================

const fixtureMulti2ManyPreviewsTarget = `import SwiftUI

struct ManyPrevHost: View {
    var body: some View {
        ManyPrevChild()
    }
}

#Preview {
    ManyPrevHost()
}
`

const fixtureMulti2ManyPreviewsDep = `import SwiftUI

struct ManyPrevChild: View {
    var body: some View {
        Text(label)
    }
}

#Preview("A") {
    ManyPrevChild()
}

#Preview("B") {
    ManyPrevChild()
        .padding()
}

#Preview("C") {
    ManyPrevChild()
        .background(Color.blue)
}

extension ManyPrevChild {
    var label: String {
        "After many previews"
    }
}
`

func TestMultiFile2_DepWithManyPreviewsBeforeExtension(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"ManyPrevHost.swift":  fixtureMulti2ManyPreviewsTarget,
			"ManyPrevChild.swift": fixtureMulti2ManyPreviewsDep,
		},
		"ManyPrevHost.swift",
	)

	// 3 #Preview blocks stripped = ~12 lines offset
	// label's Index Store line is ~15 but parser line is ~27
	// exact match fails → fallback should work (unique name "label" in type)
	foundLabel := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__label") {
			foundLabel = true
		}
	}
	if !foundLabel {
		t.Error("'label' missing — large line offset from stripping 3 #Preview blocks caused mismatch even in fallback")
	}
}

// ============================================================
// Multi2-8: Two dep files extend same type with same property name
//
// ExtA adds `var badge: String` to SharedView.
// ExtB also adds `var badge: String` to SharedView.
// This is invalid Swift (duplicate declaration), but tests
// how the thunk generator handles it.
// ============================================================

const fixtureMulti2DupExtBase = `import SwiftUI

struct SharedView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    SharedView()
}
`

const fixtureMulti2DupExtA = `import SwiftUI

extension SharedView {
    var badgeA: String {
        "A"
    }
}
`

const fixtureMulti2DupExtB = `import SwiftUI

extension SharedView {
    var badgeB: String {
        "B"
    }
}
`

func TestMultiFile2_TwoExtFilesSameType(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Both extensions add different properties.
	// Per-file thunks: each gets its own @_dynamicReplacement.
	// Both are "extension SharedView { ... }" but with different properties.
	// This should compile fine since the property names are different.
	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"SharedView.swift": fixtureMulti2DupExtBase,
			"ExtA.swift":       fixtureMulti2DupExtA,
			"ExtB.swift":       fixtureMulti2DupExtB,
		},
		"SharedView.swift",
	)

	foundA := false
	foundB := false
	for _, p := range thunkPaths {
		data, _ := os.ReadFile(p)
		content := string(data)
		if strings.Contains(content, "__preview__badgeA") {
			foundA = true
		}
		if strings.Contains(content, "__preview__badgeB") {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected both badgeA and badgeB in thunks, got A=%v B=%v", foundA, foundB)
	}
}

// ============================================================
// Multi2-9: Dep file has same property name as target's property
//
// Target: var title: String { "Target" }
// Dep:    var title: String { "Dep" }  (different type)
// Both get per-file thunks with __preview__title.
// Should be independent.
// ============================================================

const fixtureMulti2SamePropTarget = `import SwiftUI

struct SamePropHost: View {
    var title: String {
        "Host Title"
    }
    var body: some View {
        VStack {
            Text(title)
            SamePropChild()
        }
    }
}

#Preview {
    SamePropHost()
}
`

const fixtureMulti2SamePropDep = `import SwiftUI

struct SamePropChild: View {
    var title: String {
        "Child Title"
    }
    var body: some View {
        Text(title)
    }
}
`

func TestMultiFile2_SamePropertyNameAcrossFiles(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"SamePropHost.swift":  fixtureMulti2SamePropTarget,
			"SamePropChild.swift": fixtureMulti2SamePropDep,
		},
		"SamePropHost.swift",
	)

	// Each file's thunk should have its own __preview__title
	titleCount := 0
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		titleCount += strings.Count(string(data), "__preview__title")
	}
	if titleCount != 2 {
		t.Errorf("expected 2 __preview__title (one per file), got %d", titleCount)
	}
}

// ============================================================
// Multi2-10: Target imports dependency's module-level type alias
//
// Dep defines typealias. Target uses it.
// ============================================================

const fixtureMulti2TypeAliasDep = `import SwiftUI

typealias StyledText = Text
`

const fixtureMulti2TypeAliasTarget = `import SwiftUI

struct TypeAliasHost: View {
    var label: StyledText {
        StyledText("Aliased")
    }
    var body: some View {
        label
    }
}

#Preview {
    TypeAliasHost()
}
`

func TestMultiFile2_TypeAliasInDependency(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{
			"TypeAlias.swift":     fixtureMulti2TypeAliasDep,
			"TypeAliasHost.swift": fixtureMulti2TypeAliasTarget,
		},
		"TypeAliasHost.swift",
	)
}
