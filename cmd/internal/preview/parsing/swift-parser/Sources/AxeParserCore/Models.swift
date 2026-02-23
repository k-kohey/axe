import Foundation

public enum TypeKind: String, Codable, Equatable {
  case `struct`, `class`, `enum`, actor, unknown
}

public struct ParseResult: Codable, Equatable {
  public var types: [TypeInfo]
  public var imports: [String]
  public var previews: [PreviewBlock]
  public var skeletonHash: String
  public var referencedTypes: [String]
  public var definedTypes: [String]
}

public struct TypeInfo: Codable, Equatable {
  public var name: String
  public var kind: TypeKind
  public var accessLevel: String
  public var inheritedTypes: [String]
  public var properties: [PropertyInfo]
  public var methods: [MethodInfo]
}

public struct PropertyInfo: Codable, Equatable {
  public var name: String
  public var typeExpr: String
  public var bodyLine: Int
  public var source: String
}

public struct MethodInfo: Codable, Equatable {
  public var name: String
  public var selector: String
  public var signature: String
  public var bodyLine: Int
  public var source: String
}

public struct PreviewBlock: Codable, Equatable {
  public var startLine: Int
  public var title: String
  public var source: String
}
