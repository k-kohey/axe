package preview

// Known bugs in thunk generation.
//
// Each test documents a specific bug that currently exists.
// Assertions verify the bug IS present (tests PASS in current broken state).
// When a bug is fixed, the corresponding test will FAIL — update the assertion
// to verify correct behavior and remove the TODO.
//
// See docs/thunk-generation-limits.md for architectural analysis.

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

// typecheckThunks runs swiftc -typecheck and returns the error (nil if success).
// Unlike typecheckGeneratedThunks, this does NOT call t.Error on failure.
func typecheckThunks(t *testing.T, thunkPaths []string, moduleDir, moduleName, sdk string) error {
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
		return &thunkTypecheckError{output: string(out), err: err}
	}
	return nil
}

type thunkTypecheckError struct {
	output string
	err    error
}

func (e *thunkTypecheckError) Error() string {
	return e.output
}

// generateThunksForTest generates thunks without typechecking.
// Returns thunkPaths, moduleDir, or calls t.Fatal on setup failure.
func generateThunksForTest(t *testing.T, sdk string, sources map[string]string, target string) (thunkPaths []string, moduleDir string) {
	t.Helper()

	parseDir := t.TempDir()
	moduleSrcDir := t.TempDir()

	for name, src := range sources {
		writeFixtureFile(t, parseDir, name, src)
	}

	var moduleSrcPaths []string
	for name, src := range sources {
		stripped := stripPreviewBlocks(src)
		moduleSrcPaths = append(moduleSrcPaths, writeFixtureFile(t, moduleSrcDir, name, stripped))
	}
	var cache *analysis.IndexStoreCache
	moduleDir, cache = buildFixtureModule(t, moduleSrcPaths, compileTestModuleName, sdk)

	targetPath := filepath.Join(parseDir, target)
	thunkDir := filepath.Join(t.TempDir(), "thunk")

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
	return thunkPaths, moduleDir
}

// thunkContains checks if any per-file thunk contains the given substring.
func thunkContains(t *testing.T, thunkPaths []string, substr string) bool {
	t.Helper()
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), substr) {
			return true
		}
	}
	return false
}

// thunkContainsCount counts occurrences of substr across all per-file thunks.
func thunkContainsCount(t *testing.T, thunkPaths []string, substr string) int {
	t.Helper()
	count := 0
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		count += strings.Count(string(data), substr)
	}
	return count
}

// ============================================================
// Category C: @Previewable @FocusState の型不整合
//
// TransformPreviewBlock() は @FocusState → @State 変換に対応済みだが、
// @FocusState を受け取る側 (FocusFormView(focused:)) が FocusState<T> 型の
// 引数を期待するため、@State で宣言した T 型では型が合わない。
//
// @Binding の場合は $x で Binding<T> を渡せるが、@FocusState には
// 対応する projected value のパターンが異なるため単純な wrapper 置換では不十分。
// NOTE: 本質的には #Preview の @Previewable 展開自体をコンパイラプラグインが
// 処理するため、axe 側での完全再現は非公開仕様に依存する。
// @FocusState を @Previewable で使用するパターンは実プロジェクトで稀であり、
// 優先度は低い。
// ============================================================

func TestKnownBug_PreviewableFocusState(t *testing.T) {
	sdk := simulatorSDKPath(t)

	fixture := `import SwiftUI

struct FocusFormView: View {
    enum Field {
        case email
    }

    @FocusState var focused: Field?

    var body: some View {
        TextField("Email", text: .constant(""))
            .focused($focused, equals: .email)
    }
}

#Preview {
    @Previewable @FocusState var focused: FocusFormView.Field?
    FocusFormView(focused: focused)
}
`
	thunkPaths, moduleDir := generateThunksForTest(t, sdk,
		map[string]string{"FocusFormView.swift": fixture},
		"FocusFormView.swift",
	)

	// BUG: @FocusState → @State 変換は行われるが、FocusFormView(focused:) の
	// 引数型が FocusState<Field?> を期待するため型不整合でコンパイルエラー。
	// wrapper 置換だけでは解決できず、呼び出し側の引数変換も必要。
	err := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
	if err == nil {
		t.Fatal("Bug fixed: @Previewable @FocusState now compiles. Update this test.")
	}
	t.Logf("Expected compile error: %v", err)
}

// ============================================================
// Category D: thunk のスコープ再現の不完全性
//
// @_private(sourceFile:) の basename 制約、import のユニオン化未対応、
// extension-only ファイルの SourceFile 要件不適合。
//
// TODO(D-1): VFS overlay で仮想 basename を一意化する（要検証）。
//            暫定策として basename 衝突の検出と早期警告を実装。
// TODO(D-2): SourceFile の要件を緩和し、#Preview があれば受け入れる。
// TODO(D-3): import をターゲット+依存ファイルのユニオンにする。
// ============================================================

func TestKnownBug_SameBasenameDifferentDirs(t *testing.T) {
	sdk := simulatorSDKPath(t)

	fixtureA := `import SwiftUI

private struct LocalStyle {
    var color: Color { .blue }
}

struct ItemViewA: View {
    var styled: Color { LocalStyle().color }
    var body: some View {
        Text("A").foregroundColor(styled)
    }
}
`
	fixtureB := `import SwiftUI

private struct LocalStyle {
    var color: Color { .red }
}

struct ItemViewB: View {
    var styled: Color { LocalStyle().color }
    var body: some View {
        Text("B").foregroundColor(styled)
    }
}
`
	fixtureTarget := `import SwiftUI

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

	// Set up two "ItemView.swift" files in different directories.
	parseDir := t.TempDir()
	dirA := filepath.Join(parseDir, "featureA")
	dirB := filepath.Join(parseDir, "featureB")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	srcA := filepath.Join(dirA, "ItemView.swift")
	srcB := filepath.Join(dirB, "ItemView.swift")
	srcTarget := filepath.Join(parseDir, "SameBaseHost.swift")
	if err := os.WriteFile(srcA, []byte(fixtureA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte(fixtureB), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcTarget, []byte(fixtureTarget), 0o644); err != nil {
		t.Fatal(err)
	}

	moduleSrcDir := t.TempDir()
	modA := writeFixtureFile(t, moduleSrcDir, "ItemViewA.swift", stripPreviewBlocks(fixtureA))
	modB := writeFixtureFile(t, moduleSrcDir, "ItemViewB.swift", stripPreviewBlocks(fixtureB))
	modTarget := writeFixtureFile(t, moduleSrcDir, "SameBaseHost.swift", stripPreviewBlocks(fixtureTarget))
	moduleDir, cache := buildFixtureModule(t, []string{modA, modB, modTarget}, compileTestModuleName, sdk)

	remappedFiles := make(map[string]*pb.IndexFileData)
	if d := cache.FileData(modA); d != nil {
		remappedFiles[srcA] = d
	}
	if d := cache.FileData(modB); d != nil {
		remappedFiles[srcB] = d
	}
	if d := cache.FileData(modTarget); d != nil {
		remappedFiles[srcTarget] = d
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	typesA, importsA, err := analysis.DependencyFile(srcA, remappedCache)
	if err != nil {
		t.Fatal(err)
	}
	typesB, importsB, err := analysis.DependencyFile(srcB, remappedCache)
	if err != nil {
		t.Fatal(err)
	}
	typesTarget, importsTarget, err := analysis.SourceFile(srcTarget, remappedCache)
	if err != nil {
		t.Fatal(err)
	}

	files := []analysis.FileThunkData{
		{FileName: "ItemView.swift", AbsPath: srcA, Types: typesA, Imports: importsA},
		{FileName: "ItemView.swift", AbsPath: srcB, Types: typesB, Imports: importsB},
		{FileName: "SameBaseHost.swift", AbsPath: srcTarget, Types: typesTarget, Imports: importsTarget},
	}

	thunkDir := filepath.Join(t.TempDir(), "thunk")
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, thunkDir, "", srcTarget, 0)
	if err != nil {
		t.Fatal(err)
	}

	// BUG: 両方の thunk が @_private(sourceFile: "ItemView.swift") を使用
	// → 同名 private 型 LocalStyle が衝突してコンパイルエラー
	tcErr := typecheckThunks(t, thunkPaths, moduleDir, compileTestModuleName, sdk)
	if tcErr == nil {
		t.Fatal("Bug fixed: same-basename files no longer cause compile error. Update this test.")
	}
	t.Logf("Expected compile error: %v", tcErr)
}

// ============================================================
// Category E: case-insensitive FS での thunk ファイル衝突
//
// macOS のデフォルト APFS は case-insensitive。
// "ItemView.swift" と "itemView.swift" の thunk が同一ファイルに上書きされる。
//
// TODO: thunk ファイル名生成時に case-insensitive 衝突を検出し、
//       サフィックスを付与して一意化する。
// ============================================================

func TestKnownBug_CaseInsensitiveFileCollision(t *testing.T) {
	// Detect case sensitivity of the filesystem.
	tmpDir := t.TempDir()
	testUpper := filepath.Join(tmpDir, "CaseTest.txt")
	testLower := filepath.Join(tmpDir, "casetest.txt")
	if err := os.WriteFile(testUpper, []byte("upper"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testLower, []byte("lower"), 0o644); err != nil {
		t.Fatal(err)
	}
	upperData, _ := os.ReadFile(testUpper)
	if string(upperData) != "lower" {
		t.Skip("Filesystem is case-sensitive — this bug only manifests on case-insensitive FS")
	}

	sdk := simulatorSDKPath(t)

	fixtureUpper := `import SwiftUI

struct UpperItemView: View {
    var body: some View { Text("Upper") }
}
`
	fixtureLower := `import SwiftUI

struct LowerItemView: View {
    var body: some View { Text("Lower") }
}
`

	parseDir := t.TempDir()
	moduleSrcDir := t.TempDir()
	dirA := filepath.Join(parseDir, "upper")
	dirB := filepath.Join(parseDir, "lower")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	srcPathA := filepath.Join(dirA, "ItemView.swift")
	srcPathB := filepath.Join(dirB, "itemView.swift")
	if err := os.WriteFile(srcPathA, []byte(fixtureUpper), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPathB, []byte(fixtureLower), 0o644); err != nil {
		t.Fatal(err)
	}

	modA := filepath.Join(moduleSrcDir, "ItemView.swift")
	modB := filepath.Join(moduleSrcDir, "itemView2.swift")
	if err := os.WriteFile(modA, []byte(stripPreviewBlocks(fixtureUpper)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modB, []byte(stripPreviewBlocks(fixtureLower)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cache := buildFixtureModule(t, []string{modA, modB}, compileTestModuleName, sdk)

	remappedFiles := make(map[string]*pb.IndexFileData)
	if data := cache.FileData(modA); data != nil {
		remappedFiles[srcPathA] = data
	}
	if data := cache.FileData(modB); data != nil {
		remappedFiles[srcPathB] = data
	}
	remappedCache := analysis.NewIndexStoreCache(remappedFiles, map[string][]string{})

	typesA, importsA, _ := analysis.DependencyFile(srcPathA, remappedCache)
	typesB, importsB, _ := analysis.DependencyFile(srcPathB, remappedCache)

	files := []analysis.FileThunkData{
		{FileName: "ItemView.swift", AbsPath: srcPathA, Types: typesA, Imports: importsA},
		{FileName: "itemView.swift", AbsPath: srcPathB, Types: typesB, Imports: importsB},
	}

	thunkDir := filepath.Join(t.TempDir(), "thunk")
	thunkPaths, err := codegen.GenerateThunks(files, compileTestModuleName, thunkDir, "", srcPathA, 0)
	if err != nil {
		t.Fatal(err)
	}

	// BUG: case-insensitive FS で thunk_0_ItemView.swift と thunk_0_itemView.swift が衝突。
	// 後者が前者を上書きし、UpperItemView の thunk が消失する。
	perFileThunks := []string{}
	for _, p := range thunkPaths {
		if !strings.Contains(filepath.Base(p), "_main") {
			perFileThunks = append(perFileThunks, p)
		}
	}

	if len(perFileThunks) >= 2 {
		data0, _ := os.ReadFile(perFileThunks[0])
		data1, _ := os.ReadFile(perFileThunks[1])
		if string(data0) == string(data1) {
			t.Log("BUG confirmed: case-insensitive FS collision — both thunks have identical content")
			return
		}
	}
	t.Fatal("Bug fixed: case-insensitive file collision no longer occurs. Update this test.")
}
