import SwiftSyntax

/// Extracts `#Preview` blocks from Swift source.
final class PreviewExtractor: SyntaxVisitor {
  let helper: SourceTextHelper

  private(set) var previews: [PreviewBlock] = []
  /// Byte ranges of preview body regions to exclude from skeleton hash.
  private(set) var bodyRanges: [Range<String.Index>] = []

  init(helper: SourceTextHelper) {
    self.helper = helper
    super.init(viewMode: .sourceAccurate)
  }

  override func visit(_ node: MacroExpansionExprSyntax) -> SyntaxVisitorContinueKind {
    guard node.macroName.text == "Preview" else { return .skipChildren }
    return handlePreviewMacro(
      arguments: node.arguments,
      trailingClosure: node.trailingClosure,
      position: node.positionAfterSkippingLeadingTrivia
    )
  }

  override func visit(_ node: MacroExpansionDeclSyntax) -> SyntaxVisitorContinueKind {
    guard node.macroName.text == "Preview" else { return .skipChildren }
    return handlePreviewMacro(
      arguments: node.arguments,
      trailingClosure: node.trailingClosure,
      position: node.positionAfterSkippingLeadingTrivia
    )
  }

  private func handlePreviewMacro(
    arguments: LabeledExprListSyntax,
    trailingClosure: ClosureExprSyntax?,
    position: AbsolutePosition
  ) -> SyntaxVisitorContinueKind {
    let startLine = helper.lineNumber(at: position)

    // Extract title from first string literal argument
    var title = ""
    if let firstArg = arguments.first,
      let stringLiteral = firstArg.expression.as(StringLiteralExprSyntax.self)
    {
      title = stringLiteral.segments.map { $0.trimmedDescription }.joined()
    }

    // Body is the trailing closure
    if let closure = trailingClosure {
      let bodyRange = helper.innerBodyRange(of: closure)
      let bodySource = helper.extractLines(in: bodyRange)

      previews.append(
        PreviewBlock(
          startLine: startLine,
          title: title,
          source: bodySource
        ))

      bodyRanges.append(bodyRange)
    }

    return .skipChildren
  }
}
