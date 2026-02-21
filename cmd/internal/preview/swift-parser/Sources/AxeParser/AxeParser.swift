import ArgumentParser
import AxeParserCore
import Foundation

@main
struct AxeParserCLI: ParsableCommand {
  static let configuration = CommandConfiguration(
    commandName: "axe-parser",
    abstract: "Parse Swift source files using swift-syntax",
    subcommands: [Parse.self]
  )
}

struct Parse: ParsableCommand {
  static let configuration = CommandConfiguration(
    abstract: "Parse a Swift file and output JSON"
  )

  @Argument(help: "Path to the Swift source file")
  var filePath: String

  func run() throws {
    let url = URL(fileURLWithPath: filePath)
    let source = try String(contentsOf: url, encoding: .utf8)

    let analyzer = SwiftAnalyzer(source: source)
    let result = analyzer.analyze()

    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    let data = try encoder.encode(result)

    guard let json = String(data: data, encoding: .utf8) else {
      throw ValidationError("Failed to encode JSON")
    }
    print(json)
  }
}
