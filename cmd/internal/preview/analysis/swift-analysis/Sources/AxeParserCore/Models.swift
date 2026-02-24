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

/// Member source kind matching proto enum MemberSourceKind integer values.
public enum MemberSourceKind: Int, Codable, Equatable, Sendable {
  case unknown = 0
  case property = 1
  case method = 2
}

/// Source text extracted from a type member declaration.
/// No filtering is performed — the Go side uses Index Store data to determine
/// which members are relevant (instance, computed, non-init, etc.).
public struct MemberSource: Codable, Equatable, Sendable {
  public var typeName: String  // direct enclosing type/extension name
  public var line: Int  // declaration start line (var/func keyword)
  public var kind: MemberSourceKind
  public var name: String  // "body", "greet"
  public var typeExpr: String  // "some View" (property only)
  public var signature: String  // "(name: String) -> String" (method only)
  public var selector: String  // "greet(name:)" (method only)
  public var bodyLine: Int  // body content start line
  public var source: String  // body text
}

public struct ParseResult: Codable, Equatable, Sendable {
  public var memberSources: [MemberSource]
  public var imports: [String]
  public var previews: [PreviewBlock]
  public var skeletonHash: String
}

public struct PreviewBlock: Codable, Equatable, Sendable {
  public var startLine: Int
  public var title: String
  public var source: String
}
