import Testing

@testable import AxeParserCore

// MARK: - Helper

extension ParseResult {
  fileprivate func memberSources(forType typeName: String) -> [MemberSource] {
    memberSources.filter { $0.typeName == typeName }
  }

  fileprivate func properties(forType typeName: String) -> [MemberSource] {
    memberSources.filter { $0.typeName == typeName && $0.kind == .property }
  }

  fileprivate func methods(forType typeName: String) -> [MemberSource] {
    memberSources.filter { $0.typeName == typeName && $0.kind == .method }
  }
}

@Suite("MemberSource extraction — properties")
struct PropertyExtractionTests {
  @Test("Computed property body is extracted")
  func computedPropertyBody() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              Text("Hello")
                  .padding()
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeView")

    #expect(props.count == 1)
    #expect(props[0].name == "body")
    #expect(props[0].typeExpr == "some View")
    #expect(props[0].bodyLine == 5)
    #expect(props[0].source.contains("Text"))
  }

  @Test("Multiple computed properties")
  func multipleComputedProperties() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var backgroundColor: Color {
              Color.blue
          }
          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeView")

    #expect(props.count == 2)
    #expect(props[0].name == "backgroundColor")
    #expect(props[0].typeExpr == "Color")
    #expect(props[1].name == "body")
  }

  @Test("Single-line computed property")
  func singleLineComputed() {
    let source = """
      struct HogeModel {
          var items: [String]
          var count: Int { items.count }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeModel")

    #expect(props.count == 1)
    #expect(props[0].name == "count")
    #expect(props[0].source == "items.count")
  }

  @Test("Stored properties are not extracted (no accessor block)")
  func storedPropertiesSkipped() {
    let source = """
      struct HogeView: View {
          @State var count = 0
          let title: String

          var body: some View {
              Text(title)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeView")

    #expect(props.count == 1)
    #expect(props[0].name == "body")
  }

  @Test("Explicit get/set properties are skipped (thunk #sourceLocation constraint)")
  func explicitGetSetSkipped() {
    let source = """
      struct HogeModel {
          private var _value: Int = 0
          var value: Int {
              get { _value }
              set { _value = newValue }
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.memberSources.isEmpty)
  }

  @Test("Static property is extracted (filtering done on Go side)")
  func staticPropertyExtracted() {
    let source = """
      struct HogeConfig {
          static var shared: HogeConfig { HogeConfig() }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeConfig")

    #expect(props.count == 1)
    #expect(props[0].name == "shared")
  }

  @Test("Line number uses var keyword position")
  func lineNumberIsVarKeyword() {
    let source = """
      struct HogeView: View {
          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let props = result.properties(forType: "HogeView")

    // var keyword is on line 2
    #expect(props[0].line == 2)
  }
}

@Suite("MemberSource extraction — methods")
struct MethodExtractionTests {
  @Test("Method with parameters")
  func methodWithParams() {
    let source = """
      struct HogeView: View {
          var body: some View { Text("") }

          func greet(name: String) -> String {
              return "Hello, \\(name)"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let methods = result.methods(forType: "HogeView")

    #expect(methods.count == 1)
    #expect(methods[0].name == "greet")
    #expect(methods[0].selector == "greet(name:)")
    #expect(methods[0].signature == "(name: String) -> String")
    #expect(methods[0].source.contains("return"))
  }

  @Test("Method without parameters")
  func methodNoParams() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          func refresh() {
              print("refreshing")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let methods = result.methods(forType: "V")

    #expect(methods.count == 1)
    #expect(methods[0].selector == "refresh()")
    #expect(methods[0].signature == "()")
  }

  @Test("Underscore labels in selector")
  func underscoreLabels() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          func add(_ a: Int, _ b: Int) -> Int { a + b }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.methods(forType: "V")[0].selector == "add(_:_:)")
  }

  @Test("Async throws method")
  func asyncThrows() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          func fetch() async throws -> Data { Data() }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.methods(forType: "V")[0].signature == "() async throws -> Data")
  }

  @Test("Generic methods are skipped (@_dynamicReplacement constraint)")
  func genericMethodSkipped() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          func convert<T>(_ value: T) -> String { "\\(value)" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.methods(forType: "V").isEmpty)
  }

  @Test("Static method is extracted (filtering done on Go side)")
  func staticMethodExtracted() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          static func create() -> V { V() }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let methods = result.methods(forType: "V")
    #expect(methods.count == 1)
    #expect(methods[0].name == "create")
  }

  @Test("Init is extracted (filtering done on Go side)")
  func initExtracted() {
    let source = """
      struct V: View {
          var body: some View { Text("") }
          init() {}
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let methods = result.methods(forType: "V")
    #expect(methods.count == 1)
    #expect(methods[0].name == "init")
  }

  @Test("Line number uses func keyword position")
  func lineNumberIsFuncKeyword() {
    let source = """
      struct V {
          func greet() {
              print("hi")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let methods = result.methods(forType: "V")
    // func keyword is on line 2
    #expect(methods[0].line == 2)
  }
}

@Suite("MemberSource extraction — type context")
struct TypeContextTests {
  @Test("Extension members get extension type name")
  func extensionTypeName() {
    let source = """
      struct HogeView: View {
          var body: some View { Text("") }
      }

      extension HogeView {
          var subtitle: String { "sub" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let allProps = result.memberSources.filter { $0.kind == .property }

    #expect(allProps.count == 2)
    #expect(allProps[0].typeName == "HogeView")
    #expect(allProps[1].typeName == "HogeView")
  }

  @Test("Nested type members get nested type name")
  func nestedTypeName() {
    let source = """
      struct Outer {
          struct Inner {
              var computed: String { "hello" }
          }
          var outerProp: Int { 42 }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let innerProps = result.properties(forType: "Outer.Inner")
    let outerProps = result.properties(forType: "Outer")

    #expect(innerProps.count == 1)
    #expect(innerProps[0].name == "computed")
    #expect(outerProps.count == 1)
    #expect(outerProps[0].name == "outerProp")
  }

  @Test("Multiple types in same file")
  func multipleTypes() {
    let source = """
      struct FugaView: View {
          var body: some View { Text("a") }
      }
      struct PiyoView: View {
          var body: some View { Text("b") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.properties(forType: "FugaView").count == 1)
    #expect(result.properties(forType: "PiyoView").count == 1)
  }

  @Test("Actor type members")
  func actorMembers() {
    let source = """
      actor HogeManager {
          var status: String { "ready" }
          func fetch() { print("fetching") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.properties(forType: "HogeManager").count == 1)
    #expect(result.methods(forType: "HogeManager").count == 1)
  }

  @Test("Global declarations are not extracted (no enclosing type)")
  func globalDeclarationsSkipped() {
    let source = """
      var globalComputed: Int { 42 }
      func globalFunc() { print("hi") }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.memberSources.isEmpty)
  }
}

@Suite("Import parsing")
struct ImportParsingTests {
  @Test("Extracts non-SwiftUI imports")
  func importsExcludeSwiftUI() {
    let source = """
      import SwiftUI
      import SomeFramework
      import Foundation
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.imports == ["import SomeFramework", "import Foundation"])
  }

  @Test("No imports when only SwiftUI")
  func onlySwiftUI() {
    let source = """
      import SwiftUI
      struct V: View { var body: some View { Text("") } }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.imports.isEmpty)
  }
}

@Suite("#Preview parsing")
struct PreviewParsingTests {
  @Test("Unnamed preview")
  func unnamedPreview() {
    let source = """
      struct V: View { var body: some View { Text("") } }

      #Preview {
          V()
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].title == "")
    #expect(result.previews[0].source.contains("V()"))
  }

  @Test("Named preview")
  func namedPreview() {
    let source = """
      #Preview("Dark Mode") {
          Text("Hi")
              .preferredColorScheme(.dark)
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].title == "Dark Mode")
  }

  @Test("Multiple previews")
  func multiplePreviews() {
    let source = """
      #Preview("Light") { Text("A") }
      #Preview("Dark") { Text("B") }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 2)
    #expect(result.previews[0].title == "Light")
    #expect(result.previews[1].title == "Dark")
  }
}

@Suite("Skeleton hash")
struct SkeletonHashTests {
  @Test("Body-only change produces same hash")
  func bodyOnlyChange() {
    let base = """
      struct V: View {
          var body: some View { Text("Hello") }
      }
      #Preview { V() }
      """
    let modified = """
      struct V: View {
          var body: some View { Text("World").foregroundColor(.red) }
      }
      #Preview { V() }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Import addition changes hash")
  func importChangesHash() {
    let base = """
      import SwiftUI
      struct V: View { var body: some View { Text("") } }
      """
    let modified = """
      import SwiftUI
      import SomeFramework
      struct V: View { var body: some View { Text("") } }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Stored property addition changes hash")
  func storedPropertyChangesHash() {
    let base = """
      struct V: View { var body: some View { Text("") } }
      """
    let modified = """
      struct V: View {
          @State var count = 0
          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Method body change produces same hash")
  func methodBodyChange() {
    let base = """
      struct V {
          func greet(name: String) -> String { return "Hello, \\(name)" }
      }
      """
    let modified = """
      struct V {
          func greet(name: String) -> String { return "Hi, \\(name)!" }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Method signature change changes hash")
  func methodSignatureChange() {
    let base = """
      struct V {
          func greet(name: String) -> String { return "Hello" }
      }
      """
    let modified = """
      struct V {
          func greet(name: String, loud: Bool) -> String { return "Hello" }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Comment-only change produces same hash")
  func commentOnlyChange() {
    let base = """
      struct V: View { var body: some View { Text("") } }
      """
    let modified = """
      // A new comment
      struct V: View { var body: some View { Text("") } }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }
}

@Suite("Type access level extraction")
struct TypeAccessLevelTests {
  @Test("Default access level is internal")
  func defaultInternal() {
    let source = """
      struct HogeView: View {
          var body: some View { Text("") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["HogeView"] == "internal")
  }

  @Test("Private struct")
  func privateStruct() {
    let source = """
      private struct DataFormatter {
          var formatted: String { "ok" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["DataFormatter"] == "private")
  }

  @Test("Fileprivate class")
  func fileprivateClass() {
    let source = """
      fileprivate class HogeHelper {
          func help() { print("help") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["HogeHelper"] == "fileprivate")
  }

  @Test("Public enum")
  func publicEnum() {
    let source = """
      public enum HogeKind {
          var label: String { "kind" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["HogeKind"] == "public")
  }

  @Test("Actor access level")
  func actorAccessLevel() {
    let source = """
      private actor HogeManager {
          var status: String { "ready" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["HogeManager"] == "private")
  }

  @Test("Nested type gets qualified name as key")
  func nestedTypeKey() {
    let source = """
      struct Outer {
          private struct Inner {
              var computed: String { "hello" }
          }
          var prop: Int { 42 }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["Outer"] == "internal")
    #expect(result.typeAccessLevels["Outer.Inner"] == "private")
  }

  @Test("Extension does not override type access level")
  func extensionDoesNotOverride() {
    let source = """
      private struct HogeModel {
          var computed: String { "hello" }
      }

      extension HogeModel {
          var another: String { "world" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    // The type declaration's access level should be preserved.
    #expect(result.typeAccessLevels["HogeModel"] == "private")
  }

  @Test("Multiple types with different access levels")
  func multipleTypes() {
    let source = """
      public struct PublicView: View {
          var body: some View { Text("") }
      }
      private struct PrivateHelper {
          var value: String { "ok" }
      }
      struct InternalModel {
          var data: String { "data" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.typeAccessLevels["PublicView"] == "public")
    #expect(result.typeAccessLevels["PrivateHelper"] == "private")
    #expect(result.typeAccessLevels["InternalModel"] == "internal")
  }
}

@Suite("Crash regression")
struct CrashRegressionTests {
  @Test("File ending without trailing newline")
  func noTrailingNewline() {
    let source =
      "import SwiftUI\n\nlet hoge = \"bbbb\"\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hあaaaあああああ\")\n            .font(.largeTitle)\n            .background(.red)\n    }\n}\n\n#Preview {\n    HogeView()\n}\n\n#Preview {\n    Text(\"bっっｃbb\")\n}"
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.properties(forType: "HogeView").count == 1)
    #expect(result.previews.count == 2)
    #expect(result.previews[0].source.contains("HogeView()"))
  }
}
