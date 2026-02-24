import ArgumentParser
import Foundation
import IndexStore

@main
struct AxeIndexReader: ParsableCommand {
  static let configuration = CommandConfiguration(
    commandName: "axe-index-reader",
    abstract: "Read Index Store and output type/member/reference data as JSON"
  )

  @Argument(help: "Path to the index store (e.g. DerivedData/Build/Index.noindex/DataStore)")
  var indexStorePath: String

  @Option(name: .long, help: "Project source root to filter files")
  var sourceRoot: String? = nil

  func run() throws {
    let result = try readIndexStore(indexStorePath: indexStorePath, sourceRoot: sourceRoot)

    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    let data = try encoder.encode(result)

    guard let json = String(data: data, encoding: .utf8) else {
      throw ValidationError("Failed to encode JSON")
    }
    print(json)
  }
}

// MARK: - Output types (protojson-compatible with IndexStoreResult)

struct IndexStoreResultOutput: Codable, Sendable {
  let files: [IndexFileDataOutput]
  let typeFileMap: [String: String]
}

struct IndexFileDataOutput: Codable, Sendable {
  let filePath: String
  let types: [IndexTypeInfoOutput]
  let referencedTypeNames: [String]
  let definedTypeNames: [String]
}

struct IndexTypeInfoOutput: Codable, Sendable {
  let name: String
  let kind: Int  // TypeKind enum value (integer for protojson compat)
  let accessLevel: String
  let inheritedTypes: [String]
  let members: [IndexMemberInfoOutput]
  let line: Int32
  let column: Int32
}

struct IndexMemberInfoOutput: Codable, Sendable {
  let name: String
  let kind: Int  // MemberKind enum value
  let accessLevel: String
  let line: Int32
  let column: Int32
  let isComputed: Bool
}

// MARK: - Internal builder types

private struct TypeBuilder {
  let name: String
  let kind: Int
  let accessLevel: String
  var inheritedTypes: Set<String> = []
  var members: [MemberBuilder] = []
  let line: Int32
  let column: Int32
}

private struct MemberBuilder {
  let name: String
  let kind: Int
  let accessLevel: String
  let line: Int32
  let column: Int32
  let usr: String
}

/// Per-file accumulator for Index Store data.
private struct FileAccumulator {
  var types: [String: TypeBuilder] = [:]  // USR → TypeBuilder
  var referencedTypeNames: Set<String> = []
  var definedTypeNames: Set<String> = []
}

// MARK: - Core logic

/// Reads the Index Store and returns rich per-file data including types,
/// members, references, and definitions. Also includes the type→file map
/// for backward compatibility with dependency resolution.
func readIndexStore(
  indexStorePath: String, sourceRoot: String?
) throws -> IndexStoreResultOutput {
  let store: IndexStore
  do {
    store = try IndexStore(path: indexStorePath)
  } catch {
    throw ValidationError("Failed to open index store at \(indexStorePath): \(error)")
  }

  let resolvedRoot: String? = sourceRoot.map {
    URL(fileURLWithPath: $0).standardized.path
  }

  let typeKinds: Set<SymbolKind> = [.struct, .class, .enum]
  let memberKinds: Set<SymbolKind> = [
    .instanceProperty, .classProperty, .staticProperty,
    .instanceMethod, .classMethod, .staticMethod,
    .constructor,
  ]

  var fileAccumulators: [String: FileAccumulator] = [:]
  var typeFileMap: [String: String] = [:]
  var computedPropertyUSRs: Set<String> = []

  for unit in store.units {
    // Skip system units (SDK frameworks, toolchain modules).
    if unit.isSystem { continue }

    let mainFile = unit.mainFile
    guard !mainFile.isEmpty else { continue }

    // If sourceRoot is specified, only include files under it.
    if let root = resolvedRoot {
      let resolvedMain = URL(fileURLWithPath: mainFile).standardized.path
      guard resolvedMain.hasPrefix(root) else { continue }
    }

    guard let recordName = unit.recordName else { continue }

    let recordReader: RecordReader
    do {
      recordReader = try RecordReader(indexStore: store, recordName: recordName)
    } catch {
      // Skip corrupted or missing records.
      continue
    }

    if fileAccumulators[mainFile] == nil {
      fileAccumulators[mainFile] = FileAccumulator()
    }

    // swift-format-ignore: ReplaceForEachWithForLoop
    // RecordReader provides forEach but not Sequence conformance.
    recordReader.forEach { occurrence in
      let symbol = occurrence.symbol
      let name = symbol.name
      guard !name.isEmpty, !name.hasPrefix("$") else { return }

      // --- Type definitions ---
      if occurrence.roles.contains(.definition), typeKinds.contains(symbol.kind) {
        let usr = symbol.usr

        if fileAccumulators[mainFile]?.types[usr] == nil {
          fileAccumulators[mainFile]?.types[usr] = TypeBuilder(
            name: name,
            kind: symbolKindToTypeKind(symbol.kind),
            accessLevel: accessLevel(from: symbol.properties),
            line: Int32(occurrence.location.line),
            column: Int32(occurrence.location.column)
          )
        }

        // Collect inherited types from baseOf relations.
        // swift-format-ignore: ReplaceForEachWithForLoop
        occurrence.forEach { relatedSymbol, roles in
          if roles.contains(.baseOf) {
            fileAccumulators[mainFile]?.types[usr]?.inheritedTypes.insert(relatedSymbol.name)
          }
        }

        // Track defined type names per file.
        fileAccumulators[mainFile]?.definedTypeNames.insert(name)

        // First definition wins for type → file map.
        if typeFileMap[name] == nil {
          typeFileMap[name] = mainFile
        }
      }

      // --- Member definitions ---
      if occurrence.roles.contains(.definition), memberKinds.contains(symbol.kind) {
        let member = MemberBuilder(
          name: name,
          kind: symbolKindToMemberKind(symbol.kind),
          accessLevel: accessLevel(from: symbol.properties),
          line: Int32(occurrence.location.line),
          column: Int32(occurrence.location.column),
          usr: symbol.usr
        )

        // Find parent type via childOf relation and add member.
        // swift-format-ignore: ReplaceForEachWithForLoop
        occurrence.forEach { relatedSymbol, roles in
          if roles.contains(.childOf) {
            fileAccumulators[mainFile]?.types[relatedSymbol.usr]?.members.append(member)
          }
        }
      }

      // --- Accessor definitions → mark property as computed ---
      if occurrence.roles.contains(.definition), symbol.subkind == .accessorGetter {
        // swift-format-ignore: ReplaceForEachWithForLoop
        occurrence.forEach { relatedSymbol, roles in
          if roles.contains(.accessorOf) {
            computedPropertyUSRs.insert(relatedSymbol.usr)
          }
        }
      }

      // --- Type references ---
      if occurrence.roles.contains(.reference), typeKinds.contains(symbol.kind) {
        fileAccumulators[mainFile]?.referencedTypeNames.insert(name)
      }
    }
  }

  // Build output.
  var files: [IndexFileDataOutput] = []

  for (filePath, acc) in fileAccumulators {
    var types: [IndexTypeInfoOutput] = []

    for (_, builder) in acc.types.sorted(by: { $0.value.line < $1.value.line }) {
      let members = builder.members.map { member in
        IndexMemberInfoOutput(
          name: member.name,
          kind: member.kind,
          accessLevel: member.accessLevel,
          line: member.line,
          column: member.column,
          isComputed: computedPropertyUSRs.contains(member.usr)
        )
      }

      types.append(
        IndexTypeInfoOutput(
          name: builder.name,
          kind: builder.kind,
          accessLevel: builder.accessLevel,
          inheritedTypes: Array(builder.inheritedTypes).sorted(),
          members: members,
          line: builder.line,
          column: builder.column
        ))
    }

    files.append(
      IndexFileDataOutput(
        filePath: filePath,
        types: types,
        referencedTypeNames: Array(acc.referencedTypeNames).sorted(),
        definedTypeNames: Array(acc.definedTypeNames).sorted()
      ))
  }

  // Sort files by path for deterministic output.
  files.sort { $0.filePath < $1.filePath }

  return IndexStoreResultOutput(
    files: files,
    typeFileMap: typeFileMap
  )
}

// MARK: - Conversion helpers

private func symbolKindToTypeKind(_ kind: SymbolKind) -> Int {
  if kind == .struct { return 1 }  // TYPE_KIND_STRUCT
  if kind == .class { return 2 }  // TYPE_KIND_CLASS
  if kind == .enum { return 3 }  // TYPE_KIND_ENUM
  return 0  // TYPE_KIND_UNKNOWN
}

private func symbolKindToMemberKind(_ kind: SymbolKind) -> Int {
  if kind == .instanceProperty { return 1 }  // MEMBER_KIND_INSTANCE_PROPERTY
  if kind == .instanceMethod { return 2 }  // MEMBER_KIND_INSTANCE_METHOD
  if kind == .staticProperty || kind == .classProperty { return 3 }  // MEMBER_KIND_STATIC_PROPERTY
  if kind == .staticMethod || kind == .classMethod { return 4 }  // MEMBER_KIND_STATIC_METHOD
  if kind == .constructor { return 5 }  // MEMBER_KIND_CONSTRUCTOR
  return 0  // MEMBER_KIND_UNKNOWN
}

private func accessLevel(from properties: SymbolProperty) -> String {
  if properties.contains(.swiftAccessControlPublic) { return "public" }
  if properties.contains(.swiftAccessControlPackage) { return "package" }
  if properties.contains(.swiftAccessControlInternal) { return "internal" }
  if properties.contains(.swiftAccessControlFileprivate) { return "fileprivate" }
  if properties.contains(.swiftAccessControlLessThanFileprivate) { return "private" }
  return "internal"  // Default access level in Swift
}
