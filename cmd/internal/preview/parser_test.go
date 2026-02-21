package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSourceFile(t *testing.T) {
	src := `import SwiftUI
import SomeFramework

struct HogeView: View {
    var body: some View {
        Map()
            .edgesIgnoringSafeArea(.all)
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, imports, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	ti := types[0]
	if ti.Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", ti.Name)
	}
	if len(ti.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(ti.Properties))
	}

	body := ti.Properties[0]
	if body.Name != "body" {
		t.Errorf("Property name = %q, want body", body.Name)
	}
	if body.BodyLine != 6 {
		t.Errorf("BodyLine = %d, want 6", body.BodyLine)
	}
	if len(imports) != 1 || imports[0] != "import SomeFramework" {
		t.Errorf("Imports = %v, want [import SomeFramework]", imports)
	}
}

func TestParseSourceFile_NoView(t *testing.T) {
	src := `import SwiftUI

struct NotAView {
    var name: String
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "NotAView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := parseSourceFile(path)
	if err == nil {
		t.Fatal("expected error for file without View struct")
	}
}

func TestParseSourceFile_MultipleProperties(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var backgroundColor: Color {
        Color.blue
    }
    var body: some View {
        Text("Hello")
            .background(backgroundColor)
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	ti := types[0]
	if ti.Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", ti.Name)
	}
	if len(ti.Properties) != 2 {
		t.Fatalf("Properties count = %d, want 2", len(ti.Properties))
	}
	if ti.Properties[0].Name != "backgroundColor" {
		t.Errorf("Properties[0].Name = %q, want backgroundColor", ti.Properties[0].Name)
	}
	if ti.Properties[1].Name != "body" {
		t.Errorf("Properties[1].Name = %q, want body", ti.Properties[1].Name)
	}
}

func TestParseSourceFile_MultipleViews(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        TextField("", text: .constant(""))
    }
}

struct FugaView: View {
    var body: some View {
        SecureField("", text: .constant(""))
    }
}

struct PiyoView: View {
    var body: some View {
        Text("Pick")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "Views.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 3 {
		t.Fatalf("types count = %d, want 3", len(types))
	}
	names := []string{types[0].Name, types[1].Name, types[2].Name}
	want := []string{"HogeView", "FugaView", "PiyoView"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("types[%d].Name = %q, want %q", i, n, want[i])
		}
	}
	// Each type should have a body property
	for i, ti := range types {
		if len(ti.Properties) != 1 || ti.Properties[0].Name != "body" {
			t.Errorf("types[%d] missing body property", i)
		}
	}
}

func TestParsePreviewBlocks(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := parsePreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 1 {
		t.Fatalf("blocks count = %d, want 1", len(blocks))
	}
	if blocks[0].StartLine != 9 {
		t.Errorf("StartLine = %d, want 9", blocks[0].StartLine)
	}
	if blocks[0].Title != "" {
		t.Errorf("Title = %q, want empty", blocks[0].Title)
	}
}

func TestParsePreviewBlocks_Named(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview("Light Mode") {
    MyView()
}

#Preview("Dark Mode") {
    MyView()
        .preferredColorScheme(.dark)
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := parsePreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 2 {
		t.Fatalf("blocks count = %d, want 2", len(blocks))
	}
	if blocks[0].Title != "Light Mode" {
		t.Errorf("blocks[0].Title = %q, want %q", blocks[0].Title, "Light Mode")
	}
	if blocks[1].Title != "Dark Mode" {
		t.Errorf("blocks[1].Title = %q, want %q", blocks[1].Title, "Dark Mode")
	}
	if !strings.Contains(blocks[1].Source, ".preferredColorScheme(.dark)") {
		t.Errorf("blocks[1].Source missing expected content: %q", blocks[1].Source)
	}
}

func TestParsePreviewBlocks_NamedWithTraits(t *testing.T) {
	src := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview("Landscape", traits: .landscapeLeft) {
    MyView()
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "MyView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	blocks, err := parsePreviewBlocks(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(blocks) != 1 {
		t.Fatalf("blocks count = %d, want 1", len(blocks))
	}
	if blocks[0].Title != "Landscape" {
		t.Errorf("Title = %q, want %q", blocks[0].Title, "Landscape")
	}
}

func TestSelectPreview_Empty(t *testing.T) {
	_, err := selectPreview(nil, "")
	if err == nil {
		t.Fatal("expected error for empty blocks")
	}
}

func TestSelectPreview_DefaultFirst(t *testing.T) {
	blocks := []previewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
		{StartLine: 5, Title: "B", Source: "ViewB()"},
	}
	b, err := selectPreview(blocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "A" {
		t.Errorf("Title = %q, want %q", b.Title, "A")
	}
}

func TestSelectPreview_ByIndex(t *testing.T) {
	blocks := []previewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
		{StartLine: 5, Title: "B", Source: "ViewB()"},
	}
	b, err := selectPreview(blocks, "1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "B" {
		t.Errorf("Title = %q, want %q", b.Title, "B")
	}
}

func TestSelectPreview_ByTitle(t *testing.T) {
	blocks := []previewBlock{
		{StartLine: 1, Title: "Light", Source: "ViewA()"},
		{StartLine: 5, Title: "Dark", Source: "ViewB()"},
	}
	b, err := selectPreview(blocks, "Dark")
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "Dark" {
		t.Errorf("Title = %q, want %q", b.Title, "Dark")
	}
}

func TestSelectPreview_IndexOutOfRange(t *testing.T) {
	blocks := []previewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
	}
	_, err := selectPreview(blocks, "5")
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestSelectPreview_TitleNotFound(t *testing.T) {
	blocks := []previewBlock{
		{StartLine: 1, Title: "A", Source: "ViewA()"},
	}
	_, err := selectPreview(blocks, "NonExistent")
	if err == nil {
		t.Fatal("expected error for unknown title")
	}
}

func TestTransformPreviewBlock_NoPreviewable(t *testing.T) {
	pb := previewBlock{
		StartLine: 1,
		Source:    "    MyView()",
	}
	tp := transformPreviewBlock(pb)
	if len(tp.Properties) != 0 {
		t.Errorf("Properties count = %d, want 0", len(tp.Properties))
	}
	if !strings.Contains(tp.BodySource, "MyView()") {
		t.Errorf("BodySource = %q, want to contain MyView()", tp.BodySource)
	}
}

func TestTransformPreviewBlock_WithState(t *testing.T) {
	pb := previewBlock{
		StartLine: 1,
		Source:    "    @Previewable @State var count = 0\n    HogeView(count: $count)",
	}
	tp := transformPreviewBlock(pb)
	if len(tp.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var count = 0" {
		t.Errorf("Property Source = %q, want %q", tp.Properties[0].Source, "@State var count = 0")
	}
	if !strings.Contains(tp.BodySource, "HogeView(count: $count)") {
		t.Errorf("BodySource = %q, want to contain HogeView(count: $count)", tp.BodySource)
	}
}

func TestTransformPreviewBlock_BindingToState(t *testing.T) {
	pb := previewBlock{
		StartLine: 1,
		Source:    "    @Previewable @Binding var isOn: Bool\n    HogeView(isOn: $isOn)",
	}
	tp := transformPreviewBlock(pb)
	if len(tp.Properties) != 1 {
		t.Fatalf("Properties count = %d, want 1", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var isOn: Bool" {
		t.Errorf("Property Source = %q, want %q", tp.Properties[0].Source, "@State var isOn: Bool")
	}
}

func TestTransformPreviewBlock_MultiplePreviewables(t *testing.T) {
	pb := previewBlock{
		StartLine: 1,
		Source:    "    @Previewable @State var name = \"World\"\n    @Previewable @State var count = 42\n    MyView(name: name, count: count)",
	}
	tp := transformPreviewBlock(pb)
	if len(tp.Properties) != 2 {
		t.Fatalf("Properties count = %d, want 2", len(tp.Properties))
	}
	if tp.Properties[0].Source != "@State var name = \"World\"" {
		t.Errorf("Properties[0].Source = %q", tp.Properties[0].Source)
	}
	if tp.Properties[1].Source != "@State var count = 42" {
		t.Errorf("Properties[1].Source = %q", tp.Properties[1].Source)
	}
}

func TestParseSourceFile_WithMethods(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	ti := types[0]
	if len(ti.Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(ti.Methods))
	}
	m := ti.Methods[0]
	if m.Name != "greet" {
		t.Errorf("Method.Name = %q, want greet", m.Name)
	}
	if m.Selector != "greet(name:)" {
		t.Errorf("Method.Selector = %q, want greet(name:)", m.Selector)
	}
	if m.Signature != "(name: String) -> String" {
		t.Errorf("Method.Signature = %q, want (name: String) -> String", m.Signature)
	}
	if !strings.Contains(m.Source, `return "Hello, \(name)"`) {
		t.Errorf("Method.Source = %q, want to contain return statement", m.Source)
	}
}

func TestParseSourceFile_SkipStaticMethod(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    static func create() -> HogeView {
        HogeView()
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 0 {
		t.Errorf("Methods count = %d, want 0 (static should be skipped)", len(types[0].Methods))
	}
}

func TestParseSourceFile_MethodNoParams(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func refresh() {
        print("refreshing")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "refresh()" {
		t.Errorf("Selector = %q, want refresh()", m.Selector)
	}
	if m.Signature != "()" {
		t.Errorf("Signature = %q, want ()", m.Signature)
	}
}

func TestParseSourceFile_MethodUnderscoreLabel(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Calc")
    }

    func add(_ a: Int, _ b: Int) -> Int {
        a + b
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "add(_:_:)" {
		t.Errorf("Selector = %q, want add(_:_:)", m.Selector)
	}
}

func TestParseSourceFile_MultiLineSignature(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }

    func configure(
        title: String,
        count: Int
    ) -> String {
        "\(title): \(count)"
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	types, _, err := parseSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types[0].Methods) != 1 {
		t.Fatalf("Methods count = %d, want 1", len(types[0].Methods))
	}
	m := types[0].Methods[0]
	if m.Selector != "configure(title:count:)" {
		t.Errorf("Selector = %q, want configure(title:count:)", m.Selector)
	}
	if !strings.Contains(m.Signature, "-> String") {
		t.Errorf("Signature = %q, want to contain -> String", m.Signature)
	}
}

// --- computeSkeleton tests ---

// writeTemp is a helper that writes content to a temp .swift file and returns its path.
// It also resets the parse cache to ensure the new content is re-parsed.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()
	return path
}

func TestComputeSkeleton_BodyOnlyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("World")
            .foregroundColor(.red)
    }
}

#Preview {
    MyView()
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("body-only change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_ImportAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI
import SomeFramework

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("import addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_StoredPropertyAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    @State var count = 0

    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("stored property addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_StructAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	modified := `import SwiftUI

struct FugaView: View {
    var body: some View {
        Image(systemName: "star")
    }
}

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("struct addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_PreviewBodyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
        .preferredColorScheme(.dark)
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("#Preview body change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_PreviewAdded(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }
}

#Preview {
    MyView()
}

#Preview("Dark") {
    MyView()
        .preferredColorScheme(.dark)
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("#Preview addition should produce different skeleton hash")
	}
}

func TestComputeSkeleton_MethodBodyChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hi, \(name)!"
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 != hash2 {
		t.Errorf("method body change should produce same skeleton hash, got %s != %s", hash1, hash2)
	}
}

func TestComputeSkeleton_MethodSignatureChange(t *testing.T) {
	dir := t.TempDir()

	base := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String) -> String {
        return "Hello, \(name)"
    }
}
`
	modified := `import SwiftUI

struct MyView: View {
    var body: some View {
        Text("Hello")
    }

    func greet(name: String, loud: Bool) -> String {
        return "Hello, \(name)"
    }
}
`
	path := writeTemp(t, dir, "MyView.swift", base)
	hash1, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	writeTemp(t, dir, "MyView.swift", modified)
	hash2, err := computeSkeleton(path)
	if err != nil {
		t.Fatal(err)
	}

	if hash1 == hash2 {
		t.Error("method signature change should produce different skeleton hash")
	}
}

// --- parseDependencyFile tests ---

func TestParseDependencyFile_ViewFile(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Child")
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()

	types, _, err := parseDependencyFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(types) != 1 {
		t.Fatalf("types count = %d, want 1", len(types))
	}
	if types[0].Name != "HogeView" {
		t.Errorf("Name = %q, want HogeView", types[0].Name)
	}
}

func TestParseDependencyFile_NonViewFile(t *testing.T) {
	// parseDependencyFile should not fail on files without View conformance.
	src := `import Foundation

struct NotAView {
    var name: String
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "NotAView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()

	types, _, err := parseDependencyFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// No computed properties, so no types should be returned.
	if len(types) != 0 {
		t.Errorf("types count = %d, want 0 for struct without computed properties", len(types))
	}
}

// --- referencedTypes / definedTypes tests ---

func TestSwiftParse_ReferencedTypes(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var model: MyViewModel
    var child: FugaView

    var body: some View {
        Text(model.name)
    }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "HogeView.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()

	result, err := swiftParse(path)
	if err != nil {
		t.Fatal(err)
	}

	refSet := make(map[string]bool)
	for _, rt := range result.ReferencedTypes {
		refSet[rt] = true
	}

	// Should include custom types
	if !refSet["MyViewModel"] {
		t.Errorf("referencedTypes should include MyViewModel, got %v", result.ReferencedTypes)
	}
	if !refSet["FugaView"] {
		t.Errorf("referencedTypes should include FugaView, got %v", result.ReferencedTypes)
	}

	// Should NOT include standard/SwiftUI types
	if refSet["View"] {
		t.Errorf("referencedTypes should not include View")
	}
	if refSet["String"] {
		t.Errorf("referencedTypes should not include String")
	}
	if refSet["Text"] {
		t.Errorf("referencedTypes should not include Text")
	}
}

func TestSwiftParse_DefinedTypes(t *testing.T) {
	src := `import SwiftUI

struct HogeView: View {
    var body: some View {
        Text("Hello")
    }
}

class MyViewModel {
    var name: String = ""
}

enum MyState {
    case idle
    case loading
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "Combined.swift")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	resetParseCache()

	result, err := swiftParse(path)
	if err != nil {
		t.Fatal(err)
	}

	defSet := make(map[string]bool)
	for _, dt := range result.DefinedTypes {
		defSet[dt] = true
	}

	if !defSet["HogeView"] {
		t.Errorf("definedTypes should include HogeView, got %v", result.DefinedTypes)
	}
	if !defSet["MyViewModel"] {
		t.Errorf("definedTypes should include MyViewModel, got %v", result.DefinedTypes)
	}
	if !defSet["MyState"] {
		t.Errorf("definedTypes should include MyState, got %v", result.DefinedTypes)
	}
}
