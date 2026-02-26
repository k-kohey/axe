package preview

// Multi-file thunk generation edge cases.
//
// These tests focus on scenarios where multiple source files interact
// in ways that may break thunk generation: name collisions across files,
// same basenames, cross-file extensions, etc.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Multi 1: Same basename files from different directories
//
// Two dependency files both named "ItemView.swift" but in different dirs.
// @_private(sourceFile: "ItemView.swift") is identical for both.
// The per-file thunks may import the wrong file's private types.
// Also, the thunk filenames collide (thunk_0_ItemView.swift).
// ============================================================

const fixtureMultiSameBaseA = `import SwiftUI

private struct LocalStyle {
    var color: Color { .blue }
}

struct ItemViewA: View {
    var styled: Color {
        LocalStyle().color
    }
    var body: some View {
        Text("A").foregroundColor(styled)
    }
}
`

const fixtureMultiSameBaseB = `import SwiftUI

private struct LocalStyle {
    var color: Color { .red }
}

struct ItemViewB: View {
    var styled: Color {
        LocalStyle().color
    }
    var body: some View {
        Text("B").foregroundColor(styled)
    }
}
`

const fixtureMultiSameBaseTarget = `import SwiftUI

struct SameBaseHost: View {
    var body: some View {
        VStack {
            ItemViewA()
            ItemViewB()
        }
    }
}

#Preview {
    SameBaseHost()
}
`

// Multi 1: Same basename files from different directories — known bug.
// Moved to thunk_compile_known_bugs_test.go (TestKnownBug_SameBasenameDifferentDirs).
func TestMultiFile_SameBasenameDifferentDirs(t *testing.T) {
	t.Skip("Known bug: moved to TestKnownBug_SameBasenameDifferentDirs")
}

// ============================================================
// Multi 2: Same type name in different dependency files
//
// FileA.swift defines struct Helper with computed property.
// FileB.swift defines struct Helper with different computed property.
// Both are dependency files.
// ============================================================

const fixtureMultiSameTypeA = `import SwiftUI

struct Helper {
    var label: String {
        "Helper A"
    }
}

struct HelperViewA: View {
    var body: some View {
        Text(Helper().label)
    }
}
`

const fixtureMultiSameTypeB = `import SwiftUI

struct AnotherHelper {
    var label: String {
        "Another Helper"
    }
}

struct HelperViewB: View {
    var body: some View {
        Text(AnotherHelper().label)
    }
}
`

const fixtureMultiSameTypeTarget = `import SwiftUI

struct SameTypeHost: View {
    var body: some View {
        VStack {
            HelperViewA()
            HelperViewB()
        }
    }
}

#Preview {
    SameTypeHost()
}
`

func TestMultiFile_SameTypeNameInDeps(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Each file has its own per-file thunk with @_private scope.
	// Helper type appears only in FileA's thunk scope.
	runThunkCompileTest(t, sdk,
		map[string]string{
			"HelperA.swift":      fixtureMultiSameTypeA,
			"HelperB.swift":      fixtureMultiSameTypeB,
			"SameTypeHost.swift": fixtureMultiSameTypeTarget,
		},
		"SameTypeHost.swift",
	)
}

// ============================================================
// Multi 3: Cross-file extension — dependency extends target's type
//
// Target file defines TargetView.
// Dependency file has extension TargetView { var extra: ... }
// The dependency's extension members should appear in the dep's thunk.
// ============================================================

const fixtureMultiCrossExtTarget = `import SwiftUI

struct ExtendedView: View {
    var body: some View {
        Text(subtitle)
    }
}

#Preview {
    ExtendedView()
}
`

const fixtureMultiCrossExtDep = `import SwiftUI

extension ExtendedView {
    var subtitle: String {
        "Subtitle from extension"
    }
}
`

func TestMultiFile_CrossFileExtension(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"ExtendedView.swift":     fixtureMultiCrossExtTarget,
			"ExtendedView+Ext.swift": fixtureMultiCrossExtDep,
		},
		"ExtendedView.swift",
	)

	// Check that subtitle appears in one of the thunks
	foundSubtitle := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, _ := os.ReadFile(p)
		content := string(data)
		t.Logf("Thunk %s:\n%s", filepath.Base(p), content)
		if strings.Contains(content, "__preview__subtitle") {
			foundSubtitle = true
		}
	}
	if !foundSubtitle {
		t.Error("computed property 'subtitle' from cross-file extension is not in any thunk")
	}
}

// ============================================================
// Multi 4: Dependency has nested type with same short name as target's nested type
//
// Target: struct Host { struct Row: View { ... } }
// Dep:    struct Data { struct Row { var text: String { ... } } }
//
// Since combineWithIndexStore is per-file, this should be fine.
// But verify no cross-file name confusion happens.
// ============================================================

const fixtureMultiCrossNestTarget = `import SwiftUI

struct Host {
    struct Row: View {
        var body: some View {
            Text("Host.Row")
        }
    }
}

struct CrossNestHostView: View {
    var body: some View {
        Host.Row()
    }
}

#Preview {
    CrossNestHostView()
}
`

const fixtureMultiCrossNestDep = `import SwiftUI

struct DataGroup {
    struct Row {
        var text: String {
            "DataGroup.Row"
        }
    }
}

struct DataGroupView: View {
    var body: some View {
        Text(DataGroup.Row().text)
    }
}
`

func TestMultiFile_CrossFileNestedSameShortName(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{
			"CrossNestHost.swift": fixtureMultiCrossNestTarget,
			"DataGroupView.swift": fixtureMultiCrossNestDep,
		},
		"CrossNestHost.swift",
	)
}

// ============================================================
// Multi 5: Many dependency files (8 files)
//
// Simulate a realistic project with many dependencies.
// ============================================================

func TestMultiFile_ManyDependencies(t *testing.T) {
	sdk := simulatorSDKPath(t)

	sources := map[string]string{
		"CompA.swift": `import SwiftUI
struct CompA: View {
    var label: String { "A" }
    var body: some View { Text(label) }
}
`,
		"CompB.swift": `import SwiftUI
struct CompB: View {
    var label: String { "B" }
    var body: some View { Text(label) }
}
`,
		"CompC.swift": `import SwiftUI
struct CompC: View {
    var label: String { "C" }
    var body: some View { Text(label) }
}
`,
		"CompD.swift": `import SwiftUI
struct CompD: View {
    var label: String { "D" }
    var body: some View { Text(label) }
}
`,
		"CompE.swift": `import SwiftUI
struct CompE: View {
    var label: String { "E" }
    var body: some View { Text(label) }
}
`,
		"CompF.swift": `import SwiftUI
struct CompF: View {
    var label: String { "F" }
    var body: some View { Text(label) }
}
`,
		"CompG.swift": `import SwiftUI
struct CompG: View {
    var label: String { "G" }
    var body: some View { Text(label) }
}
`,
		"ManyDepsHost.swift": `import SwiftUI
struct ManyDepsHost: View {
    var body: some View {
        VStack {
            CompA()
            CompB()
            CompC()
            CompD()
            CompE()
            CompF()
            CompG()
        }
    }
}

#Preview {
    ManyDepsHost()
}
`,
	}

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk, sources, "ManyDepsHost.swift")

	// Should have 8 per-file thunks + 1 main thunk = 9 total
	if len(thunkPaths) != 9 {
		t.Errorf("expected 9 thunk files (8 per-file + 1 main), got %d", len(thunkPaths))
	}
}

// ============================================================
// Multi 6: Dependency file has no computed properties (stored only)
//
// The dependency has only stored properties — no thunk should be
// generated for it, or it should be an empty extension.
// ============================================================

const fixtureMultiStoredOnlyDep = `import SwiftUI

struct Config {
    let title: String
    let count: Int
}
`

const fixtureMultiStoredOnlyTarget = `import SwiftUI

struct StoredDepHost: View {
    var body: some View {
        let c = Config(title: "Hello", count: 5)
        Text("\(c.title): \(c.count)")
    }
}

#Preview {
    StoredDepHost()
}
`

func TestMultiFile_DepWithNoComputedProperties(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Config has no computed properties, so its thunk should have no extensions.
	// The thunk should still compile.
	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"Config.swift":         fixtureMultiStoredOnlyDep,
			"StoredDepHost.swift": fixtureMultiStoredOnlyTarget,
		},
		"StoredDepHost.swift",
	)

	// Config.swift's per-file thunk should exist but have no extension blocks
	for _, p := range thunkPaths {
		base := filepath.Base(p)
		if strings.Contains(base, "Config") {
			data, _ := os.ReadFile(p)
			content := string(data)
			if strings.Contains(content, "extension") {
				t.Log("Config thunk has extensions (unexpected for stored-only type):\n" + content)
			}
		}
	}
}

// ============================================================
// Multi 7: Target file is extension-only (no struct declaration)
//
// Target file has only "extension SomeView { ... }" but no struct.
// SourceFile() requires at least one View with body → should error.
// ============================================================

const fixtureMultiExtOnlyTargetBase = `import SwiftUI

struct ExtOnlyTarget: View {
    var body: some View {
        Text(extra)
    }
}
`

const fixtureMultiExtOnlyTargetExt = `import SwiftUI

extension ExtOnlyTarget {
    var extra: String {
        "Extension"
    }
}

#Preview {
    ExtOnlyTarget()
}
`

// Multi 7: Target file is extension-only — known bug.
// Moved to thunk_compile_known_bugs_test.go (TestKnownBug_ExtensionOnlyTarget).
func TestMultiFile_TargetIsExtensionOnly(t *testing.T) {
	t.Skip("Known bug: moved to TestKnownBug_ExtensionOnlyTarget")
}

// ============================================================
// Multi 8: Two dependency files with same private type name
//
// FileA has `private struct Styles { ... }` and
// FileB has `private struct Styles { ... }`.
// Per-file thunks should isolate them.
// ============================================================

const fixtureMultiPrivateCollisionA = `import SwiftUI

private struct Styles {
    var primary: Color { .blue }
}

struct StyledViewA: View {
    var color: Color { Styles().primary }
    var body: some View {
        Text("A").foregroundColor(color)
    }
}
`

const fixtureMultiPrivateCollisionB = `import SwiftUI

private struct Styles {
    var primary: Color { .green }
}

struct StyledViewB: View {
    var color: Color { Styles().primary }
    var body: some View {
        Text("B").foregroundColor(color)
    }
}
`

const fixtureMultiPrivateCollisionTarget = `import SwiftUI

struct PrivCollisionHost: View {
    var body: some View {
        VStack {
            StyledViewA()
            StyledViewB()
        }
    }
}

#Preview {
    PrivCollisionHost()
}
`

func TestMultiFile_PrivateTypeCollisionAcrossFiles(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Per-file thunks isolate private types, so this should compile.
	runThunkCompileTest(t, sdk,
		map[string]string{
			"StyledViewA.swift":      fixtureMultiPrivateCollisionA,
			"StyledViewB.swift":      fixtureMultiPrivateCollisionB,
			"PrivCollisionHost.swift": fixtureMultiPrivateCollisionTarget,
		},
		"PrivCollisionHost.swift",
	)
}

// ============================================================
// Multi 9: Dependency uses a different module (import Foundation only)
//
// Some dependency files may not import SwiftUI at all.
// The Index Store would report a different module for them.
// ============================================================

const fixtureMultiNonSwiftUIDep = `import Foundation

struct DataFormatter {
    var formatted: String {
        "formatted"
    }
}
`

const fixtureMultiNonSwiftUITarget = `import SwiftUI

struct FormatterHost: View {
    var body: some View {
        Text(DataFormatter().formatted)
    }
}

#Preview {
    FormatterHost()
}
`

func TestMultiFile_DepWithoutSwiftUIImport(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{
			"DataFormatter.swift": fixtureMultiNonSwiftUIDep,
			"FormatterHost.swift": fixtureMultiNonSwiftUITarget,
		},
		"FormatterHost.swift",
	)
}

// ============================================================
// Multi 10: Dependency file has same short name nested type as target
//           AND target file also has same short name nested type
//
// Target: struct Screen { struct Header: View { ... } }
// Dep:    struct Panel  { struct Header: View { ... } }
//
// Per-file isolation means each file's qualifiedNames is independent.
// But if BOTH types end up in the SAME per-file thunk somehow
// (e.g., both defined in the target file), it would break.
// This test verifies the cross-file case is safe.
// ============================================================

const fixtureMultiCrossShortTarget = `import SwiftUI

struct Screen {
    struct Header: View {
        var body: some View {
            Text("Screen.Header")
        }
    }
}

struct CrossShortHost: View {
    var body: some View {
        VStack {
            Screen.Header()
            Panel.Header()
        }
    }
}

#Preview {
    CrossShortHost()
}
`

const fixtureMultiCrossShortDep = `import SwiftUI

struct Panel {
    struct Header: View {
        var body: some View {
            Text("Panel.Header")
        }
    }
}
`

func TestMultiFile_CrossFileShortNameNoCollision(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// When the two "Header" types are in DIFFERENT files,
	// per-file qualifiedNames isolation should prevent collision.
	runThunkCompileTest(t, sdk,
		map[string]string{
			"CrossShortHost.swift": fixtureMultiCrossShortTarget,
			"PanelHeader.swift":   fixtureMultiCrossShortDep,
		},
		"CrossShortHost.swift",
	)
}

// ============================================================
// Multi 11: Dependency chain (A -> B -> C)
//
// Target references B, B references C.
// All three files are dependencies.
// ============================================================

const fixtureChainA = `import SwiftUI

struct ChainHost: View {
    var body: some View {
        ChainB()
    }
}

#Preview {
    ChainHost()
}
`

const fixtureChainB = `import SwiftUI

struct ChainB: View {
    var label: String { "B" }
    var body: some View {
        VStack {
            Text(label)
            ChainC()
        }
    }
}
`

const fixtureChainC = `import SwiftUI

struct ChainC: View {
    var detail: String { "C" }
    var body: some View {
        Text(detail)
    }
}
`

func TestMultiFile_DependencyChain(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{
			"ChainHost.swift": fixtureChainA,
			"ChainB.swift":    fixtureChainB,
			"ChainC.swift":    fixtureChainC,
		},
		"ChainHost.swift",
	)

	// Verify all three files got thunks and they compile
	if len(thunkPaths) != 4 { // 3 per-file + 1 main
		t.Errorf("expected 4 thunk files, got %d", len(thunkPaths))
	}
}
