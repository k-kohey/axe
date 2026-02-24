import SwiftSyntax

/// Collects import declarations from Swift source, excluding SwiftUI.
/// Imports inside `#if` conditional compilation blocks are skipped because
/// the thunk cannot evaluate conditions like `canImport(AppKit)` and the
/// essential imports (SwiftUI, UIKit) are already added by the thunk template.
final class ImportCollector: SyntaxVisitor {
  private(set) var imports: [String] = []

  init() {
    super.init(viewMode: .sourceAccurate)
  }

  override func visit(_ node: IfConfigDeclSyntax) -> SyntaxVisitorContinueKind {
    .skipChildren
  }

  override func visit(_ node: ImportDeclSyntax) -> SyntaxVisitorContinueKind {
    let text = node.trimmedDescription
    let moduleName = node.path.map { $0.name.text }.joined(separator: ".")
    if moduleName != "SwiftUI" {
      imports.append(text)
    }
    return .skipChildren
  }
}
