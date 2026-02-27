// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "SwiftAnalysis",
    platforms: [.macOS(.v13)],
    products: [
        .executable(name: "axe-parser", targets: ["AxeParser"]),
        .executable(name: "axe-index-reader", targets: ["AxeIndexReader"]),
        .library(name: "AxeParserCore", targets: ["AxeParserCore"]),
    ],
    dependencies: [
        .package(url: "https://github.com/swiftlang/swift-syntax.git", from: "600.0.1"),
        .package(url: "https://github.com/apple/swift-argument-parser.git", from: "1.3.0"),
        .package(url: "https://github.com/MobileNativeFoundation/swift-index-store", revision: "c4665d1c0897f45add476bb78692cbf1821c0e7a"),
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.28.1"),
    ],
    targets: [
        .target(
            name: "AxeAnalysisProto",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ],
            path: "Sources/AxeAnalysisProto"
        ),
        .target(
            name: "AxeParserCore",
            dependencies: [
                .product(name: "SwiftParser", package: "swift-syntax"),
                .product(name: "SwiftSyntax", package: "swift-syntax"),
                "AxeAnalysisProto",
            ]
        ),
        .executableTarget(
            name: "AxeParser",
            dependencies: [
                "AxeParserCore",
                .product(name: "ArgumentParser", package: "swift-argument-parser"),
            ]
        ),
        .executableTarget(
            name: "AxeIndexReader",
            dependencies: [
                "AxeAnalysisProto",
                .product(name: "IndexStore", package: "swift-index-store"),
                .product(name: "ArgumentParser", package: "swift-argument-parser"),
            ]
        ),
        .testTarget(
            name: "AxeParserTests",
            dependencies: ["AxeParserCore"]
        ),
    ]
)
