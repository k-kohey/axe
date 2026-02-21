import CommonCrypto
import Foundation
import SwiftParser
import SwiftSyntax

/// Analyzes Swift source using swift-syntax AST.
public struct SwiftAnalyzer {
  let source: String
  let tree: SourceFileSyntax

  public init(source: String) {
    self.source = source
    self.tree = Parser.parse(source: source)
  }

  public func analyze() -> ParseResult {
    let helper = SourceTextHelper(source: source)

    let importCollector = ImportCollector()
    importCollector.walk(tree)

    let typeRefCollector = TypeReferenceCollector()
    typeRefCollector.walk(tree)

    let memberExtractor = TypeMemberExtractor(helper: helper)
    memberExtractor.walk(tree)
    memberExtractor.resolvePendingExtensions()

    let previewExtractor = PreviewExtractor(helper: helper)
    previewExtractor.walk(tree)

    let allBodyRanges = memberExtractor.bodyRanges + previewExtractor.bodyRanges
    let skeletonHash = computeSkeletonHash(
      source: source,
      bodyRanges: allBodyRanges
    )

    return ParseResult(
      types: memberExtractor.types,
      imports: importCollector.imports,
      previews: previewExtractor.previews,
      skeletonHash: skeletonHash,
      referencedTypes: Array(typeRefCollector.referencedTypes).sorted(),
      definedTypes: typeRefCollector.definedTypes
    )
  }

  // MARK: - Skeleton Hash

  /// Computes SHA-256 from the token stream with body regions and trivia stripped out.
  /// Body regions are: computed property bodies and method bodies inside
  /// type declarations, and #Preview block bodies.
  /// Trivia (comments, whitespace) is excluded so that formatting-only changes
  /// do not alter the hash.
  private func computeSkeletonHash(
    source: String,
    bodyRanges: [Range<String.Index>]
  ) -> String {
    // Convert bodyRanges to UTF-8 offset ranges for fast comparison with token positions.
    let utf8View = source.utf8
    let excludedRanges =
      bodyRanges
      .map {
        utf8View.distance(
          from: utf8View.startIndex, to: $0.lowerBound)..<utf8View.distance(
            from: utf8View.startIndex, to: $0.upperBound)
      }
      .sorted { $0.lowerBound < $1.lowerBound }

    var skeleton = ""
    for token in tree.tokens(viewMode: .sourceAccurate) {
      let tokenStart = token.positionAfterSkippingLeadingTrivia.utf8Offset
      let tokenEnd = token.endPositionBeforeTrailingTrivia.utf8Offset
      // Skip tokens that fall inside a body range.
      if excludedRanges.contains(where: { $0.lowerBound <= tokenStart && tokenEnd <= $0.upperBound }
      ) {
        continue
      }
      skeleton += token.text
    }

    return sha256(skeleton)
  }

  private func sha256(_ string: String) -> String {
    let data = Array(string.utf8)
    return commonCryptoSHA256(data)
  }
}

// MARK: - SHA-256 via CommonCrypto

private func commonCryptoSHA256(_ data: [UInt8]) -> String {
  var digest = [UInt8](repeating: 0, count: Int(CC_SHA256_DIGEST_LENGTH))
  _ = data.withUnsafeBufferPointer { bufferPointer in
    CC_SHA256(bufferPointer.baseAddress, CC_LONG(data.count), &digest)
  }
  return digest.map { String(format: "%02x", $0) }.joined()
}
