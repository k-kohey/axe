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

  @Test("#if canImport imports are collected in conditional form")
  func canImportCollected() {
    let source = """
      import SwiftUI
      import Foundation
      #if canImport(MapKit)
      import MapKit
      #endif
      #if canImport(AppKit)
      import AppKit
      #elseif canImport(UIKit)
      import UIKit
      #endif
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports == [
      "import Foundation",
      "#if canImport(MapKit)\nimport MapKit\n#endif",
      "#if canImport(AppKit)\nimport AppKit\n#elseif canImport(UIKit)\nimport UIKit\n#endif",
    ])
  }

  @Test("#if os / #if DEBUG imports are skipped")
  func nonCanImportSkipped() {
    let source = """
      import SwiftUI
      import Foundation
      #if os(iOS)
      import UIKit
      #endif
      #if DEBUG
      import OSLog
      #endif
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports == ["import Foundation"])
  }

  @Test("Mixed chain with non-canImport clause is skipped entirely")
  func mixedChainSkipped() {
    let source = """
      import Foundation
      #if os(macOS)
      import AppKit
      #elseif canImport(UIKit)
      import UIKit
      #endif
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports == ["import Foundation"])
  }

  @Test("Mixed chain is skipped even when non-canImport clause has no imports")
  func mixedChainNoImportClauseSkipped() {
    let source = """
      import Foundation
      #if os(macOS)
      let sentinel = 1
      #elseif canImport(UIKit)
      import UIKit
      #endif
      """
    let tree = Parser.parse(source: source)
    let collector = ImportCollector()
    collector.walk(tree)

    #expect(collector.imports == ["import Foundation"])
  }
}
