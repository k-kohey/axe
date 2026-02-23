import Foundation

/// Type kind matching proto enum TypeKind integer values.
/// Encodes as integer for protojson compatibility on the Go side.
public enum TypeKind: Int, Codable, Equatable, Sendable {
  case unknown = 0
  case `struct` = 1
  case `class` = 2
  case `enum` = 3
  case actor = 4
}

public struct ParseResult: Codable, Equatable, Sendable {
  public var types: [TypeInfo]
  public var imports: [String]
  public var previews: [PreviewBlock]
  public var skeletonHash: String
  public var referencedTypes: [String]
  public var definedTypes: [String]
}

public struct TypeInfo: Codable, Equatable, Sendable {
  public var name: String
  public var kind: TypeKind
  public var accessLevel: String
  public var inheritedTypes: [String]
  public var properties: [PropertyInfo]
  public var methods: [MethodInfo]
}

public struct PropertyInfo: Codable, Equatable, Sendable {
  public var name: String
  public var typeExpr: String
  public var bodyLine: Int
  public var source: String
}

public struct MethodInfo: Codable, Equatable, Sendable {
  public var name: String
  public var selector: String
  public var signature: String
  public var bodyLine: Int
  public var source: String
}

public struct PreviewBlock: Codable, Equatable, Sendable {
  public var startLine: Int
  public var title: String
  public var source: String
}
