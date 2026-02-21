// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "AxeParser",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "axe-parser", targets: ["AxeParser"]),
        .library(name: "AxeParserCore", targets: ["AxeParserCore"]),
    ],
    dependencies: [
        .package(url: "https://github.com/swiftlang/swift-syntax.git", from: "600.0.1"),
        .package(url: "https://github.com/apple/swift-argument-parser.git", from: "1.3.0"),
    ],
    targets: [
        .target(
            name: "AxeParserCore",
            dependencies: [
                .product(name: "SwiftParser", package: "swift-syntax"),
                .product(name: "SwiftSyntax", package: "swift-syntax"),
            ]
        ),
        .executableTarget(
            name: "AxeParser",
            dependencies: [
                "AxeParserCore",
                .product(name: "ArgumentParser", package: "swift-argument-parser"),
            ]
        ),
        .testTarget(
            name: "AxeParserTests",
            dependencies: ["AxeParserCore"]
        ),
    ]
)
