import SwiftParser
import SwiftSyntax
import Testing

@testable import AxeParserCore

@Suite("PreviewExtractor")
struct PreviewExtractorTests {

  /// Helper to run PreviewExtractor on source.
  private func extract(from source: String) -> PreviewExtractor {
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let extractor = PreviewExtractor(helper: helper)
    extractor.walk(tree)
    return extractor
  }

  @Test("Extracts unnamed #Preview")
  func unnamedPreview() {
    let source = """
      #Preview {
          HogeView()
      }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 1)
    #expect(extractor.previews[0].title == "")
    #expect(extractor.previews[0].startLine == 1)
    #expect(extractor.previews[0].source.contains("HogeView()"))
  }

  @Test("Extracts named #Preview with title")
  func namedPreview() {
    let source = """
      #Preview("Dark Mode") {
          Text("Hi")
              .preferredColorScheme(.dark)
      }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 1)
    #expect(extractor.previews[0].title == "Dark Mode")
    #expect(extractor.previews[0].source.contains(".preferredColorScheme(.dark)"))
  }

  @Test("Extracts multiple #Preview blocks")
  func multiplePreviews() {
    let source = """
      #Preview("Light") {
          Text("A")
      }

      #Preview("Dark") {
          Text("B")
      }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 2)
    #expect(extractor.previews[0].title == "Light")
    #expect(extractor.previews[1].title == "Dark")
  }

  @Test("Handles traits argument correctly")
  func previewWithTraits() {
    let source = """
      #Preview("Landscape", traits: .landscapeLeft) {
          Text("Hi")
      }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 1)
    #expect(extractor.previews[0].title == "Landscape")
  }

  @Test("Handles both MacroExpansionExprSyntax and MacroExpansionDeclSyntax")
  func bothMacroTypes() {
    // MacroExpansionDeclSyntax (top-level)
    let source1 = """
      #Preview {
          Text("A")
      }
      """
    let extractor1 = extract(from: source1)
    #expect(extractor1.previews.count == 1)

    // Inside a struct context (may produce ExprSyntax)
    let source2 = """
      struct HogeView {
          var body: some View { Text("") }
      }

      #Preview { HogeView() }
      """
    let extractor2 = extract(from: source2)
    #expect(extractor2.previews.count == 1)
  }

  @Test("bodyRanges include preview body ranges")
  func bodyRangesForPreviews() {
    let source = """
      #Preview {
          Text("hello")
      }
      """
    let extractor = extract(from: source)
    #expect(!extractor.bodyRanges.isEmpty)
  }

  @Test("Non-Preview macro is skipped")
  func nonPreviewMacroSkipped() {
    let source = """
      #available(iOS 17, *)
      struct HogeView {
          var body: some View { Text("") }
      }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.isEmpty)
  }

  @Test("Single-line preview body is extracted correctly")
  func singleLinePreviewBody() {
    let source = """
      #Preview { HogeView() }
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 1)
    #expect(extractor.previews[0].source == "HogeView()")
  }

  @Test("#if DEBUG wrapping #Preview is handled")
  func ifDebugPreview() {
    let source = """
      #if DEBUG
      #Preview {
          HogeView()
      }
      #endif
      """
    let extractor = extract(from: source)

    #expect(extractor.previews.count == 1)
    #expect(extractor.previews[0].source.contains("HogeView()"))
  }
}
