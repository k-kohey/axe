import SwiftParser
import SwiftSyntax
import Testing

@testable import AxeParserCore

@Suite("TypeReferenceCollector")
struct TypeReferenceCollectorTests {
  // MARK: - Type Reference Collection

  @Test("Collects custom types from type annotations")
  func typeAnnotations() {
    let source = """
      struct HogeView {
          var model: HogeModel
          var child: FugaView
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(collector.referencedTypes.contains("HogeModel"))
    #expect(collector.referencedTypes.contains("FugaView"))
  }

  @Test("Collects UpperCamelCase references in expressions (DeclReferenceExpr)")
  func expressionReferences() {
    let source = """
      struct HogeView {
          var body: some View {
              FugaView(title: "Hi")
          }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(collector.referencedTypes.contains("FugaView"))
  }

  @Test("Does not collect lowercase function calls")
  func lowercaseExcluded() {
    let source = """
      struct HogeView {
          func doSomething() {
              helperFunction()
              makeView()
          }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(!collector.referencedTypes.contains("helperFunction"))
    #expect(!collector.referencedTypes.contains("makeView"))
  }

  @Test("Excludes filteredTypes (standard library and SwiftUI types)")
  func filteredTypesExcluded() {
    let source = """
      struct HogeView {
          var name: String
          var items: [Int]
          var body: some View {
              Text("hello")
              VStack { Image("star") }
          }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(!collector.referencedTypes.contains("String"))
    #expect(!collector.referencedTypes.contains("Int"))
    #expect(!collector.referencedTypes.contains("Text"))
    #expect(!collector.referencedTypes.contains("VStack"))
    #expect(!collector.referencedTypes.contains("Image"))
  }

  @Test("Collects generic type arguments")
  func genericTypeArguments() {
    let source = """
      struct HogeView {
          var items: [HogeModel]
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(collector.referencedTypes.contains("HogeModel"))
  }

  // MARK: - Type Definition Collection

  @Test("Collects struct/class/enum/actor names")
  func allTypeDefinitions() {
    let source = """
      struct HogeView {}
      class HogeModel {}
      enum HogeState { case idle }
      actor HogeManager {}
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    let defs = Set(collector.definedTypes)
    #expect(defs.contains("HogeView"))
    #expect(defs.contains("HogeModel"))
    #expect(defs.contains("HogeState"))
    #expect(defs.contains("HogeManager"))
  }

  @Test("Collects nested type names (non-qualified)")
  func nestedTypeNames() {
    let source = """
      struct HogeView {
          struct HelperView {}
          class ViewModel {}
          enum Style { case a }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    let defs = Set(collector.definedTypes)
    #expect(defs.contains("HogeView"))
    #expect(defs.contains("HelperView"))
    #expect(defs.contains("ViewModel"))
    #expect(defs.contains("Style"))
  }

  @Test("Deduplicates referenced types (Set semantics)")
  func deduplicatesReferencedTypes() {
    let source = """
      struct HogeView {
          var a: HogeModel
          var b: HogeModel
          func doWork() -> HogeModel { HogeModel() }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    // Should have exactly one entry for HogeModel despite multiple references
    let count = collector.referencedTypes.filter { $0 == "HogeModel" }.count
    #expect(count == 1)
  }

  @Test("Single-character uppercase identifier is collected")
  func singleCharUppercase() {
    let source = """
      struct HogeView {
          var body: some View {
              V()
          }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(collector.referencedTypes.contains("V"))
  }

  @Test("Struct without computed properties is included in definedTypes")
  func nonComputedStruct() {
    let source = """
      struct PlainModel {
          var name: String
      }
      """
    let tree = Parser.parse(source: source)
    let collector = TypeReferenceCollector()
    collector.walk(tree)

    #expect(collector.definedTypes.contains("PlainModel"))
  }
}
