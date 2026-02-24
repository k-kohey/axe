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
    return .visitChildren
  }
  override func visitPost(_ node: StructDeclSyntax) { popTypeName() }

  override func visit(_ node: ClassDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    return .visitChildren
  }
  override func visitPost(_ node: ClassDeclSyntax) { popTypeName() }

  override func visit(_ node: EnumDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    return .visitChildren
  }
  override func visitPost(_ node: EnumDeclSyntax) { popTypeName() }

  override func visit(_ node: ActorDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.name.text)
    return .visitChildren
  }
  override func visitPost(_ node: ActorDeclSyntax) { popTypeName() }

  override func visit(_ node: ExtensionDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeName(node.extendedType.trimmedDescription)
    return .visitChildren
  }
  override func visitPost(_ node: ExtensionDeclSyntax) { popTypeName() }

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
        MemberSource(
          typeName: typeName,
          line: declLine,
          kind: .property,
          name: propName,
          typeExpr: typeExpr,
          signature: "",
          selector: "",
          bodyLine: bodyLine,
          source: bodySource
        ))

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
      MemberSource(
        typeName: typeName,
        line: declLine,
        kind: .method,
        name: funcName,
        typeExpr: "",
        signature: signature,
        selector: selector,
        bodyLine: bodyLine,
        source: bodySource
      ))

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
      MemberSource(
        typeName: typeName,
        line: declLine,
        kind: .method,
        name: "init",
        typeExpr: "",
        signature: signature,
        selector: selector,
        bodyLine: bodyLine,
        source: bodySource
      ))

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
