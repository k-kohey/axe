import SwiftSyntax

/// Collects import declarations from Swift source, excluding SwiftUI.
///
/// Only `#if` blocks whose **every** import-bearing clause is a simple
/// `canImport(X)` condition are preserved. Mixed chains (e.g.
/// `#if os(macOS) ... #elseif canImport(UIKit)`) and compound conditions
/// (`canImport(X) && DEBUG`) are skipped entirely because the thunk
/// cannot reliably evaluate the non-`canImport` parts.
final class ImportCollector: SyntaxVisitor {
  private(set) var imports: [String] = []

  init() {
    super.init(viewMode: .sourceAccurate)
  }

  // NOTE: Nested #if blocks inside canImport clauses are not recursively
  // processed. This is acceptable because nested conditional imports are
  // extremely rare in practice.
  override func visit(_ node: IfConfigDeclSyntax) -> SyntaxVisitorContinueKind {
    // Pass 1: reject the entire block if any clause that is part of the
    // chain has a non-canImport condition. This prevents partial
    // reconstruction of mixed chains like `#if os(macOS) ... #elseif
    // canImport(UIKit)` which would break mutual-exclusion semantics.
    for clause in node.clauses {
      guard isSimpleCanImport(clause.condition) else { return .skipChildren }
    }

    // Pass 2: all clauses are simple canImport — collect imports.
    var blockLines: [String] = []
    var emittedClauseCount = 0

    for clause in node.clauses {
      guard case .statements(let stmts) = clause.elements else { continue }

      let importsInClause = stmts.compactMap { stmt -> String? in
        guard let importDecl = stmt.item.as(ImportDeclSyntax.self) else { return nil }
        let moduleName = importDecl.path.map { $0.name.text }.joined(separator: ".")
        return moduleName == "SwiftUI" ? nil : importDecl.trimmedDescription
      }
      guard !importsInClause.isEmpty else { continue }

      let directive = emittedClauseCount == 0 ? "#if" : "#elseif"
      blockLines.append("\(directive) \(clause.condition!.trimmedDescription)")
      blockLines.append(contentsOf: importsInClause)
      emittedClauseCount += 1
    }

    if emittedClauseCount > 0 {
      blockLines.append("#endif")
      imports.append(blockLines.joined(separator: "\n"))
    }

    return .skipChildren
  }

  private func isSimpleCanImport(_ condition: ExprSyntax?) -> Bool {
    guard let condition,
      let callExpr = condition.as(FunctionCallExprSyntax.self),
      let declRef = callExpr.calledExpression.as(DeclReferenceExprSyntax.self),
      declRef.baseName.text == "canImport"
    else {
      return false
    }
    return true
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
