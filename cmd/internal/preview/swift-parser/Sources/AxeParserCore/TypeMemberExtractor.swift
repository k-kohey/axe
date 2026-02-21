import SwiftSyntax

/// Extracts type members (computed properties and methods) from Swift source.
final class TypeMemberExtractor: SyntaxVisitor {
  let helper: SourceTextHelper

  private(set) var types: [TypeInfo] = []
  /// Byte ranges of body regions to exclude from skeleton hash.
  private(set) var bodyRanges: [Range<String.Index>] = []

  /// Stack of enclosing type names (struct, class, enum) for building qualified names.
  private var enclosingTypes: [String] = []

  /// Stack-based tracking for nested type declarations.
  private struct TypeContext {
    let qualifiedName: String
    let kind: TypeKind
    let accessLevel: String
    let inheritedTypes: [String]
    var properties: [PropertyInfo]
    var methods: [MethodInfo]
  }
  private var typeStack: [TypeContext] = []

  /// Extensions whose target type was not yet seen at visit time.
  /// Resolved after the walk completes.
  private var pendingExtensions: [TypeContext] = []

  init(helper: SourceTextHelper) {
    self.helper = helper
    super.init(viewMode: .sourceAccurate)
  }

  // MARK: - Type Definitions

  /// Pushes a new type context onto the stack. Called at the start of each type declaration visit.
  private func pushTypeContext(
    name: String, kind: TypeKind, modifiers: DeclModifierListSyntax,
    inheritance: InheritanceClauseSyntax?
  ) {
    enclosingTypes.append(name)
    let qualifiedName = enclosingTypes.joined(separator: ".")
    typeStack.append(
      TypeContext(
        qualifiedName: qualifiedName,
        kind: kind,
        accessLevel: accessLevel(from: modifiers),
        inheritedTypes: extractInheritedTypes(inheritance),
        properties: [],
        methods: []
      ))
  }

  /// Pops the current type context, appending it to `types` if it has any members.
  /// Called at the end of each type declaration visitPost.
  private func popTypeContext() {
    let qualifiedName = enclosingTypes.joined(separator: ".")
    enclosingTypes.removeLast()

    guard let last = typeStack.last, last.qualifiedName == qualifiedName else { return }
    let ctx = typeStack.removeLast()

    if !ctx.properties.isEmpty || !ctx.methods.isEmpty {
      types.append(
        TypeInfo(
          name: ctx.qualifiedName,
          kind: ctx.kind,
          accessLevel: ctx.accessLevel,
          inheritedTypes: ctx.inheritedTypes,
          properties: ctx.properties,
          methods: ctx.methods
        ))
    }
  }

  override func visit(_ node: StructDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeContext(
      name: node.name.text, kind: .struct, modifiers: node.modifiers,
      inheritance: node.inheritanceClause)
    return .visitChildren
  }

  override func visitPost(_ node: StructDeclSyntax) { popTypeContext() }

  override func visit(_ node: ClassDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeContext(
      name: node.name.text, kind: .class, modifiers: node.modifiers,
      inheritance: node.inheritanceClause)
    return .visitChildren
  }

  override func visitPost(_ node: ClassDeclSyntax) { popTypeContext() }

  override func visit(_ node: EnumDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeContext(
      name: node.name.text, kind: .enum, modifiers: node.modifiers,
      inheritance: node.inheritanceClause)
    return .visitChildren
  }

  override func visitPost(_ node: EnumDeclSyntax) { popTypeContext() }

  override func visit(_ node: ActorDeclSyntax) -> SyntaxVisitorContinueKind {
    pushTypeContext(
      name: node.name.text, kind: .actor, modifiers: node.modifiers,
      inheritance: node.inheritanceClause)
    return .visitChildren
  }

  override func visitPost(_ node: ActorDeclSyntax) { popTypeContext() }

  // MARK: - Extension

  override func visit(_ node: ExtensionDeclSyntax) -> SyntaxVisitorContinueKind {
    let typeName = node.extendedType.trimmedDescription
    enclosingTypes.append(typeName)

    let qualifiedName = enclosingTypes.joined(separator: ".")
    let inheritedTypes = extractInheritedTypes(node.inheritanceClause)

    // Always push so that property/method visitors can collect members.
    typeStack.append(
      TypeContext(
        qualifiedName: qualifiedName,
        kind: .unknown,
        accessLevel: "internal",
        inheritedTypes: inheritedTypes,
        properties: [],
        methods: []
      ))

    return .visitChildren
  }

  override func visitPost(_ node: ExtensionDeclSyntax) {
    let qualifiedName = enclosingTypes.joined(separator: ".")
    enclosingTypes.removeLast()

    guard let last = typeStack.last, last.qualifiedName == qualifiedName else { return }
    let ctx = typeStack.removeLast()

    // Merge into existing TypeInfo or defer
    if let idx = types.firstIndex(where: { $0.name == ctx.qualifiedName }) {
      types[idx].properties += ctx.properties
      types[idx].methods += ctx.methods
      // Merge inherited types from the extension
      for inherited in ctx.inheritedTypes {
        if !types[idx].inheritedTypes.contains(inherited) {
          types[idx].inheritedTypes.append(inherited)
        }
      }
    } else if !ctx.properties.isEmpty || !ctx.methods.isEmpty || !ctx.inheritedTypes.isEmpty {
      // Target type not yet seen; defer for post-walk resolution.
      pendingExtensions.append(ctx)
    }
  }

  /// Merges pending extensions into known types.
  /// Called after the walk so that forward-declared extensions
  /// (extension before struct) can be resolved.
  func resolvePendingExtensions() {
    for ctx in pendingExtensions {
      if let idx = types.firstIndex(where: { $0.name == ctx.qualifiedName }) {
        types[idx].properties += ctx.properties
        types[idx].methods += ctx.methods
        for inherited in ctx.inheritedTypes {
          if !types[idx].inheritedTypes.contains(inherited) {
            types[idx].inheritedTypes.append(inherited)
          }
        }
      } else {
        // Extension-only type (original declaration in another file).
        types.append(
          TypeInfo(
            name: ctx.qualifiedName,
            kind: .unknown,
            accessLevel: ctx.accessLevel,
            inheritedTypes: ctx.inheritedTypes,
            properties: ctx.properties,
            methods: ctx.methods
          ))
      }
    }
    pendingExtensions.removeAll()
  }

  // MARK: - Computed Property

  override func visit(_ node: VariableDeclSyntax) -> SyntaxVisitorContinueKind {
    guard !typeStack.isEmpty else { return .visitChildren }

    // Skip properties inside nested types.
    // Only extract properties that belong directly to the type on top of the stack.
    let currentQualifiedName = enclosingTypes.joined(separator: ".")
    guard currentQualifiedName == typeStack.last!.qualifiedName else { return .visitChildren }

    // Skip static/class properties.
    // The thunk generates instance-level @_dynamicReplacement, so static properties
    // cannot be replaced. Additionally, the replacement body runs in an instance context
    // where unqualified references to other static members fail with
    // "static member 'X' cannot be used on instance of type 'Y'".
    for modifier in node.modifiers {
      let kind = modifier.name.tokenKind
      if kind == .keyword(.static) || kind == .keyword(.class) {
        return .visitChildren
      }
    }

    // Skip stored properties (let, var with initializer but no getter)
    for binding in node.bindings {
      guard let accessorBlock = binding.accessorBlock else { continue }

      // Only extract properties with an implicit getter (.getter case).
      // Properties with explicit get/set (.accessors case) are skipped because
      // the thunk template wraps the body with #sourceLocation directives, and
      // Swift's parser cannot recognise `get`/`set` accessor keywords after
      // a #sourceLocation directive, causing "cannot find 'get' in scope" errors.
      // In practice, explicit get/set properties are model-layer computed properties
      // (e.g. `var priority: Priority { get { ... } set { ... } }`), not SwiftUI
      // view bodies, so skipping them has no impact on preview functionality.
      guard case .getter = accessorBlock.accessors else { continue }

      let propName = binding.pattern.trimmedDescription
      let typeExpr = binding.typeAnnotation?.type.trimmedDescription ?? "some View"

      // Body content: lines inside the accessor block braces
      let bodyRange = helper.innerBodyRange(of: accessorBlock)
      let bodySource = helper.extractLines(in: bodyRange)
      let bodyLine = helper.lineNumber(at: bodyRange.lowerBound)

      typeStack[typeStack.count - 1].properties.append(
        PropertyInfo(
          name: propName,
          typeExpr: typeExpr,
          bodyLine: bodyLine,
          source: bodySource
        ))

      // Track for skeleton
      bodyRanges.append(bodyRange)
    }

    return .visitChildren
  }

  // MARK: - Method

  override func visit(_ node: FunctionDeclSyntax) -> SyntaxVisitorContinueKind {
    guard !typeStack.isEmpty else { return .visitChildren }

    // Skip methods inside nested types.
    let currentQualifiedName = enclosingTypes.joined(separator: ".")
    guard currentQualifiedName == typeStack.last!.qualifiedName else { return .visitChildren }

    // Skip static/class methods
    for modifier in node.modifiers {
      let kind = modifier.name.tokenKind
      if kind == .keyword(.static) || kind == .keyword(.class) {
        return .visitChildren
      }
    }

    let funcName = node.name.text

    // Skip init
    if funcName == "init" {
      return .visitChildren
    }

    // Skip generic methods
    if node.genericParameterClause != nil {
      return .visitChildren
    }

    guard let body = node.body else { return .visitChildren }

    let selector = buildSelector(name: funcName, params: node.signature.parameterClause)
    let signature = buildSignature(node.signature)

    let bodyRange = helper.innerBodyRange(of: body)
    let bodySource = helper.extractLines(in: bodyRange)
    let bodyLine = helper.lineNumber(at: bodyRange.lowerBound)

    typeStack[typeStack.count - 1].methods.append(
      MethodInfo(
        name: funcName,
        selector: selector,
        signature: signature,
        bodyLine: bodyLine,
        source: bodySource
      ))

    // Track for skeleton
    bodyRanges.append(bodyRange)

    return .visitChildren
  }

  // MARK: - Helpers

  /// Extracts the access level keyword from declaration modifiers.
  /// Returns "internal" (the Swift default) if no explicit access modifier is present.
  private func accessLevel(from modifiers: DeclModifierListSyntax) -> String {
    for modifier in modifiers {
      switch modifier.name.tokenKind {
      case .keyword(.private): return "private"
      case .keyword(.fileprivate): return "fileprivate"
      case .keyword(.internal): return "internal"
      case .keyword(.public): return "public"
      case .keyword(.open): return "open"
      default: continue
      }
    }
    return "internal"
  }

  private func extractInheritedTypes(_ clause: InheritanceClauseSyntax?) -> [String] {
    guard let clause = clause else { return [] }
    return clause.inheritedTypes.map { $0.type.trimmedDescription }
  }

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
}
