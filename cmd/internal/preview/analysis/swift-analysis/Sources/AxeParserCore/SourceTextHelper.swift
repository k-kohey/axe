import SwiftSyntax

/// Shared utility for text extraction from Swift source.
/// Used by `TypeMemberExtractor` and `PreviewExtractor` to compute
/// body ranges and extract source lines.
struct SourceTextHelper {
  private let source: String
  private let sourceUTF8: String.UTF8View

  init(source: String) {
    self.source = source
    self.sourceUTF8 = source.utf8
  }

  // MARK: - Body Range Extraction

  /// Returns the String.Index range of the inner body (between braces),
  /// excluding the opening and closing brace lines themselves.
  func innerBodyRange(of block: AccessorBlockSyntax) -> Range<String.Index> {
    innerRange(leftBrace: block.leftBrace, rightBrace: block.rightBrace)
  }

  func innerBodyRange(of block: CodeBlockSyntax) -> Range<String.Index> {
    innerRange(leftBrace: block.leftBrace, rightBrace: block.rightBrace)
  }

  func innerBodyRange(of closure: ClosureExprSyntax) -> Range<String.Index> {
    innerRange(leftBrace: closure.leftBrace, rightBrace: closure.rightBrace)
  }

  /// Given left and right brace tokens, return the range of content between them.
  /// For multi-line bodies, returns inner lines (excluding brace lines).
  /// For single-line bodies (braces on same line), returns the content between braces.
  func innerRange(leftBrace: TokenSyntax, rightBrace: TokenSyntax) -> Range<String.Index> {
    let afterLeftBrace = utf8Index(leftBrace.endPositionBeforeTrailingTrivia.utf8Offset)
    let rightBracePos = utf8Index(rightBrace.positionAfterSkippingLeadingTrivia.utf8Offset)

    // Check if braces are on the same line (single-line body).
    let bodyStart = nextLineStart(after: afterLeftBrace)
    let bodyEnd = lineStart(at: rightBracePos)

    if bodyStart >= bodyEnd {
      // Single-line: extract content between { and } (trimming the space after {).
      var start = afterLeftBrace
      while start < rightBracePos && source[start] == " " {
        start = source.index(after: start)
      }
      var end = rightBracePos
      while end > start && source[source.index(before: end)] == " " {
        end = source.index(before: end)
      }
      return start..<end
    }
    return bodyStart..<bodyEnd
  }

  // MARK: - Index Conversion

  /// Converts a UTF-8 byte offset to a String.Index.
  func utf8Index(_ utf8Offset: Int) -> String.Index {
    sourceUTF8.index(sourceUTF8.startIndex, offsetBy: min(utf8Offset, sourceUTF8.count))
  }

  // MARK: - Line Number

  /// Returns the 1-based line number for the given source position.
  func lineNumber(at position: AbsolutePosition) -> Int {
    let offset = position.utf8Offset
    var line = 1
    var i = sourceUTF8.startIndex
    let end = sourceUTF8.index(sourceUTF8.startIndex, offsetBy: min(offset, sourceUTF8.count))
    while i < end {
      if sourceUTF8[i] == UInt8(ascii: "\n") {
        line += 1
      }
      i = sourceUTF8.index(after: i)
    }
    return line
  }

  /// Returns the 1-based line number for the given string index.
  func lineNumber(at index: String.Index) -> Int {
    var line = 1
    var i = source.startIndex
    while i < index && i < source.endIndex {
      if source[i] == "\n" {
        line += 1
      }
      i = source.index(after: i)
    }
    return line
  }

  // MARK: - Text Extraction

  /// Extracts the text between the given range, trimming trailing newline.
  func extractLines(in range: Range<String.Index>) -> String {
    guard range.lowerBound < range.upperBound else { return "" }
    var text = String(source[range])
    // Remove trailing newline if present
    while text.hasSuffix("\n") {
      text.removeLast()
    }
    return text
  }

  /// Returns the index of the start of the next line after the given index.
  func nextLineStart(after index: String.Index) -> String.Index {
    var i = index
    while i < source.endIndex {
      if source[i] == "\n" {
        return source.index(after: i)
      }
      i = source.index(after: i)
    }
    return source.endIndex
  }

  /// Returns the index of the start of the line containing the given index.
  func lineStart(at index: String.Index) -> String.Index {
    var i = index
    while i > source.startIndex {
      let prev = source.index(before: i)
      if source[prev] == "\n" {
        return i
      }
      i = prev
    }
    return source.startIndex
  }
}
