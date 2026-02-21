import SwiftSyntax

/// Collects type references and type definitions from Swift source.
final class TypeReferenceCollector: SyntaxVisitor {
  /// Type names referenced in this file (type annotations, generic arguments, etc.).
  private(set) var referencedTypes: Set<String> = []
  /// Type names defined in this file (struct, class, enum, actor declarations).
  private(set) var definedTypes: [String] = []

  /// Standard library and SwiftUI types to exclude from referencedTypes.
  private static let filteredTypes: Set<String> = [
    // Swift stdlib
    "String", "Int", "Int8", "Int16", "Int32", "Int64",
    "UInt", "UInt8", "UInt16", "UInt32", "UInt64",
    "Float", "Double", "Bool", "Void", "Never",
    "Array", "Dictionary", "Set", "Optional",
    "Any", "AnyObject", "Error", "Codable", "Hashable",
    "Equatable", "Comparable", "Identifiable", "Sendable",
    "Result", "Data", "URL", "Date", "UUID",
    // SwiftUI
    "View", "Text", "Image", "Button", "HStack", "VStack", "ZStack",
    "List", "ForEach", "NavigationView", "NavigationStack", "NavigationLink",
    "ScrollView", "Form", "Section", "Group", "Spacer", "Divider",
    "Color", "Font", "Edge", "Alignment", "Binding", "State",
    "ObservedObject", "StateObject", "EnvironmentObject", "Environment",
    "Published", "ObservableObject", "AnyView", "EmptyView",
    "GeometryReader", "LazyVStack", "LazyHStack", "LazyVGrid", "LazyHGrid",
    "Toggle", "Slider", "Stepper", "Picker", "DatePicker", "TextField",
    "SecureField", "TextEditor", "Label", "ProgressView", "Alert",
    "Sheet", "TabView", "ToolbarItem", "Menu", "ContextMenu",
    "Path", "Shape", "Circle", "Rectangle", "RoundedRectangle", "Capsule",
    "LinearGradient", "RadialGradient", "AngularGradient",
    "Animation", "Transaction", "Namespace",
    "FetchRequest", "AppStorage", "SceneStorage",
    "some", "Self",
    // UIKit
    "UIHostingController", "UIViewController", "UIView",
    // Foundation
    "NSObject", "CGFloat", "CGPoint", "CGSize", "CGRect",
  ]

  init() {
    super.init(viewMode: .sourceAccurate)
  }

  // MARK: - Type References

  override func visit(_ node: IdentifierTypeSyntax) -> SyntaxVisitorContinueKind {
    let name = node.name.text
    if !Self.filteredTypes.contains(name) {
      referencedTypes.insert(name)
    }
    return .visitChildren
  }

  /// Captures type references in expression position (e.g. `ChildView(title: "Hi")`).
  /// Uses Swift's naming convention (UpperCamelCase = type) as a heuristic.
  override func visit(_ node: DeclReferenceExprSyntax) -> SyntaxVisitorContinueKind {
    let name = node.baseName.text
    if let first = name.first, first.isUppercase, !Self.filteredTypes.contains(name) {
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
