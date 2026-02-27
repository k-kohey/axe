import SwiftSyntax

/// Extracts source text of type members (properties with accessor blocks, methods with bodies).
///
/// This extractor performs NO filtering — static members, inits, stored properties
/// without accessor blocks, etc. are all included if they have extractable bodies.
/// Filtering (instance-only, computed-only, non-init) is performed on the Go side
/// using Index Store semantic data.
///
/// Two exceptions where the extractor skips members due to thunk template constraints:
/// 1. Explicit get/set properties — `#sourceLocation` breaks Swift parser for accessor keywords
/// 2. Generic methods — `@_dynamicReplacement` does not support generic parameters
final class MemberSourceExtractor: SyntaxVisitor {
  let helper: SourceTextHelper

  private(set) var memberSources: [MemberSource] = []
  /// Byte ranges of body regions to exclude from skeleton hash.
  private(set) var bodyRanges: [Range<String.Index>] = []
  /// Parser-derived access levels for type declarations (qualified name → access level string).
  /// Only populated from type declarations (struct/class/enum/actor), not extensions.
  private(set) var typeAccessLevels: [String: String] = [:]

  /// Stack of enclosing type/extension names. The top element is the direct parent.
  private var typeNameStack: [String] = []

  init(helper: SourceTextHelper) {
    self.helper = helper
    super.init(viewMode: .sourceAccurate)
  }

  // MARK: - Type Context Tracking

  private func pushTypeName(_ name: String) {
    typeNameStack.append(name)
  }

  private func popTypeName() {
    typeNameStack.removeLast()
  }

  /// Returns the fully qualified type name by joining all stack elements with ".".
  /// e.g. ["OuterView", "InnerView"] → "OuterView.InnerView"
  private var currentTypeName: String? {
    guard !typeNameStack.isEmpty else { return nil }
    return typeNameStack.joined(separator: ".")
  }

  override func visit(_ node: StructDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    recordAccessLevel(from: node.modifiers)
    return .visitChildren
  }
  override func visitPost(_ node: StructDeclSyntax) { popTypeName() }

  override func visit(_ node: ClassDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    recordAccessLevel(from: node.modifiers)
    return .visitChildren
  }
  override func visitPost(_ node: ClassDeclSyntax) { popTypeName() }

  override func visit(_ node: EnumDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    recordAccessLevel(from: node.modifiers)
    return .visitChildren
  }
  override func visitPost(_ node: EnumDeclSyntax) { popTypeName() }

  override func visit(_ node: ActorDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    recordAccessLevel(from: node.modifiers)
    return .visitChildren
  }
  override func visitPost(_ node: ActorDeclSyntax) { popTypeName() }

  override func visit(_ node: ExtensionDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.extendedType.trimmedDescription)
    return .visitChildren
  }
  override func visitPost(_ node: ExtensionDeclSyntax) { popTypeName() }

  // MARK: - Access Level Extraction

  /// Records the access level of the current type declaration.
  /// Only called for type declarations (struct/class/enum/actor), not extensions,
  /// because extensions don't define a type's access level.
  private func recordAccessLevel(from modifiers: DeclModifierListSyntax) {
    guard let typeName = currentTypeName else { return }
    // First declaration wins (avoid overwriting with extension-merged duplicates).
    if typeAccessLevels[typeName] == nil {
      typeAccessLevels[typeName] = extractAccessLevel(from: modifiers)
    }
  }

  /// Extracts the access level keyword from a modifier list.
  /// Returns "internal" (Swift default) if no explicit access modifier is present.
  private func extractAccessLevel(from modifiers: DeclModifierListSyntax) -> String {
    for modifier in modifiers {
      switch modifier.name.tokenKind {
      case .keyword(.open): return "open"
      case .keyword(.public): return "public"
      case .keyword(.package): return "package"
      case .keyword(.internal): return "internal"
      case .keyword(.fileprivate): return "fileprivate"
      case .keyword(.private): return "private"
      default: continue
      }
    }
    return "internal"
  }

  // MARK: - Property Extraction

  override func visit(_ node: VariableDeclSyntax) -> SyntaxVisitorContinueKind {
    guard let typeName = currentTypeName else { return .visitChildren }

    for binding in node.bindings {
      guard let accessorBlock = binding.accessorBlock else { continue }

      // Skip explicit get/set properties: #sourceLocation directive breaks
      // Swift parser when wrapping accessor keywords.
      guard case .getter = accessorBlock.accessors else { continue }

      let propName = binding.pattern.trimmedDescription
      let typeExpr = binding.typeAnnotation?.type.trimmedDescription ?? "some View"

      let bodyRange = helper.innerBodyRange(of: accessorBlock)
      let bodySource = helper.extractLines(in: bodyRange)
      let bodyLine = helper.lineNumber(at: bodyRange.lowerBound)

      // Line number: use bindingSpecifier (var/let keyword) position for Index Store matching.
      let declLine = helper.lineNumber(at: node.bindingSpecifier.positionAfterSkippingLeadingTrivia)

      memberSources.append(
        MemberSource.with {
          $0.typeName = typeName
          $0.line = Int32(declLine)
          $0.kind = .property
          $0.name = propName
          $0.typeExpr = typeExpr
          $0.signature = ""
          $0.selector = ""
          $0.bodyLine = Int32(bodyLine)
          $0.source = bodySource
        })

      bodyRanges.append(bodyRange)
    }

    return .visitChildren
  }

  // MARK: - Method Extraction

  override func visit(_ node: FunctionDeclSyntax) -> SyntaxVisitorContinueKind {
    guard let typeName = currentTypeName else { return .visitChildren }

    // Skip generic methods: @_dynamicReplacement does not support generic parameters.
    if node.genericParameterClause != nil {
      return .visitChildren
    }

    guard let body = node.body else { return .visitChildren }

    let funcName = node.name.text
    let selector = buildSelector(name: funcName, params: node.signature.parameterClause)
    let signature = buildSignature(node.signature)

    let bodyRange = helper.innerBodyRange(of: body)
    let bodySource = helper.extractLines(in: bodyRange)
    let bodyLine = helper.lineNumber(at: bodyRange.lowerBound)

    // Line number: use funcKeyword position for Index Store matching.
    let declLine = helper.lineNumber(at: node.funcKeyword.positionAfterSkippingLeadingTrivia)

    memberSources.append(
      MemberSource.with {
        $0.typeName = typeName
        $0.line = Int32(declLine)
        $0.kind = .method
        $0.name = funcName
        $0.typeExpr = ""
        $0.signature = signature
        $0.selector = selector
        $0.bodyLine = Int32(bodyLine)
        $0.source = bodySource
      })

    bodyRanges.append(bodyRange)

    return .visitChildren
  }

  // MARK: - Initializer Extraction

  override func visit(_ node: InitializerDeclSyntax) -> SyntaxVisitorContinueKind {
    guard let typeName = currentTypeName else { return .visitChildren }
    guard let body = node.body else { return .visitChildren }

    // Skip generic initializers: @_dynamicReplacement does not support generic parameters.
    if node.genericParameterClause != nil {
      return .visitChildren
    }

    let selector = buildSelector(name: "init", params: node.signature.parameterClause)
    let signature = buildInitSignature(node.signature)

    let bodyRange = helper.innerBodyRange(of: body)
    let bodySource = helper.extractLines(in: bodyRange)
    let bodyLine = helper.lineNumber(at: bodyRange.lowerBound)

    let declLine = helper.lineNumber(at: node.initKeyword.positionAfterSkippingLeadingTrivia)

    memberSources.append(
      MemberSource.with {
        $0.typeName = typeName
        $0.line = Int32(declLine)
        $0.kind = .method
        $0.name = "init"
        $0.typeExpr = ""
        $0.signature = signature
        $0.selector = selector
        $0.bodyLine = Int32(bodyLine)
        $0.source = bodySource
      })

    bodyRanges.append(bodyRange)

    return .visitChildren
  }

  // MARK: - Helpers

  private func buildSelector(name: String, params: FunctionParameterClauseSyntax) -> String {
    let paramList = params.parameters
    if paramList.isEmpty {
      return "\(name)()"
    }
    var result = "\(name)("
    for param in paramList {
      let label = param.firstName.text
      result += "\(label):"
    }
    result += ")"
    return result
  }

  private func buildSignature(_ sig: FunctionSignatureSyntax) -> String {
    var result = sig.parameterClause.trimmedDescription
    if let effects = sig.effectSpecifiers {
      result += " \(effects.trimmedDescription)"
    }
    if let returnClause = sig.returnClause {
      result += " \(returnClause.trimmedDescription)"
    }
    return result
  }

  private func buildInitSignature(_ sig: FunctionSignatureSyntax) -> String {
    sig.parameterClause.trimmedDescription
  }
}
