import SwiftParser
import SwiftSyntax
import Testing

@testable import AxeParserCore

@Suite("SourceTextHelper")
struct SourceTextHelperTests {
  @Test("Multi-line body innerBodyRange returns inner content only")
  func multiLineBody() {
    let source = """
      struct Hoge {
          var value: Int {
              return 42
          }
      }
      """
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    // Find the AccessorBlockSyntax
    let accessorBlock = findFirst(AccessorBlockSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: accessorBlock)
    let text = helper.extractLines(in: range)
    #expect(text.contains("return 42"))
    #expect(!text.contains("{"))
    #expect(!text.contains("}"))
  }

  @Test("Single-line body innerBodyRange returns content between braces")
  func singleLineBody() {
    let source = """
      struct Hoge {
          var count: Int { items.count }
      }
      """
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let accessorBlock = findFirst(AccessorBlockSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: accessorBlock)
    let text = helper.extractLines(in: range)
    #expect(text == "items.count")
  }

  @Test("Empty body innerBodyRange returns empty range")
  func emptyBody() {
    let source = """
      struct Hoge {
          func doNothing() {}
      }
      """
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let codeBlock = findFirst(CodeBlockSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: codeBlock)
    let text = helper.extractLines(in: range)
    #expect(text == "")
  }

  @Test("lineNumber(at: AbsolutePosition) returns 1-based line number")
  func lineNumberAbsolutePosition() {
    let source = "line1\nline2\nline3\n"
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)

    // Position 0 = line 1
    #expect(helper.lineNumber(at: AbsolutePosition(utf8Offset: 0)) == 1)
    // Position 6 = after first newline = line 2
    #expect(helper.lineNumber(at: AbsolutePosition(utf8Offset: 6)) == 2)
    // Position 12 = after second newline = line 3
    #expect(helper.lineNumber(at: AbsolutePosition(utf8Offset: 12)) == 3)
    // Suppress unused variable warning
    _ = tree
  }

  @Test("lineNumber(at: String.Index) returns 1-based line number")
  func lineNumberStringIndex() {
    let source = "line1\nline2\nline3\n"
    let helper = SourceTextHelper(source: source)

    #expect(helper.lineNumber(at: source.startIndex) == 1)
    let secondLine = source.index(source.startIndex, offsetBy: 6)
    #expect(helper.lineNumber(at: secondLine) == 2)
    let thirdLine = source.index(source.startIndex, offsetBy: 12)
    #expect(helper.lineNumber(at: thirdLine) == 3)
  }

  @Test("extractLines strips trailing newlines")
  func extractLinesTrimsTrailingNewline() {
    let source = "hello\nworld\n"
    let helper = SourceTextHelper(source: source)

    let range = source.startIndex..<source.endIndex
    let text = helper.extractLines(in: range)
    #expect(text == "hello\nworld")
  }

  @Test("EOF without trailing newline does not crash")
  func eofNoTrailingNewline() {
    let source = "struct Hoge { var x: Int { 42 } }"
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let accessorBlock = findFirst(AccessorBlockSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: accessorBlock)
    let text = helper.extractLines(in: range)
    #expect(text == "42")
  }

  @Test("CodeBlockSyntax innerBodyRange for method body")
  func codeBlockBody() {
    let source = """
      struct Hoge {
          func greet() -> String {
              return "hello"
          }
      }
      """
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let codeBlock = findFirst(CodeBlockSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: codeBlock)
    let text = helper.extractLines(in: range)
    #expect(text.contains("return \"hello\""))
  }

  @Test("ClosureExprSyntax innerBodyRange for closure body")
  func closureBody() {
    let source = """
      let f = {
          print("hello")
      }
      """
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let closure = findFirst(ClosureExprSyntax.self, in: tree)!
    let range = helper.innerBodyRange(of: closure)
    let text = helper.extractLines(in: range)
    #expect(text.contains("print(\"hello\")"))
  }
}

// MARK: - Helpers

private func findFirst<T: SyntaxProtocol>(_ type: T.Type, in tree: some SyntaxProtocol) -> T? {
  for child in tree.children(viewMode: .sourceAccurate) {
    if let match = child.as(type) {
      return match
    }
    if let found = findFirst(type, in: child) {
      return found
    }
  }
  return nil
}
