import ArgumentParser
import Foundation
import IndexStore

@main
struct AxeIndexReader: ParsableCommand {
  static let configuration = CommandConfiguration(
    commandName: "axe-index-reader",
    abstract: "Read Index Store and output a type-to-file mapping as JSON"
  )

  @Argument(help: "Path to the index store (e.g. DerivedData/Build/Index.noindex/DataStore)")
  var indexStorePath: String

  @Option(name: .long, help: "Project source root to filter files")
  var sourceRoot: String? = nil

  func run() throws {
    let map = try readTypeFileMap(indexStorePath: indexStorePath, sourceRoot: sourceRoot)

    // Output as protojson-compatible TypeFileMap: {"types": {"TypeName": "/path/to/file"}}
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    let wrapper = TypeFileMapOutput(types: map)
    let data = try encoder.encode(wrapper)

    guard let json = String(data: data, encoding: .utf8) else {
      throw ValidationError("Failed to encode JSON")
    }
    print(json)
  }
}

/// Output wrapper matching the protobuf TypeFileMap message.
struct TypeFileMapOutput: Codable {
  let types: [String: String]
}

/// Reads the index store and builds a map of user-defined type names to their file paths.
/// System units (SDK, toolchain) are skipped. If sourceRoot is provided, only files
/// under that directory are included.
func readTypeFileMap(
  indexStorePath: String, sourceRoot: String?
) throws -> [String: String] {
  let store: IndexStore
  do {
    store = try IndexStore(path: indexStorePath)
  } catch {
    throw ValidationError("Failed to open index store at \(indexStorePath): \(error)")
  }

  let typeKinds: Set<SymbolKind> = [.struct, .class, .enum]

  // Resolved source root for prefix matching.
  let resolvedRoot: String? = sourceRoot.map {
    URL(fileURLWithPath: $0).standardized.path
  }

  var typeFileMap: [String: String] = [:]

  for unit in store.units {
    // Skip system units (SDK frameworks, toolchain modules).
    if unit.isSystem {
      continue
    }

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

    // swift-format-ignore: ReplaceForEachWithForLoop
    // RecordReader provides forEach but not Sequence conformance.
    recordReader.forEach { occurrence in
      let symbol = occurrence.symbol
      // Only collect type definitions.
      guard occurrence.roles.contains(.definition),
        typeKinds.contains(symbol.kind)
      else {
        return
      }

      let typeName = symbol.name
      // Skip empty names and compiler-generated symbols.
      guard !typeName.isEmpty, !typeName.hasPrefix("$") else { return }

      // First definition wins (stable mapping).
      if typeFileMap[typeName] == nil {
        typeFileMap[typeName] = mainFile
      }
    }
  }

  return typeFileMap
}
