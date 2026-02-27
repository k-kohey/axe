package preview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/preview/analysis"
	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"github.com/k-kohey/axe/internal/preview/codegen"
)

const (
	compileTestTarget     = "arm64-apple-ios17.0-simulator"
	compileTestModuleName = "TestModule"
)

// --- Test helpers ---

// simulatorSDKPath returns the iOS simulator SDK path via xcrun.
// Skips the test if Xcode is not available.
func simulatorSDKPath(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", "--sdk", "iphonesimulator", "--show-sdk-path").Output()
	if err != nil {
		t.Skipf("Xcode not available: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// writeFixtureFile writes Swift source to a file and returns its path.
func writeFixtureFile(t *testing.T, dir, fileName, source string) string {
	t.Helper()
	p := filepath.Join(dir, fileName)
	if err := os.WriteFile(p, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// buildFixtureModule compiles Swift sources into a .swiftmodule with an index store.
// Returns the module directory and an IndexStoreCache loaded from the built index store.
func buildFixtureModule(t *testing.T, srcFiles []string, moduleName, sdk string) (string, *analysis.IndexStoreCache) {
	t.Helper()
	outDir := t.TempDir()
	indexStorePath := filepath.Join(outDir, "index-store")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args := []string{
		"xcrun", "swiftc",
		"-emit-module",
		"-emit-module-path", filepath.Join(outDir, moduleName+".swiftmodule"),
		"-module-name", moduleName,
		"-parse-as-library",
		"-sdk", sdk,
		"-target", compileTestTarget,
		"-enable-testing",
		"-Xfrontend", "-enable-implicit-dynamic",
		"-Xfrontend", "-enable-private-imports",
		"-index-store-path", indexStorePath,
	}
	args = append(args, srcFiles...)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building fixture module: %v\n%s", err, out)
	}

	// Load Index Store cache for source file parsing.
	// Use the first source file's directory as source root.
	sourceRoot := filepath.Dir(srcFiles[0])
	cache, cacheErr := analysis.LoadIndexStore(ctx, indexStorePath, sourceRoot)
	if cacheErr != nil {
		t.Fatalf("loading index store: %v", cacheErr)
	}

	return outDir, cache
}

// typecheckGeneratedThunks runs swiftc -typecheck on the generated thunk files
// against the fixture module to verify they are valid Swift.
func typecheckGeneratedThunks(t *testing.T, thunkPaths []string, moduleDir, moduleName, sdk string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	args := []string{
		"xcrun", "swiftc",
		"-typecheck",
		"-sdk", sdk,
		"-target", compileTestTarget,
		"-enable-testing",
		"-I", moduleDir,
		"-module-name", moduleName + "_PreviewReplacement_Test_0",
		"-parse-as-library",
		"-Xfrontend", "-disable-previous-implementation-calls-in-dynamic-replacements",
		"-Xfrontend", "-enable-private-imports",
	}
	args = append(args, thunkPaths...)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var thunkSources []string
		for _, p := range thunkPaths {
			src, _ := os.ReadFile(p)
			thunkSources = append(thunkSources, "--- "+filepath.Base(p)+" ---\n"+string(src))
		}
		t.Errorf("thunk typecheck failed:\n%s\n\nThunk sources:\n%s", out, strings.Join(thunkSources, "\n"))
	}
}

// stripPreviewBlocks removes #Preview { ... } blocks from Swift source.
// Used to build fixture modules without preview macros that may require
// a newer SDK or macro plugin. Only used in tests; relies on well-formed
// fixtures with balanced braces.
func stripPreviewBlocks(src string) string {
	lines := strings.Split(src, "\n")
	var result []string
	inPreview := false
	braceDepth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inPreview && strings.HasPrefix(trimmed, "#Preview") {
			inPreview = true
			braceDepth = strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inPreview = false
			}
			continue
		}
		if inPreview {
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inPreview = false
			}
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// --- Fixture Swift sources ---
//
// Minimal, self-contained Swift files that exercise different
// thunk template code paths. #Preview blocks are stripped before
// module build (swiftc -emit-module) but retained for axe-parser
// parsing and thunk generation.

// Case 1 & 11: body-only View, no #Preview.
// Verifies basic @_dynamicReplacement and empty axe_preview_refresh.
const fixtureSimpleView = `import SwiftUI

struct SimpleView: View {
    var body: some View {
        Text("Hello")
    }
}
`

// Case 2: multiple computed properties in one type.
const fixtureMultipleProperties = `import SwiftUI

struct FooView: View {
    var backgroundColor: Color {
        Color.blue
    }
    var body: some View {
        Text("Hello")
            .background(backgroundColor)
    }
}
`

// Case 3: method with parameters.
// Verifies @_dynamicReplacement(for: selector) + signature match.
const fixtureWithMethod = `import SwiftUI

struct GreetView: View {
    var body: some View {
        Text(greet(name: "World"))
    }

    func greet(name: String) -> String {
        "Hello, \(name)"
    }
}
`

// Case 4: nested type (Outer.Inner).
// Verifies extension OuterView.InnerView {} is valid Swift.
const fixtureNestedType = `import SwiftUI

struct OuterView: View {
    struct InnerView: View {
        var body: some View {
            Text("Inner")
        }
    }
    var body: some View {
        InnerView()
    }
}
`

// Case 5: simple #Preview block.
// Verifies _AxePreviewWrapper + import struct generation.
const fixtureWithPreview = `import SwiftUI

struct PreviewableView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    PreviewableView()
}
`

// Case 6: @Previewable @State in #Preview.
// Verifies wrapper struct property generation.
const fixturePreviewableState = `import SwiftUI

struct StatefulView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    @Previewable @State var text = "Hello"
    StatefulView()
}
`

// Case 7: @Previewable @Binding -> @State conversion.
const fixturePreviewableBinding = `import SwiftUI

struct BindingView: View {
    @Binding var isOn: Bool
    var body: some View {
        Toggle("Toggle", isOn: $isOn)
    }
}

#Preview {
    @Previewable @Binding var isOn = true
    BindingView(isOn: $isOn)
}
`

// Case 8: external import (MapKit).
// Verifies extra imports are propagated to the thunk.
const fixtureExternalImport = `import SwiftUI
import MapKit

struct MapView: View {
    var body: some View {
        Map()
    }
}
`

// Case 9 (parent): multi-file combined thunk.
const fixtureParentView = `import SwiftUI

struct ParentView: View {
    var body: some View {
        ChildView()
    }
}

#Preview {
    ParentView()
}
`

// Case 9 (child): dependency file for multi-file test.
const fixtureChildView = `import SwiftUI

struct ChildView: View {
    var body: some View {
        Text("Child")
    }
}
`

// Case 9b: 3-file combined thunk.
// Verifies multiple @_private imports and merged extensions.
const fixtureListView = `import SwiftUI

struct ListView: View {
    var body: some View {
        VStack {
            HeaderView()
            RowView()
        }
    }
}

#Preview {
    ListView()
}
`

const fixtureHeaderView = `import SwiftUI

struct HeaderView: View {
    var title: String { "Header" }
    var body: some View {
        Text(title)
    }
}
`

const fixtureRowView = `import SwiftUI

struct RowView: View {
    var body: some View {
        Text("Row")
    }
}
`

// Case 9c: dependency with method.
// Verifies @_dynamicReplacement for methods across files.
const fixtureCallerView = `import SwiftUI

struct CallerView: View {
    var body: some View {
        Text(HelperView().format(value: 42))
    }
}

#Preview {
    CallerView()
}
`

const fixtureHelperWithMethod = `import SwiftUI

struct HelperView: View {
    var body: some View {
        Text("Helper")
    }

    func format(value: Int) -> String {
        "Value: \(value)"
    }
}
`

// Case 9d: dependency with external import.
// Verifies extra imports are merged from all files.
const fixtureMainView = `import SwiftUI

struct MainView: View {
    var body: some View {
        VStack {
            MapDetailView()
        }
    }
}

#Preview {
    MainView()
}
`

const fixtureMapDetailView = `import SwiftUI
import MapKit

struct MapDetailView: View {
    var body: some View {
        Map()
    }
}
`

// Case 9e: non-View dependency across files.
// Verifies import struct is only generated for target View types,
// not for helper types in dependency files.
const fixtureDisplayView = `import SwiftUI

struct DisplayView: View {
    var body: some View {
        Text(Formatter().formatted)
    }
}

#Preview {
    DisplayView()
}
`

const fixtureFormatter = `import SwiftUI

struct Formatter {
    var formatted: String {
        "formatted"
    }
}
`

// Case A: method with no parameters.
// Verifies @_dynamicReplacement(for: refresh()) selector matching.
const fixtureMethodNoParams = `import SwiftUI

struct RefreshView: View {
    var body: some View {
        Text("Hello")
    }

    func refresh() {
        print("refreshing")
    }
}
`

// Case A': method with underscore labels.
// Verifies @_dynamicReplacement(for: add(_:_:)) selector matching.
const fixtureMethodUnderscoreLabel = `import SwiftUI

struct CalcView: View {
    var body: some View {
        Text("Calc")
    }

    func add(_ a: Int, _ b: Int) -> Int {
        a + b
    }
}
`

// Case B: multiple views in one file with #Preview.
// Verifies import struct for both types and preview wrapper resolution.
const fixtureMultipleViewsWithPreview = `import SwiftUI

struct AlphaView: View {
    var body: some View {
        TextField("", text: .constant(""))
    }
}

struct BetaView: View {
    var body: some View {
        SecureField("", text: .constant(""))
    }
}

#Preview {
    AlphaView()
}
`

// Case C: multiple @Previewable declarations.
// Verifies wrapper struct with multiple properties compiles.
const fixtureMultiplePreviewables = `import SwiftUI

struct MultiPropView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    @Previewable @State var name = "World"
    @Previewable @State var count = 42
    MultiPropView()
}
`

// Case D: path with special characters.
// The source itself is normal; the test writes it to a directory
// with quotes and backslashes to verify escapeSwiftString produces
// valid Swift string literals at compile time.
const fixturePathEscaping = `import SwiftUI

struct EscapeView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    EscapeView()
}
`

// Case 10: non-View type alongside a View.
// Verifies import struct is only generated for View-conforming types.
const fixtureNonViewType = `import SwiftUI

struct DataHelper {
    var formatted: String {
        "formatted"
    }
}

struct DataView: View {
    var body: some View {
        Text("Data")
    }
}

#Preview {
    DataView()
}
`

// Private type collision fixtures.
// Two files define the same private struct name. With per-file thunks,
// each file only imports its own @_private scope, so there is no collision.
const fixtureCollisionFileA = `import SwiftUI

private struct DataFormatter {
    var text: String { "formatted A" }
}

struct CollisionViewA: View {
    var body: some View {
        Text(DataFormatter().text)
    }
}
`

const fixtureCollisionFileB = `import SwiftUI

private struct DataFormatter {
    var text: String { "formatted B" }
}

struct CollisionViewB: View {
    var body: some View {
        Text(DataFormatter().text)
    }
}
`

// Case G: delegate method in protocol conformance extension.
// Verifies that a method declared in an extension (for protocol conformance)
// is correctly generated as a @_dynamicReplacement func (not var).
// Bug: if the method is mis-identified as a property, the parameter "error"
// disappears from scope, and Swift 6 confuses bare "error" with the #error macro.
const fixtureDelegateProtocol = `import Foundation

protocol ErrorHandler {
    func handleError(error: any Error)
}
`

const fixtureDelegateView = `import SwiftUI

enum AppError: Error, Equatable {
    case notFound
}

class DelegateViewModel: ObservableObject {
    // Stored property with the same name as the delegate method.
    // Index Store reports this as kind=INSTANCE_PROPERTY with isComputed=true,
    // causing combineWithIndexStore to misidentify the method as a property.
    var handleError: ((any Error) -> Void)? = nil
}

extension DelegateViewModel: ErrorHandler {
    func handleError(error: any Error) {
        if case AppError.notFound = error {
            print("not found")
        }
    }
}

struct DelegateView: View {
    @StateObject var vm = DelegateViewModel()
    var body: some View {
        Text("Hello")
    }
}
`

// Case F: catch block enum case pattern matching.
const fixtureErrorCatchPattern = `import SwiftUI

enum DataError: Error {
    case notFound
    case permissionDenied
}

struct CatchPatternView: View {
    var body: some View {
        Text("Hello")
    }

    func loadData() throws -> String {
        do {
            throw DataError.notFound
        } catch DataError.notFound {
            return "Not Found"
        } catch DataError.permissionDenied {
            return "Permission Denied"
        }
    }
}
`

// Case F2: "if case" pattern matching on Error existential.
// "if case SomeEnum.case = error" is the idiomatic way to match
// errors in Swift without a custom ~= operator.
const fixtureErrorIfCasePattern = `import SwiftUI

enum AppError: Error {
    case networkFailure
    case timeout
}

struct IfCaseErrorView: View {
    var body: some View {
        Text("Hello")
    }

    func describeError(error: Error) -> String {
        if case AppError.networkFailure = error {
            return "Network Failure"
        } else if case AppError.timeout = error {
            return "Timeout"
        }
        return "Unknown"
    }
}
`

// Case F3: switch pattern matching on Error existential.
const fixtureErrorSwitchPattern = `import SwiftUI

enum FetchError: Error {
    case badURL
    case unauthorized
    case serverError
}

struct SwitchErrorView: View {
    var body: some View {
        Text("Hello")
    }

    func errorMessage(for error: Error) -> String {
        switch error {
        case FetchError.badURL:
            return "Bad URL"
        case FetchError.unauthorized:
            return "Unauthorized"
        case FetchError.serverError:
            return "Server Error"
        default:
            return "Unknown"
        }
    }
}
`

// Case F4: Equatable ~= with same-type operands (standard library ~=).
// When both sides are the same Equatable type, the standard ~= applies.
const fixtureEquatablePatternMatching = `import SwiftUI

enum Status: Equatable {
    case active
    case inactive
}

struct StatusView: View {
    var body: some View {
        Text("Hello")
    }

    func isActive(status: Status) -> Bool {
        Status.active ~= status
    }
}
`

// Case F5: explicit ~= with the implicit catch block "error" variable.
// Reproduces the reported pattern: if (myError ~= error) {}
// where error is the implicit catch variable.
const fixtureExplicitTildeEqWithImplicitError = `import SwiftUI

enum MyError: Error, Equatable {
    case notFound
    case unauthorized
}

func ~= (pattern: MyError, value: Error) -> Bool {
    guard let value = value as? MyError else { return false }
    return pattern == value
}

struct TildeEqView: View {
    var body: some View {
        Text("Hello")
    }

    func doWork() -> String {
        do {
            throw MyError.notFound
        } catch {
            if (MyError.notFound ~= error) {
                return "Not Found"
            }
        }
        return "OK"
    }
}
`

// Case F6: cross-file error catch pattern matching.
// Error type is defined in a separate file from the View.
const fixtureErrorTypesCrossFile = `import Foundation

enum NetworkError: Error {
    case connectionLost
    case timeout
}
`

const fixtureCrossFileErrorView = `import SwiftUI

struct CrossFileErrorView: View {
    var body: some View {
        Text("Hello")
    }

    func describeError(error: Error) -> String {
        switch error {
        case NetworkError.connectionLost:
            return "Connection Lost"
        case NetworkError.timeout:
            return "Timeout"
        default:
            return "Error"
        }
    }
}

#Preview {
    CrossFileErrorView()
}
`

// Case E: #Preview referencing a private type.
// The main thunk uses @_private(sourceFile:) import for the target file,
// so private types defined in that file are visible in the preview wrapper.
const fixturePreviewWithPrivateType = `import SwiftUI

private struct PrivateContent: View {
    var body: some View {
        Text("Private")
    }
}

struct WrapperView: View {
    var body: some View {
        PrivateContent()
    }
}

#Preview {
    PrivateContent()
}
`

// --- Test ---

// TestThunkCompilation verifies that thunks generated by codegen.GenerateThunks
// pass swiftc -typecheck against a fixture module.
// This is a compilation integration test (category A in the design doc):
// it catches template bugs (syntax errors, selector mismatches, missing
// imports) that string-matching tests cannot detect.
func TestThunkCompilation(t *testing.T) {
	sdk := simulatorSDKPath(t)

	tests := []struct {
		name    string
		sources map[string]string // fileName -> Swift source
		target  string            // target fileName for thunk generation
	}{
		{
			name:    "SimpleView_NoPreview",
			sources: map[string]string{"SimpleView.swift": fixtureSimpleView},
			target:  "SimpleView.swift",
		},
		{
			name:    "MultipleProperties",
			sources: map[string]string{"FooView.swift": fixtureMultipleProperties},
			target:  "FooView.swift",
		},
		{
			name:    "WithMethod",
			sources: map[string]string{"GreetView.swift": fixtureWithMethod},
			target:  "GreetView.swift",
		},
		{
			name:    "NestedType",
			sources: map[string]string{"OuterView.swift": fixtureNestedType},
			target:  "OuterView.swift",
		},
		{
			name:    "WithPreview",
			sources: map[string]string{"PreviewableView.swift": fixtureWithPreview},
			target:  "PreviewableView.swift",
		},
		{
			name:    "PreviewableState",
			sources: map[string]string{"StatefulView.swift": fixturePreviewableState},
			target:  "StatefulView.swift",
		},
		{
			name:    "PreviewableBinding",
			sources: map[string]string{"BindingView.swift": fixturePreviewableBinding},
			target:  "BindingView.swift",
		},
		{
			name:    "ExternalImport",
			sources: map[string]string{"MapView.swift": fixtureExternalImport},
			target:  "MapView.swift",
		},
		{
			name: "MultiFile",
			sources: map[string]string{
				"ParentView.swift": fixtureParentView,
				"ChildView.swift":  fixtureChildView,
			},
			target: "ParentView.swift",
		},
		{
			name: "MultiFile_ThreeFiles",
			sources: map[string]string{
				"ListView.swift":   fixtureListView,
				"HeaderView.swift": fixtureHeaderView,
				"RowView.swift":    fixtureRowView,
			},
			target: "ListView.swift",
		},
		{
			name: "MultiFile_DepWithMethod",
			sources: map[string]string{
				"CallerView.swift": fixtureCallerView,
				"HelperView.swift": fixtureHelperWithMethod,
			},
			target: "CallerView.swift",
		},
		{
			name: "MultiFile_DepWithExternalImport",
			sources: map[string]string{
				"MainView.swift":      fixtureMainView,
				"MapDetailView.swift": fixtureMapDetailView,
			},
			target: "MainView.swift",
		},
		{
			name: "MultiFile_NonViewDep",
			sources: map[string]string{
				"DisplayView.swift": fixtureDisplayView,
				"Formatter.swift":   fixtureFormatter,
			},
			target: "DisplayView.swift",
		},
		{
			name:    "MethodNoParams",
			sources: map[string]string{"RefreshView.swift": fixtureMethodNoParams},
			target:  "RefreshView.swift",
		},
		{
			name:    "MethodUnderscoreLabel",
			sources: map[string]string{"CalcView.swift": fixtureMethodUnderscoreLabel},
			target:  "CalcView.swift",
		},
		{
			name:    "MultipleViewsWithPreview",
			sources: map[string]string{"Views.swift": fixtureMultipleViewsWithPreview},
			target:  "Views.swift",
		},
		{
			name:    "MultiplePreviewables",
			sources: map[string]string{"MultiPropView.swift": fixtureMultiplePreviewables},
			target:  "MultiPropView.swift",
		},
		{
			name:    "NonViewType",
			sources: map[string]string{"DataView.swift": fixtureNonViewType},
			target:  "DataView.swift",
		},
		{
			name: "PrivateTypeCollision",
			sources: map[string]string{
				"CollisionViewA.swift": fixtureCollisionFileA,
				"CollisionViewB.swift": fixtureCollisionFileB,
			},
			target: "CollisionViewA.swift",
		},
		{
			name:    "PreviewWithPrivateType",
			sources: map[string]string{"WrapperView.swift": fixturePreviewWithPrivateType},
			target:  "WrapperView.swift",
		},
		{
			name: "DelegateMethodInExtension",
			sources: map[string]string{
				"ErrorHandler.swift": fixtureDelegateProtocol,
				"DelegateView.swift": fixtureDelegateView,
			},
			target: "DelegateView.swift",
		},
		{
			name:    "ErrorCatchPattern",
			sources: map[string]string{"CatchPatternView.swift": fixtureErrorCatchPattern},
			target:  "CatchPatternView.swift",
		},
		{
			name:    "ErrorIfCasePattern",
			sources: map[string]string{"IfCaseErrorView.swift": fixtureErrorIfCasePattern},
			target:  "IfCaseErrorView.swift",
		},
		{
			name:    "ErrorSwitchPattern",
			sources: map[string]string{"SwitchErrorView.swift": fixtureErrorSwitchPattern},
			target:  "SwitchErrorView.swift",
		},
		{
			name:    "EquatablePatternMatching",
			sources: map[string]string{"StatusView.swift": fixtureEquatablePatternMatching},
			target:  "StatusView.swift",
		},
		{
			name: "CrossFileErrorPattern",
			sources: map[string]string{
				"ErrorTypes.swift":         fixtureErrorTypesCrossFile,
				"CrossFileErrorView.swift": fixtureCrossFileErrorView,
			},
			target: "CrossFileErrorView.swift",
		},
		{
			name:    "ExplicitTildeEqWithImplicitError",
			sources: map[string]string{"TildeEqView.swift": fixtureExplicitTildeEqWithImplicitError},
			target:  "TildeEqView.swift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parseDir := t.TempDir()
			moduleSrcDir := t.TempDir()

			// Write full sources to parseDir (for axe-parser).
			for name, src := range tt.sources {
				writeFixtureFile(t, parseDir, name, src)
			}

			// Write stripped sources (no #Preview) to moduleSrcDir and build module.
			// #Preview blocks are stripped because the preview macros may not be
			// available at module-build time, but they are parsed by axe-parser
			// (which uses swift-syntax, not swiftc) for thunk generation.
			var moduleSrcPaths []string
			for name, src := range tt.sources {
				stripped := stripPreviewBlocks(src)
				moduleSrcPaths = append(moduleSrcPaths, writeFixtureFile(t, moduleSrcDir, name, stripped))
			}
			moduleDir, cache := buildFixtureModule(t, moduleSrcPaths, compileTestModuleName, sdk)

			// Generate thunks.
			// Use the parse directory sources (with #Preview blocks) for axe-parser,
			// but use the cache from the module build (stripped sources) for Index Store data.
			// The cache is keyed by module source paths; we need to remap to parse paths.
			targetPath := filepath.Join(parseDir, tt.target)
			dirs := previewDirs{Thunk: filepath.Join(t.TempDir(), "thunk")}

			// Build a remapped cache: moduleSrcDir paths → parseDir paths.
			remappedFiles := make(map[string]*pb.IndexFileData)
			for name := range tt.sources {
				parsePath := filepath.Join(parseDir, name)
				modulePath := filepath.Join(moduleSrcDir, name)
				if data := cache.FileData(modulePath); data != nil {
					remappedFiles[parsePath] = data
				}
			}
			remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

			var files []analysis.FileThunkData
			for name := range tt.sources {
				path := filepath.Join(parseDir, name)
				var types []analysis.TypeInfo
				var imports []string
				var err error
				if name == tt.target {
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
			thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, dirs.Thunk, "", targetPath, 0)
			if err != nil {
				t.Fatal(err)
			}

			// Typecheck the generated thunks against the fixture module.
			typecheckGeneratedThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
		})
	}
}

// TestThunkCompilation_PathEscaping verifies that escapeSwiftString produces
// valid Swift string literals when the source file path contains quotes and
// backslashes. This is a separate test because it requires a directory with
// special characters that cannot be expressed via the table-driven approach.
func TestThunkCompilation_PathEscaping(t *testing.T) {
	sdk := simulatorSDKPath(t)

	// Create a directory whose name contains characters needing Swift escaping.
	base := t.TempDir()
	weirdDir := filepath.Join(base, `path with "quotes"`)
	if err := os.MkdirAll(weirdDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Module is built from a normal directory (module source path doesn't matter).
	moduleSrcDir := t.TempDir()
	moduleSrc := stripPreviewBlocks(fixturePathEscaping)
	moduleSrcPath := writeFixtureFile(t, moduleSrcDir, "EscapeView.swift", moduleSrc)
	moduleDir, cache := buildFixtureModule(t, []string{moduleSrcPath}, compileTestModuleName, sdk)

	// Write the parse source to the weird directory.
	srcPath := writeFixtureFile(t, weirdDir, `My\View.swift`, fixturePathEscaping)

	// Remap cache from module source path to parse source path.
	remappedFiles := make(map[string]*pb.IndexFileData)
	if data := cache.FileData(moduleSrcPath); data != nil {
		remappedFiles[srcPath] = data
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	types, imports, err := analysis.SourceFile(srcPath, remappedCache)
	if err != nil {
		t.Fatal(err)
	}

	dirs := previewDirs{Thunk: filepath.Join(t.TempDir(), "thunk")}
	files := []analysis.FileThunkData{
		{
			FileName:   filepath.Base(srcPath),
			AbsPath:    srcPath,
			Types:      types,
			Imports:    imports,
			ModuleName: remappedCache.FileModuleName(srcPath),
		},
	}
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, dirs.Thunk, "", srcPath, 0)
	if err != nil {
		t.Fatal(err)
	}

	typecheckGeneratedThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
}
