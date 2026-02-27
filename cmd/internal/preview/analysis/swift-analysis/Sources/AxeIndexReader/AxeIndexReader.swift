import ArgumentParser
import AxeAnalysisProto
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

    let json = try result.jsonString()
    print(json)
  }
}

// MARK: - Internal builder types

private struct TypeBuilder {
  let usr: String
  let name: String
  let kind: Int
  let accessLevel: String
  var inheritedTypes: Set<String> = []
  /// Members keyed by USR to deduplicate across multiple Index Store records.
  var membersByUSR: [String: MemberBuilder] = [:]
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
  var moduleName: String = ""
}

// MARK: - Core logic

/// Reads the Index Store and returns rich per-file data including types,
/// members, references, and definitions. Also includes the type→file map
/// for backward compatibility with dependency resolution.
func readIndexStore(
  indexStorePath: String, sourceRoot: String?
) throws -> IndexStoreResult {
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
      fileAccumulators[mainFile] = FileAccumulator(moduleName: unit.moduleName)
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
            usr: usr,
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
        // If the parent type was defined in another file (extension case),
        // create a placeholder TypeBuilder so the member isn't dropped.
        // swift-format-ignore: ReplaceForEachWithForLoop
        occurrence.forEach { relatedSymbol, roles in
          if roles.contains(.childOf) {
            if fileAccumulators[mainFile]?.types[relatedSymbol.usr] == nil {
              fileAccumulators[mainFile]?.types[relatedSymbol.usr] = TypeBuilder(
                usr: relatedSymbol.usr,
                name: relatedSymbol.name,
                kind: symbolKindToTypeKind(relatedSymbol.kind),
                accessLevel: accessLevel(from: relatedSymbol.properties),
                line: 0, column: 0
              )
            }
            fileAccumulators[mainFile]?.types[relatedSymbol.usr]?.membersByUSR[member.usr] = member
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

      // --- Inheritance / conformance references ---
      // Protocol conformances (e.g. `: View`) appear as reference occurrences
      // with a baseOf relation to the conforming type. We collect these here
      // because the type definition occurrence does not always carry them.
      if occurrence.roles.contains(.reference) {
        // swift-format-ignore: ReplaceForEachWithForLoop
        occurrence.forEach { relatedSymbol, roles in
          if roles.contains(.baseOf) {
            fileAccumulators[mainFile]?.types[relatedSymbol.usr]?.inheritedTypes.insert(name)
          }
        }
      }
    }
  }

  // Build output.
  var files: [IndexFileData] = []

  for (filePath, acc) in fileAccumulators {
    var types: [IndexTypeInfo] = []

    for (_, builder) in acc.types.sorted(by: { $0.value.line < $1.value.line }) {
      let members = builder.membersByUSR.values
        .sorted(by: { $0.line < $1.line })
        .map { member in
          IndexMemberInfo.with {
            $0.name = member.name
            $0.kind = MemberKind(rawValue: member.kind) ?? .unknown
            $0.accessLevel = member.accessLevel
            $0.line = member.line
            $0.column = member.column
            $0.isComputed = computedPropertyUSRs.contains(member.usr)
            $0.usr = member.usr
          }
        }

      types.append(
        IndexTypeInfo.with {
          $0.name = builder.name
          $0.kind = TypeKind(rawValue: builder.kind) ?? .unknown
          $0.accessLevel = builder.accessLevel
          $0.inheritedTypes = Array(builder.inheritedTypes).sorted()
          $0.members = members
          $0.line = builder.line
          $0.column = builder.column
          $0.usr = builder.usr
        })
    }

    files.append(
      IndexFileData.with {
        $0.filePath = filePath
        $0.types = types
        $0.referencedTypeNames = Array(acc.referencedTypeNames).sorted()
        $0.definedTypeNames = Array(acc.definedTypeNames).sorted()
        $0.moduleName = acc.moduleName
      })
  }

  // Sort files by path for deterministic output.
  files.sort { $0.filePath < $1.filePath }

  return IndexStoreResult.with {
    $0.files = files
    $0.typeFileMap = typeFileMap
  }
}

// MARK: - Conversion helpers

private func symbolKindToTypeKind(_ kind: SymbolKind) -> Int {
  if kind == .struct { return TypeKind.`struct`.rawValue }
  if kind == .class { return TypeKind.`class`.rawValue }
  if kind == .enum { return TypeKind.`enum`.rawValue }
  return TypeKind.unknown.rawValue
}

private func symbolKindToMemberKind(_ kind: SymbolKind) -> Int {
  if kind == .instanceProperty { return MemberKind.instanceProperty.rawValue }
  if kind == .instanceMethod { return MemberKind.instanceMethod.rawValue }
  if kind == .staticProperty || kind == .classProperty { return MemberKind.staticProperty.rawValue }
  if kind == .staticMethod || kind == .classMethod { return MemberKind.staticMethod.rawValue }
  if kind == .constructor { return MemberKind.constructor.rawValue }
  return MemberKind.unknown.rawValue
}

private func accessLevel(from properties: SymbolProperty) -> String {
  // NOTE: The Index Store SDK does not reliably populate access level
  // properties (rawValue is 0 for most symbols). The access level returned
  // here may be inaccurate — callers should not depend on it for critical
  // filtering decisions. The Go-side parser_swift.go supplements this with
  // parser-derived access levels from type_access_levels.
  if properties.contains(.swiftAccessControlPublic) { return "public" }
  if properties.contains(.swiftAccessControlPackage) { return "package" }
  if properties.contains(.swiftAccessControlInternal) { return "internal" }
  if properties.contains(.swiftAccessControlFileprivate) { return "fileprivate" }
  if properties.contains(.swiftAccessControlLessThanFileprivate) { return "private" }
  return "internal"  // Default access level in Swift
}
