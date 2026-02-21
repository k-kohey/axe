import SwiftParser
import SwiftSyntax
import Testing

@testable import AxeParserCore

@Suite("ImportCollector")
struct ImportCollectorTests {
  @Test("Excludes SwiftUI and collects other imports")
  func excludesSwiftUI() {
    let source = """
      import SwiftUI
      import MapKit
      import Foundation
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports == ["import MapKit", "import Foundation"])
  }

  @Test("SwiftUI only returns empty")
  func swiftUIOnlyReturnsEmpty() {
    let source = """
      import SwiftUI
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports.isEmpty)
  }

  @Test("Submodule import is collected correctly")
  func submoduleImport() {
    let source = """
      import SwiftUI
      import Foundation.NSObject
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports.count == 1)
    #expect(collector.imports[0].contains("Foundation.NSObject"))
  }

  @Test("No imports returns empty")
  func noImportsReturnsEmpty() {
    let source = """
      struct HogeView {
          var body: some View { Text("") }
      }
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports.isEmpty)
  }

  @Test("#if canImport imports are collected")
  func canImportCollected() {
    let source = """
      import SwiftUI
      #if canImport(MapKit)
      import MapKit
      #endif
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports.contains("import MapKit"))
  }
}
