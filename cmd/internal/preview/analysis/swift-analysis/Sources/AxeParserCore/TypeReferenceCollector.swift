import SwiftSyntax

/// Collects type references and type definitions from Swift source.
///
/// No filtering of standard library / framework types is performed here.
/// Instead, the index store type→file map (populated only with user-defined types)
/// acts as the authoritative filter: types not present in the map are simply
/// not resolved to any file. This eliminates the need for an incomplete
/// blacklist and makes dependency resolution correct by construction.
final class TypeReferenceCollector: SyntaxVisitor {
  /// Type names referenced in this file (type annotations, generic arguments, etc.).
  private(set) var referencedTypes: Set<String> = []
  /// Type names defined in this file (struct, class, enum, actor declarations).
  private(set) var definedTypes: [String] = []

  init() {
    super.init(viewMode: .sourceAccurate)
  }

  // MARK: - Type References

  override func visit(_ node: IdentifierTypeSyntax) -> SyntaxVisitorContinueKind {
    referencedTypes.insert(node.name.text)
    return .visitChildren
  }

  /// Captures type references in expression position (e.g. `ChildView(title: "Hi")`).
  /// Uses Swift's naming convention (UpperCamelCase = type) as a heuristic.
  override func visit(_ node: DeclReferenceExprSyntax) -> SyntaxVisitorContinueKind {
    let name = node.baseName.text
    if let first = name.first, first.isUppercase {
      referencedTypes.insert(name)
    }
    return .visitChildren
  }

  // MARK: - Type Definitions

  override func visit(_ node: StructDeclSyntax) -> SyntaxVisitorContinueKind {
    definedTypes.append(node.name.text)
    return .visitChildren
  }

  override func visit(_ node: ClassDeclSyntax) -> SyntaxVisitorContinueKind {
    definedTypes.append(node.name.text)
    return .visitChildren
  }

  override func visit(_ node: EnumDeclSyntax) -> SyntaxVisitorContinueKind {
    definedTypes.append(node.name.text)
    return .visitChildren
  }

  override func visit(_ node: ActorDeclSyntax) -> SyntaxVisitorContinueKind {
    definedTypes.append(node.name.text)
    return .visitChildren
  }
}
