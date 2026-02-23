import Testing

@testable import AxeParserCore

@Suite("View parsing")
struct ViewParsingTests {
  @Test("Single View with body property")
  func singleView() {
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

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].kind == .struct)
    #expect(result.types[0].inheritedTypes == ["View"])
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
    #expect(result.types[0].properties[0].typeExpr == "some View")
    #expect(result.types[0].properties[0].bodyLine == 5)
  }

  @Test("Multiple computed properties")
  func multipleProperties() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var backgroundColor: Color {
              Color.blue
          }
          var body: some View {
              Text("Hello")
                  .background(backgroundColor)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].properties.count == 2)
    #expect(result.types[0].properties[0].name == "backgroundColor")
    #expect(result.types[0].properties[0].typeExpr == "Color")
    #expect(result.types[0].properties[1].name == "body")
  }

  @Test("Multiple View structs")
  func multipleViews() {
    let source = """
      import SwiftUI

      struct FugaView: View {
          var body: some View {
              TextField("", text: .constant(""))
          }
      }

      struct PiyoView: View {
          var body: some View {
              SecureField("", text: .constant(""))
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 2)
    #expect(result.types[0].name == "FugaView")
    #expect(result.types[1].name == "PiyoView")
  }

  @Test("Nested View struct uses qualified name")
  func nestedView() {
    let source = """
      import SwiftUI

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
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 2)
    // Inner view should have qualified name
    #expect(result.types[0].name == "OuterView.InnerView")
    #expect(result.types[0].kind == .struct)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
    // Outer view should keep its simple name
    #expect(result.types[1].name == "OuterView")
    #expect(result.types[1].kind == .struct)
    #expect(result.types[1].properties.count == 1)
    #expect(result.types[1].properties[0].name == "body")
  }

  @Test("Deeply nested View struct uses full qualified name")
  func deeplyNestedView() {
    let source = """
      import SwiftUI

      struct A: View {
          struct B: View {
              struct C: View {
                  var body: some View { Text("C") }
              }
              var body: some View { C() }
          }
          var body: some View { B() }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let names = result.types.map(\.name)
    #expect(names.contains("A.B.C"))
    #expect(names.contains("A.B"))
    #expect(names.contains("A"))
  }

  @Test("Non-View struct with no computed properties is not extracted")
  func nonViewStruct() {
    let source = """
      import SwiftUI

      struct NotAView {
          var name: String
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.types.isEmpty)
  }

  @Test("Fully qualified SwiftUI.View is detected")
  func fullyQualifiedView() {
    let source = """
      import SwiftUI

      struct HogeView: SwiftUI.View {
          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].inheritedTypes == ["SwiftUI.View"])
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
  }

  @Test("Stored properties are not extracted")
  func storedPropertiesIgnored() {
    let source = """
      import SwiftUI

      struct MyView: View {
          @State var count = 0
          let title: String

          var body: some View {
              Text(title)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    // Only body should be extracted (stored properties are skipped)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
  }

  @Test("Single-line computed properties are extracted")
  func singleLineComputedExtracted() {
    let source = """
      import SwiftUI

      struct MyView: View {
          var items: [String]
          var count: Int { items.count }
          var isEmpty: Bool { items.isEmpty }
          var body: some View {
              Text("\\(count)")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].properties.count == 3)
    #expect(result.types[0].properties[0].name == "count")
    #expect(result.types[0].properties[0].source == "items.count")
    #expect(result.types[0].properties[1].name == "isEmpty")
    #expect(result.types[0].properties[1].source == "items.isEmpty")
    #expect(result.types[0].properties[2].name == "body")
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

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.imports.isEmpty)
  }
}

@Suite("Method parsing")
struct MethodParsingTests {
  @Test("Simple method with parameters")
  func methodWithParams() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              Text("Hello")
          }

          func greet(name: String) -> String {
              return "Hello, \\(name)"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types[0].methods.count == 1)
    let m = result.types[0].methods[0]
    #expect(m.name == "greet")
    #expect(m.selector == "greet(name:)")
    #expect(m.signature == "(name: String) -> String")
    #expect(m.source.contains("return"))
  }

  @Test("Method with no parameters")
  func methodNoParams() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func refresh() {
              print("refreshing")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].selector == "refresh()")
    #expect(result.types[0].methods[0].signature == "()")
  }

  @Test("Underscore labels in selector")
  func underscoreLabels() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func add(_ a: Int, _ b: Int) -> Int {
              a + b
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types[0].methods[0].selector == "add(_:_:)")
  }

  @Test("Multi-line signature")
  func multiLineSignature() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func configure(
              title: String,
              count: Int
          ) -> String {
              "\\(title): \\(count)"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types[0].methods[0].selector == "configure(title:count:)")
    #expect(result.types[0].methods[0].signature.contains("-> String"))
  }

  @Test("Async throws method preserves effect specifiers in signature")
  func asyncThrowsMethod() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func refresh() async throws -> Data {
              Data()
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types[0].methods.count == 1)
    let m = result.types[0].methods[0]
    #expect(m.name == "refresh")
    #expect(m.signature == "() async throws -> Data")
  }

  @Test("Async-only method preserves async in signature")
  func asyncOnlyMethod() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func load(id: Int) async -> String {
              ""
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let m = result.types[0].methods[0]
    #expect(m.signature == "(id: Int) async -> String")
  }

  @Test("Throws-only method preserves throws in signature")
  func throwsOnlyMethod() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func save(data: Data) throws {
              print("saving")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let m = result.types[0].methods[0]
    #expect(m.signature == "(data: Data) throws")
  }

  @Test("Static methods are skipped")
  func staticMethodSkipped() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          static func create() -> V { V() }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.types[0].methods.isEmpty)
  }

  @Test("Generic methods are skipped")
  func genericMethodSkipped() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func convert<T>(_ value: T) -> String { "\\(value)" }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.types[0].methods.isEmpty)
  }

  @Test("Init is skipped")
  func initSkipped() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          init() {}
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.types[0].methods.isEmpty)
  }
}

@Suite("#Preview parsing")
struct PreviewParsingTests {
  @Test("Unnamed preview")
  func unnamedPreview() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }

      #Preview {
          V()
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].title == "")
    #expect(result.previews[0].startLine == 7)
    #expect(result.previews[0].source.contains("V()"))
  }

  @Test("Named preview")
  func namedPreview() {
    let source = """
      import SwiftUI

      #Preview("Dark Mode") {
          Text("Hi")
              .preferredColorScheme(.dark)
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].title == "Dark Mode")
    #expect(result.previews[0].source.contains(".preferredColorScheme(.dark)"))
  }

  @Test("Multiple previews")
  func multiplePreviews() {
    let source = """
      #Preview("Light") {
          Text("A")
      }

      #Preview("Dark") {
          Text("B")
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 2)
    #expect(result.previews[0].title == "Light")
    #expect(result.previews[1].title == "Dark")
  }

  @Test("Preview with traits argument")
  func previewWithTraits() {
    let source = """
      #Preview("Landscape", traits: .landscapeLeft) {
          Text("Hi")
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].title == "Landscape")
  }
}

@Suite("Skeleton hash")
struct SkeletonHashTests {
  @Test("Body-only change produces same hash")
  func bodyOnlyChange() {
    let base = """
      import SwiftUI

      struct MyView: View {
          var body: some View {
              Text("Hello")
          }
      }

      #Preview {
          MyView()
      }
      """
    let modified = """
      import SwiftUI

      struct MyView: View {
          var body: some View {
              Text("World")
                  .foregroundColor(.red)
          }
      }

      #Preview {
          MyView()
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Import addition changes hash")
  func importChangesHash() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let modified = """
      import SwiftUI
      import SomeFramework

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Stored property addition changes hash")
  func storedPropertyChangesHash() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let modified = """
      import SwiftUI

      struct V: View {
          @State var count = 0

          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("New struct changes hash")
  func newStructChangesHash() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let modified = """
      import SwiftUI

      struct Helper: View {
          var body: some View { Image(systemName: "star") }
      }

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("#Preview body change produces same hash")
  func previewBodyChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }

      #Preview {
          V()
      }
      """
    let modified = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }

      #Preview {
          V()
              .preferredColorScheme(.dark)
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("New #Preview block changes hash")
  func newPreviewChangesHash() {
    let base = """
      #Preview { Text("A") }
      """
    let modified = """
      #Preview { Text("A") }

      #Preview("Dark") { Text("B") }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Method body change produces same hash")
  func methodBodyChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func greet(name: String) -> String {
              return "Hello, \\(name)"
          }
      }
      """
    let modified = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func greet(name: String) -> String {
              return "Hi, \\(name)!"
          }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Method signature change changes hash")
  func methodSignatureChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func greet(name: String) -> String {
              return "Hello"
          }
      }
      """
    let modified = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func greet(name: String, loud: Bool) -> String {
              return "Hello"
          }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 != hash2)
  }

  @Test("Comment-only change produces same hash")
  func commentOnlyChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let modified = """
      import SwiftUI

      // This is a new comment
      struct V: View {
          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Whitespace-only change produces same hash")
  func whitespaceOnlyChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }
      }
      """
    let modified = """
      import SwiftUI


      struct V:   View {
          var body: some View { Text("") }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("Block comment change produces same hash")
  func blockCommentChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("") }

          func greet(name: String) -> String {
              "Hello"
          }
      }
      """
    let modified = """
      import SwiftUI

      /* A block comment */
      struct V: View {
          // inline comment
          var body: some View { Text("") }

          /// doc comment
          func greet(name: String) -> String {
              "Hello"
          }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }
}

@Suite("Crash regression")
struct CrashRegressionTests {
  @Test("HogeView.swift — file ending without trailing newline")
  func helloViewNoTrailingNewline() {
    // Reproduces: Fatal error: String index is out of bounds
    // The last #Preview block's closing brace is at EOF with no newline.
    let source =
      "import SwiftUI\n\nlet hoge = \"bbbb\"\n\nstruct HogeView: View {\n    var body: some View {\n        Text(\"Hあaaaあああああ\")\n            .font(.largeTitle)\n            .background(.red)\n    }\n}\n\n#Preview {\n    HogeView()\n}\n\n#Preview {\n    Text(\"bっっｃbb\")\n}"
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.previews.count == 2)
    #expect(result.previews[0].source.contains("HogeView()"))
  }
}

@Suite("Referenced types")
struct ReferencedTypesTests {
  @Test("Minimal: stored property type annotation is collected")
  func minimalTypeAnnotation() {
    let source = """
      struct Foo {
          var bar: Baz
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    #expect(result.referencedTypes.contains("Baz"))
    #expect(result.definedTypes.contains("Foo"))
  }

  @Test("Extracts custom type references from type annotations")
  func customTypeReferences() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var model: MyViewModel
          var child: FugaView

          var body: some View {
              Text(model.name)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(refs.contains("MyViewModel"))
    #expect(refs.contains("FugaView"))
    #expect(!refs.contains("View"))
    #expect(!refs.contains("String"))
    #expect(!refs.contains("Text"))
  }

  @Test("No custom types yields empty referencedTypes")
  func noCustomTypes() {
    let source = """
      import SwiftUI

      struct SimpleView: View {
          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    for t in result.referencedTypes {
      #expect(t != "View")
      #expect(t != "Text")
      #expect(t != "String")
    }
  }

  @Test("Generic type arguments are collected")
  func genericTypeArguments() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var items: [HogeModel]

          var body: some View {
              Text("list")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(refs.contains("HogeModel"))
  }
}

@Suite("Defined types")
struct DefinedTypesTests {
  @Test("Collects struct, class, and enum definitions")
  func allTypeDefinitions() {
    let source = """
      import SwiftUI

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
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let defs = Set(result.definedTypes)

    #expect(defs.contains("HogeView"))
    #expect(defs.contains("MyViewModel"))
    #expect(defs.contains("MyState"))
  }

  @Test("Non-View struct is included in definedTypes")
  func nonViewStructIncluded() {
    let source = """
      struct HogeModel {
          var name: String
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.definedTypes.contains("HogeModel"))
    // No computed properties, so types should be empty
    #expect(result.types.isEmpty)
  }
}

@Suite("Expression-position type references")
struct ExpressionTypeReferenceTests {
  @Test("Initializer calls in body are captured via DeclReferenceExprSyntax")
  func initializerCallsCaptured() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              ChildView(title: "Hi")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(
      refs.contains("ChildView"),
      "ChildView() initializer call should be captured")
  }

  @Test("Lowercase function calls are NOT captured")
  func lowercaseFunctionsFiltered() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              helperFunction()
              makeView()
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(!refs.contains("helperFunction"))
    #expect(!refs.contains("makeView"))
  }

  @Test("Type annotations AND expression calls are both captured")
  func bothAnnotationAndExpression() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var model: MyModel

          var body: some View {
              FugaView()
              PiyoView(data: model)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    // Type annotation
    #expect(refs.contains("MyModel"))
    // Expression-position initializer calls
    #expect(refs.contains("FugaView"))
    #expect(refs.contains("PiyoView"))
  }

  @Test("Static member access base type is captured")
  func staticMemberAccessCaptured() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View {
              MyTheme.primaryColor
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(refs.contains("MyTheme"))
  }

  @Test("Filtered types in expression position are excluded")
  func filteredTypesExcluded() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View {
              Text("hello")
              VStack { Image("star") }
              NavigationLink("go") { Text("dest") }
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    #expect(!refs.contains("Text"))
    #expect(!refs.contains("VStack"))
    #expect(!refs.contains("Image"))
    #expect(!refs.contains("NavigationLink"))
  }

  @Test("Real-world pattern: detail view with multiple child views")
  func realWorldPattern() {
    let source = """
      import SwiftUI

      struct ItemDetailView: View {
          let item: Item

          var body: some View {
              ScrollView {
                  VStack {
                      ItemFavoriteButton(item: item)
                      ItemCollectionsMenu(item: item)
                  }
              }
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()
    let refs = Set(result.referencedTypes)

    // Type annotation
    #expect(refs.contains("Item"))
    // Expression-position initializer calls
    #expect(refs.contains("ItemFavoriteButton"))
    #expect(refs.contains("ItemCollectionsMenu"))
    // Filtered SwiftUI types
    #expect(!refs.contains("ScrollView"))
    #expect(!refs.contains("VStack"))
  }
}

@Suite("Access level")
struct AccessLevelTests {
  @Test("Default access level is internal")
  func defaultIsInternal() {
    let source = """
      import SwiftUI

      struct MyView: View {
          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].accessLevel == "internal")
  }

  @Test("Private struct gets private access level")
  func privateStruct() {
    let source = """
      import SwiftUI

      private struct HogeView: View {
          var body: some View {
              Text("Helper")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].accessLevel == "private")
  }

  @Test("Fileprivate struct gets fileprivate access level")
  func fileprivateStruct() {
    let source = """
      import SwiftUI

      fileprivate struct HogeView: View {
          var body: some View {
              Text("fp")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].accessLevel == "fileprivate")
  }

  @Test("Public struct gets public access level")
  func publicStruct() {
    let source = """
      import SwiftUI

      public struct HogeView: View {
          public var body: some View {
              Text("Public")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].accessLevel == "public")
  }

  @Test("Mixed access levels in same file")
  func mixedAccessLevels() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View { Text("internal") }
      }

      private struct FugaView: View {
          var body: some View { Text("private") }
      }

      public struct PiyoView: View {
          public var body: some View { Text("public") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 3)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].accessLevel == "internal")
    #expect(result.types[1].name == "FugaView")
    #expect(result.types[1].accessLevel == "private")
    #expect(result.types[2].name == "PiyoView")
    #expect(result.types[2].accessLevel == "public")
  }
}

@Suite("#if / #endif handling")
struct ConditionalCompilationTests {
  @Test("#if inside View body")
  func ifInsideBody() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              #if os(iOS)
              Text("iOS")
              #else
              Text("Other")
              #endif
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
    let bodySource = result.types[0].properties[0].source
    #expect(bodySource.contains("#if os(iOS)"))
    #expect(bodySource.contains("#else"))
    #expect(bodySource.contains("#endif"))
  }

  @Test("#if wrapping entire struct")
  func ifWrappingStruct() {
    let source = """
      import SwiftUI

      #if os(iOS)
      struct HogeView: View {
          var body: some View {
              Text("iOS only")
          }
      }
      #endif
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
  }

  @Test("#if with nested braces in body")
  func ifWithNestedBraces() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              VStack {
                  #if DEBUG
                  Button("Debug") {
                      print("debug")
                  }
                  #else
                  Button("Release") {
                      print("release")
                  }
                  #endif
              }
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    let body = result.types[0].properties[0]
    #expect(body.name == "body")
    #expect(body.source.contains("#if DEBUG"))
    #expect(body.source.contains("#endif"))
  }

  @Test("Skeleton hash stable with #if body change")
  func skeletonStableWithIfBodyChange() {
    let base = """
      import SwiftUI

      struct V: View {
          var body: some View {
              #if DEBUG
              Text("A")
              #endif
          }
      }
      """
    let modified = """
      import SwiftUI

      struct V: View {
          var body: some View {
              #if DEBUG
              Text("B")
                  .bold()
              #endif
          }
      }
      """
    let hash1 = SwiftAnalyzer(source: base).analyze().skeletonHash
    let hash2 = SwiftAnalyzer(source: modified).analyze().skeletonHash
    #expect(hash1 == hash2)
  }

  @Test("#if between struct members")
  func ifBetweenMembers() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              Text("Hello")
          }

          #if DEBUG
          func debugInfo() -> String {
              "debug"
          }
          #endif

          func normalMethod() -> String {
              "normal"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    // Both methods should be detected (swift-syntax parses #if contents)
    #expect(result.types[0].methods.count == 2)
    let names = result.types[0].methods.map(\.name)
    #expect(names.contains("debugInfo"))
    #expect(names.contains("normalMethod"))
  }

  @Test("#if around #Preview")
  func ifAroundPreview() {
    let source = """
      import SwiftUI

      struct V: View {
          var body: some View { Text("Hi") }
      }

      #if DEBUG
      #Preview {
          V()
      }
      #endif
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.previews.count == 1)
    #expect(result.previews[0].source.contains("V()"))
  }

  @Test("#if canImport")
  func ifCanImport() {
    let source = """
      import SwiftUI
      #if canImport(SomeFramework)
      import SomeFramework
      #endif

      struct HogeView: View {
          var body: some View {
              #if canImport(SomeFramework)
              Text("Available")
              #else
              Text("Unavailable")
              #endif
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.imports.contains("import SomeFramework"))
    let body = result.types[0].properties[0].source
    #expect(body.contains("#if canImport(SomeFramework)"))
  }
}

@Suite("Nested non-View types inside a View")
struct NestedNonViewTypesTests {
  @Test("Nested non-View struct does not leak properties into parent View")
  func nestedNonViewStruct() {
    let source = """
      import SwiftUI

      struct MyView: View {
          struct Helper {
              var computedProp: String {
                  "hello"
              }
          }

          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    // MyView and Helper are both extracted (Helper has computed property)
    let myView = result.types.first(where: { $0.name == "MyView" })!
    let helper = result.types.first(where: { $0.name == "MyView.Helper" })!

    // MyView should only have body
    #expect(myView.properties.count == 1)
    #expect(myView.properties[0].name == "body")

    // Helper should have its own computed property
    #expect(helper.properties.count == 1)
    #expect(helper.properties[0].name == "computedProp")
    #expect(helper.kind == .struct)
    #expect(helper.inheritedTypes.isEmpty)
  }

  @Test("Nested class does not leak properties or methods into parent View")
  func nestedClass() {
    let source = """
      import SwiftUI

      struct MyView: View {
          class ViewModel {
              var displayName: String {
                  "John"
              }

              func refresh() {
                  print("refreshing")
              }
          }

          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let myView = result.types.first(where: { $0.name == "MyView" })!
    let viewModel = result.types.first(where: { $0.name == "MyView.ViewModel" })!

    // ViewModel's properties and methods should NOT appear in MyView
    #expect(myView.properties.count == 1)
    #expect(myView.properties[0].name == "body")
    #expect(myView.methods.isEmpty)

    // ViewModel should have its own members
    #expect(viewModel.properties.count == 1)
    #expect(viewModel.methods.count == 1)
    #expect(viewModel.kind == .class)
  }

  @Test("Nested enum does not leak computed properties into parent View")
  func nestedEnum() {
    let source = """
      import SwiftUI

      struct MyView: View {
          enum Style {
              case hoge
              case fuga

              var spacing: CGFloat {
                  switch self {
                  case .hoge: return 4
                  case .fuga: return 16
                  }
              }
          }

          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let myView = result.types.first(where: { $0.name == "MyView" })!
    let style = result.types.first(where: { $0.name == "MyView.Style" })!

    // Style.spacing should NOT appear in MyView's properties
    #expect(myView.properties.count == 1)
    #expect(myView.properties[0].name == "body")

    // Style should have its own computed property
    #expect(style.properties.count == 1)
    #expect(style.properties[0].name == "spacing")
    #expect(style.kind == .enum)
  }

  @Test("Nested enum with method does not leak method into parent View")
  func nestedEnumWithMethod() {
    let source = """
      import SwiftUI

      struct MyView: View {
          enum Action {
              case tap
              case swipe

              func describe() -> String {
                  switch self {
                  case .tap: return "tap"
                  case .swipe: return "swipe"
                  }
              }
          }

          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let myView = result.types.first(where: { $0.name == "MyView" })!
    let action = result.types.first(where: { $0.name == "MyView.Action" })!

    #expect(myView.methods.isEmpty)
    #expect(action.methods.count == 1)
    #expect(action.methods[0].name == "describe")
  }

  @Test("Multiple nested types do not leak into parent View")
  func multipleNestedTypes() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          struct Config {
              var label: String {
                  "config"
              }
          }

          class DataStore {
              func load() {
                  print("loading")
              }
          }

          enum Tab {
              case info
              case settings

              var title: String {
                  switch self {
                  case .info: return "Info"
                  case .settings: return "Settings"
                  }
              }
          }

          var body: some View {
              Text("Profile")
          }

          func handleTap() {
              print("tapped")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let profileView = result.types.first(where: { $0.name == "HogeView" })!
    // Only body from HogeView itself
    #expect(profileView.properties.count == 1)
    #expect(profileView.properties[0].name == "body")
    // Only handleTap from HogeView itself
    #expect(profileView.methods.count == 1)
    #expect(profileView.methods[0].name == "handleTap")

    // Nested types should also be extracted
    #expect(result.types.first(where: { $0.name == "HogeView.Config" }) != nil)
    #expect(result.types.first(where: { $0.name == "HogeView.DataStore" }) != nil)
    #expect(result.types.first(where: { $0.name == "HogeView.Tab" }) != nil)
  }

  @Test("Nested non-View struct without computed properties is harmless")
  func nestedStructWithoutComputedProps() {
    let source = """
      import SwiftUI

      struct MyView: View {
          struct Item {
              let id: Int
              let name: String
          }

          var body: some View {
              Text("Hello")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    // Only MyView (Item has no computed properties)
    #expect(result.types.count == 1)
    #expect(result.types[0].name == "MyView")
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
  }

  @Test("Nested types are included in definedTypes")
  func nestedTypesInDefinedTypes() {
    let source = """
      import SwiftUI

      struct MyView: View {
          struct Helper {}
          class ViewModel {}
          enum Style { case a }

          var body: some View { Text("") }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    let defs = Set(result.definedTypes)
    #expect(defs.contains("MyView"))
    #expect(defs.contains("Helper"))
    #expect(defs.contains("ViewModel"))
    #expect(defs.contains("Style"))
  }
}

@Suite("Class View conformance")
struct ClassViewTests {
  @Test("Class conforming to View is detected")
  func classConformingToView() {
    let source = """
      import SwiftUI

      class HogeView: View {
          var body: some View {
              Text("profile")
          }

          func reload() {
              print("reload")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].kind == .class)
    #expect(result.types[0].inheritedTypes == ["View"])
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "reload")
  }

  @Test("Extension on a View class merges members")
  func extensionOnViewClass() {
    let source = """
      import SwiftUI

      class HogeView: View {
          var body: some View {
              Text("settings")
          }
      }

      extension HogeView {
          func reset() {
              print("reset")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "reset")
  }

  @Test("Non-View class with computed property is extracted")
  func nonViewClassExtracted() {
    let source = """
      import SwiftUI

      class ViewModel {
          var displayName: String {
              "hello"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].kind == .class)
    #expect(result.types[0].inheritedTypes.isEmpty)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "displayName")
  }
}

@Suite("Extension View conformance")
struct ExtensionViewTests {
  @Test("Extension with View conformance is detected")
  func extensionViewConformance() {
    let source = """
      import SwiftUI

      struct HogeView {
          let title: String
      }

      extension HogeView: View {
          var body: some View {
              Text(title)
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].inheritedTypes.contains("View"))
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "body")
  }

  @Test("Extension with View conformance and methods")
  func extensionWithMethods() {
    let source = """
      import SwiftUI

      struct FugaView {
      }

      extension FugaView: View {
          var body: some View {
              Text("item")
          }

          func format(value: Int) -> String {
              "\\(value)"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "format")
  }

  @Test("Struct with View + extension adds properties from both")
  func structAndExtensionMerged() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              Text("main")
          }
      }

      extension HogeView {
          var subtitle: String {
              "sub"
          }

          func reload() {
              print("reload")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].properties.count == 2)
    let propNames = result.types[0].properties.map(\.name)
    #expect(propNames.contains("body"))
    #expect(propNames.contains("subtitle"))
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "reload")
  }

  @Test("Extension before struct declaration merges members")
  func extensionBeforeStruct() {
    let source = """
      import SwiftUI

      extension HogeView {
          var subtitle: String {
              "sub"
          }
      }

      struct HogeView: View {
          var body: some View {
              Text("main")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].properties.count == 2)
    let propNames = result.types[0].properties.map(\.name)
    #expect(propNames.contains("body"))
    #expect(propNames.contains("subtitle"))
  }

  @Test("Extension with methods before struct declaration")
  func extensionWithMethodsBeforeStruct() {
    let source = """
      import SwiftUI

      extension HogeView {
          func format(value: Int) -> String {
              "\\(value)"
          }
      }

      struct HogeView: View {
          var body: some View {
              Text("item")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "format")
  }

  @Test("Extension on non-View struct with computed property is extracted")
  func extensionOnNonViewStruct() {
    let source = """
      import SwiftUI

      struct HogeModel {
          var name: String
      }

      extension HogeModel {
          var displayName: String {
              name.uppercased()
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    // HogeModel now gets extracted because it has a computed property
    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeModel")
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "displayName")
  }
}

@Suite("Non-View type extraction")
struct NonViewTypeExtractionTests {
  @Test("Non-View struct with computed property is extracted")
  func nonViewStructWithComputed() {
    let source = """
      struct HogeModel {
          var formatted: String {
              "formatted value"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeModel")
    #expect(result.types[0].kind == .struct)
    #expect(result.types[0].inheritedTypes.isEmpty)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "formatted")
  }

  @Test("Enum with computed property is extracted")
  func enumWithComputed() {
    let source = """
      enum Priority {
          case low, medium, high

          var label: String {
              switch self {
              case .low: return "Low"
              case .medium: return "Medium"
              case .high: return "High"
              }
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "Priority")
    #expect(result.types[0].kind == .enum)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "label")
  }

  @Test("Actor with computed property and method is extracted")
  func actorExtracted() {
    let source = """
      actor HogeManager {
          var status: String {
              "ready"
          }

          func fetch() {
              print("fetching")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeManager")
    #expect(result.types[0].kind == .actor)
    #expect(result.types[0].properties.count == 1)
    #expect(result.types[0].properties[0].name == "status")
    #expect(result.types[0].methods.count == 1)
    #expect(result.types[0].methods[0].name == "fetch")
  }

  @Test("Extension-only type gets kind .unknown")
  func extensionOnlyType() {
    let source = """
      extension ExternalType {
          var computed: String {
              "value"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "ExternalType")
    #expect(result.types[0].kind == .unknown)
    #expect(result.types[0].properties.count == 1)
  }

  @Test("Extension inheritedTypes merge with struct")
  func extensionInheritedTypesMerge() {
    let source = """
      import SwiftUI

      struct HogeView: View {
          var body: some View {
              Text("tag")
          }
      }

      extension HogeView: Identifiable {
          var id: String {
              "tag-id"
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].inheritedTypes.contains("View"))
    #expect(result.types[0].inheritedTypes.contains("Identifiable"))
  }

  @Test("Extension with only inheritedTypes (no members) still merges conformance")
  func extensionOnlyInheritedTypes() {
    let source = """
      import SwiftUI

      struct FugaView: View {
          var body: some View {
              Text("item")
          }
      }

      extension FugaView: Identifiable {}
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "FugaView")
    #expect(result.types[0].inheritedTypes.contains("View"))
    #expect(result.types[0].inheritedTypes.contains("Identifiable"))
  }

  @Test("Extension-before-struct with only inheritedTypes merges conformance")
  func extensionBeforeStructOnlyInheritedTypes() {
    let source = """
      import SwiftUI

      extension HogeView: Identifiable {}

      struct HogeView: View {
          var body: some View {
              Text("card")
          }
      }
      """
    let result = SwiftAnalyzer(source: source).analyze()

    #expect(result.types.count == 1)
    #expect(result.types[0].name == "HogeView")
    #expect(result.types[0].inheritedTypes.contains("View"))
    #expect(result.types[0].inheritedTypes.contains("Identifiable"))
  }
}
