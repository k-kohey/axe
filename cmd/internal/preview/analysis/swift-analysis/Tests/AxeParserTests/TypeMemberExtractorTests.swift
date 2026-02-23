import SwiftParser
import SwiftSyntax
import Testing

@testable import AxeParserCore

@Suite("TypeMemberExtractor")
struct TypeMemberExtractorTests {

  /// Helper to run TypeMemberExtractor on source.
  private func extract(from source: String) -> TypeMemberExtractor {
    let helper = SourceTextHelper(source: source)
    let tree = Parser.parse(source: source)
    let extractor = TypeMemberExtractor(helper: helper)
    extractor.walk(tree)
    extractor.resolvePendingExtensions()
    return extractor
  }

  // MARK: - Global Declarations

  @Suite("Global declarations")
  struct GlobalDeclarationTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Global computed variable is not extracted")
    func globalVariableSkipped() {
      let source = """
        var globalConfig: String {
            "default"
        }

        struct HogeView {
            var body: some View { Text("") }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].name == "HogeView")
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].properties[0].name == "body")
    }

    @Test("Global function is not extracted")
    func globalFunctionSkipped() {
      let source = """
        func helperFunction() -> String {
            "hello"
        }

        struct HogeView {
            var body: some View { Text("") }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].methods.isEmpty)
    }
  }

  // MARK: - Property Extraction

  @Suite("Property extraction")
  struct PropertyExtractionTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Extracts computed property with name, type, bodyLine, and source")
    func computedProperty() {
      let source = """
        struct HogeView {
            var title: String {
                "hello"
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      let prop = extractor.types[0].properties[0]
      #expect(prop.name == "title")
      #expect(prop.typeExpr == "String")
      #expect(prop.bodyLine == 3)
      #expect(prop.source.contains("\"hello\""))
    }

    @Test("Extracts single-line computed property correctly")
    func singleLineComputed() {
      let source = """
        struct HogeView {
            var items: [String]
            var count: Int { items.count }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].properties[0].name == "count")
      #expect(extractor.types[0].properties[0].source == "items.count")
    }

    @Test("Skips static computed properties")
    func staticPropertySkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            static var defaultTitle: String { "Untitled" }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].properties[0].name == "body")
    }

    @Test("Skips class computed properties")
    func classPropertySkipped() {
      let source = """
        class HogeController {
            var value: Int { 0 }
            class var shared: HogeController { HogeController() }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].properties[0].name == "value")
    }

    @Test("Does not extract stored properties")
    func storedPropertySkipped() {
      let source = """
        struct HogeView {
            var name: String = "default"
            let count: Int = 0
        }
        """
      let extractor = extract(from: source)
      #expect(extractor.types.isEmpty)
    }

    @Test("Skips property with explicit get/set (incompatible with thunk #sourceLocation)")
    func explicitGetterSkipped() {
      let source = """
        struct HogeView {
            var value: Int {
                get { 42 }
                set { }
            }
        }
        """
      let extractor = extract(from: source)

      // Explicit get/set properties are skipped because the thunk template's
      // #sourceLocation wrapping breaks Swift's accessor keyword parsing.
      #expect(extractor.types.isEmpty)
    }

    @Test("Property without type annotation defaults to some View")
    func propertyWithoutTypeAnnotation() {
      let source = """
        struct HogeView {
            var body: some View {
                Text("hello")
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties[0].name == "body")
      #expect(extractor.types[0].properties[0].typeExpr == "some View")
    }

    @Test("bodyRanges include property body ranges")
    func bodyRangesForProperties() {
      let source = """
        struct HogeView {
            var title: String {
                "hello"
            }
        }
        """
      let extractor = extract(from: source)
      #expect(!extractor.bodyRanges.isEmpty)
    }
  }

  // MARK: - Method Extraction

  @Suite("Method extraction")
  struct MethodExtractionTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Extracts method selector and signature correctly")
    func methodSelectorAndSignature() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }

            func greet(name: String) -> String {
                return "Hello, \\(name)"
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types[0].methods.count == 1)
      let m = extractor.types[0].methods[0]
      #expect(m.name == "greet")
      #expect(m.selector == "greet(name:)")
      #expect(m.signature == "(name: String) -> String")
    }

    @Test("Extracts async throws method signature correctly")
    func asyncThrowsSignature() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }

            func fetch() async throws -> Data {
                Data()
            }
        }
        """
      let extractor = extract(from: source)

      let m = extractor.types[0].methods[0]
      #expect(m.signature == "() async throws -> Data")
    }

    @Test("Skips static and class methods")
    func staticMethodSkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            static func create() -> HogeView { HogeView() }
        }
        """
      let extractor = extract(from: source)
      #expect(extractor.types[0].methods.isEmpty)
    }

    @Test("Skips init")
    func initSkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            init() {}
        }
        """
      let extractor = extract(from: source)
      #expect(extractor.types[0].methods.isEmpty)
    }

    @Test("Skips class methods")
    func classMethodSkipped() {
      let source = """
        class HogeController {
            var body: some View { Text("") }
            class func shared() -> HogeController { HogeController() }
        }
        """
      let extractor = extract(from: source)
      #expect(extractor.types[0].methods.isEmpty)
    }

    @Test("Skips method without body (protocol requirement)")
    func methodWithoutBodySkipped() {
      let source = """
        protocol HogeProtocol {
            func doWork()
        }
        """
      let extractor = extract(from: source)
      // Protocol is not a struct/class/enum/actor, so no type is extracted
      #expect(extractor.types.isEmpty)
    }

    @Test("Skips generic methods")
    func genericMethodSkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            func convert<T>(_ value: T) -> String { "\\(value)" }
        }
        """
      let extractor = extract(from: source)
      #expect(extractor.types[0].methods.isEmpty)
    }

    @Test("bodyRanges include method body ranges")
    func bodyRangesForMethods() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }

            func doWork() {
                print("working")
            }
        }
        """
      let extractor = extract(from: source)
      // body property + doWork method = 2 bodyRanges
      #expect(extractor.bodyRanges.count == 2)
    }
  }

  // MARK: - Type Context Management

  @Suite("Type context management")
  struct TypeContextTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Nested type members do not leak into parent")
    func nestedTypesIsolated() {
      let source = """
        struct HogeView {
            struct HelperView {
                var title: String {
                    "helper"
                }
            }

            var body: some View {
                Text("Hello")
            }
        }
        """
      let extractor = extract(from: source)

      let hogeView = extractor.types.first(where: { $0.name == "HogeView" })!
      let helperView = extractor.types.first(where: { $0.name == "HogeView.HelperView" })!

      #expect(hogeView.properties.count == 1)
      #expect(hogeView.properties[0].name == "body")
      #expect(helperView.properties.count == 1)
      #expect(helperView.properties[0].name == "title")
    }

    @Test("Deeply nested type has correct qualified name")
    func deeplyNestedQualifiedName() {
      let source = """
        struct A {
            struct B {
                struct C {
                    var value: Int { 42 }
                }
                var value: Int { 1 }
            }
            var value: Int { 0 }
        }
        """
      let extractor = extract(from: source)

      let names = extractor.types.map(\.name)
      #expect(names.contains("A.B.C"))
      #expect(names.contains("A.B"))
      #expect(names.contains("A"))
    }

    @Test("open access level is extracted correctly")
    func openAccessLevel() {
      let source = """
        open class HogeController {
            var value: Int { 0 }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].accessLevel == "open")
      #expect(extractor.types[0].kind == .class)
    }

    @Test("Access levels are extracted correctly")
    func accessLevels() {
      let source = """
        private struct PrivateView {
            var value: Int { 0 }
        }
        fileprivate struct FileprivateView {
            var value: Int { 0 }
        }
        struct InternalView {
            var value: Int { 0 }
        }
        public struct PublicView {
            var value: Int { 0 }
        }
        """
      let extractor = extract(from: source)

      let privateType = extractor.types.first(where: { $0.name == "PrivateView" })!
      let fileprivateType = extractor.types.first(where: { $0.name == "FileprivateView" })!
      let internalType = extractor.types.first(where: { $0.name == "InternalView" })!
      let publicType = extractor.types.first(where: { $0.name == "PublicView" })!

      #expect(privateType.accessLevel == "private")
      #expect(fileprivateType.accessLevel == "fileprivate")
      #expect(internalType.accessLevel == "internal")
      #expect(publicType.accessLevel == "public")
    }
  }

  // MARK: - Extension Processing

  @Suite("Extension processing")
  struct ExtensionTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Extension members merge into existing type")
    func extensionMerge() {
      let source = """
        struct HogeView {
            var body: some View {
                Text("hello")
            }
        }

        extension HogeView {
            func reload() {
                print("reload")
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].methods.count == 1)
      #expect(extractor.types[0].methods[0].name == "reload")
    }

    @Test("Extension before struct declaration is resolved via pendingExtensions")
    func extensionBeforeStruct() {
      let source = """
        extension HogeView {
            var subtitle: String {
                "sub"
            }
        }

        struct HogeView {
            var body: some View {
                Text("main")
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].name == "HogeView")
      let propNames = extractor.types[0].properties.map(\.name)
      #expect(propNames.contains("body"))
      #expect(propNames.contains("subtitle"))
    }

    @Test("Extension inheritedTypes are merged")
    func extensionInheritedTypesMerge() {
      let source = """
        struct HogeView: View {
            var body: some View {
                Text("hoge")
            }
        }

        extension HogeView: Identifiable {
            var id: String {
                "hoge-id"
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].inheritedTypes.contains("View"))
      #expect(extractor.types[0].inheritedTypes.contains("Identifiable"))
    }

    @Test("Extension with only inheritedTypes still merges conformance")
    func extensionOnlyInheritedTypes() {
      let source = """
        struct HogeView: View {
            var body: some View {
                Text("hoge")
            }
        }

        extension HogeView: Identifiable {}
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].inheritedTypes.contains("View"))
      #expect(extractor.types[0].inheritedTypes.contains("Identifiable"))
    }

    @Test("Extension-only type (no declaration) gets kind .unknown")
    func extensionOnlyType() {
      let source = """
        extension ExternalType {
            var computed: String {
                "value"
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].name == "ExternalType")
      #expect(extractor.types[0].kind == .unknown)
      #expect(extractor.types[0].properties.count == 1)
    }
  }

  // MARK: - Actor Type

  @Suite("Actor type extraction")
  struct ActorTypeTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Actor type kind is correctly extracted")
    func actorTypeKind() {
      let source = """
        actor HogeManager {
            var status: String {
                "running"
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].kind == .actor)
      #expect(extractor.types[0].name == "HogeManager")
    }
  }

  // MARK: - resolvePendingExtensions

  @Suite("resolvePendingExtensions")
  struct ResolvePendingExtensionsTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    @Test("Pending extension is merged into existing type")
    func pendingMergedIntoExisting() {
      let source = """
        extension HogeView {
            func format(value: Int) -> String {
                "\\(value)"
            }
        }

        struct HogeView {
            var body: some View {
                Text("hoge")
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].methods.count == 1)
      #expect(extractor.types[0].methods[0].name == "format")
    }

    @Test("Extension without matching type declaration creates new TypeInfo")
    func extensionCreatesNewType() {
      let source = """
        extension ExternalModel {
            func process() {
                print("processing")
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].name == "ExternalModel")
      #expect(extractor.types[0].kind == .unknown)
    }
  }

  // MARK: - Known Limitations (hot-reload できない宣言)
  //
  // 以下のテストは、Swift の @_dynamicReplacement では本来差し替え可能だが、
  // 現在の thunk テンプレートの制約により抽出をスキップしている宣言を文書化する。
  // これらのボディを編集すると、hot-reload ではなくフルビルドが発生する。
  //
  // 将来 thunk テンプレートを拡張して対応する場合、該当テストの期待値を
  // 「抽出される」方向に変更すること。

  @Suite("Known limitations — skipped despite @_dynamicReplacement compatibility")
  struct KnownLimitationTests {
    private func extract(from source: String) -> TypeMemberExtractor {
      let helper = SourceTextHelper(source: source)
      let tree = Parser.parse(source: source)
      let extractor = TypeMemberExtractor(helper: helper)
      extractor.walk(tree)
      extractor.resolvePendingExtensions()
      return extractor
    }

    // --- static/class メンバー ---
    // @_dynamicReplacement は static メンバーにも適用可能だが、
    // replacement 自体も static である必要がある。現在の thunk テンプレートは
    // instance-level の replacement のみ生成するため、static メンバーを
    // 抽出すると "static member 'X' cannot be used on instance of type 'Y'" エラーになる。
    // 対応には thunk テンプレートで static replacement を生成する拡張が必要。

    @Test("static computed property is skipped (thunk generates instance-level replacement only)")
    func staticComputedPropertySkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            static var defaultTitle: String { "Untitled" }
        }
        """
      let extractor = extract(from: source)

      let props = extractor.types[0].properties
      #expect(props.count == 1)
      #expect(props[0].name == "body")
      // TODO: static var defaultTitle should be extractable with a static replacement
    }

    @Test("static method is skipped (thunk generates instance-level replacement only)")
    func staticMethodSkipped() {
      let source = """
        struct HogeView {
            var body: some View { Text("") }
            static func create() -> HogeView { HogeView() }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types[0].methods.isEmpty)
      // TODO: static func create() should be extractable with a static replacement
    }

    @Test("class method is skipped (thunk generates instance-level replacement only)")
    func classMethodSkipped() {
      let source = """
        class HogeController {
            var body: some View { Text("") }
            class func shared() -> HogeController { HogeController() }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types[0].methods.isEmpty)
      // TODO: class func shared() should be extractable with a class-level replacement
    }

    // --- explicit get/set ---
    // @_dynamicReplacement は get/set を持つプロパティにも適用可能だが、
    // 現在の thunk テンプレートはボディ全体を #sourceLocation で包む。
    // Swift パーサーは #sourceLocation の後に get/set キーワードを認識できず
    // "cannot find 'get' in scope" エラーになる。
    // 対応には thunk テンプレートで各 accessor を個別に #sourceLocation で
    // 包むか、#sourceLocation を accessor ブロックの外に出す拡張が必要。

    @Test("explicit get/set property is skipped (#sourceLocation breaks accessor keyword parsing)")
    func explicitAccessorPropertySkipped() {
      let source = """
        struct HogeModel {
            var priorityRaw: Int = 0
            var priority: Int {
                get { priorityRaw }
                set { priorityRaw = newValue }
            }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.isEmpty)
      // TODO: var priority should be extractable if thunk wraps each accessor separately
    }

    // --- init ---
    // @_dynamicReplacement は dynamic init にも適用可能だが（class + @objc 等）、
    // 現在のパーサーは init を明示的にスキップしている。
    // init の hot-reload は利用頻度が低く、@objc 制約もあるため優先度は低い。

    @Test("init is skipped (not supported by thunk template)")
    func initSkipped() {
      let source = """
        class HogeController {
            var value: Int { 0 }
            init() {}
            init(value: Int) { self.value }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types[0].methods.isEmpty)
      // TODO: init could be extractable for classes with dynamic dispatch
    }

    // --- グローバル関数・変数 ---
    // @_dynamicReplacement はモジュールレベルの dynamic func にも適用可能だが、
    // 現在のパーサーは型スコープ外の宣言を無視する。
    // thunk テンプレートは extension ベースのため、グローバル宣言の replacement を
    // 生成するにはテンプレート構造の変更が必要。

    @Test("global function is skipped (parser only extracts type members)")
    func globalFunctionSkipped() {
      let source = """
        func formatDate(_ date: Date) -> String {
            date.description
        }

        struct HogeView {
            var body: some View { Text("") }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].methods.isEmpty)
      // TODO: global functions could be extractable with module-level replacement
    }

    @Test("global computed variable is skipped (parser only extracts type members)")
    func globalComputedVariableSkipped() {
      let source = """
        var appVersion: String {
            "1.0.0"
        }

        struct HogeView {
            var body: some View { Text("") }
        }
        """
      let extractor = extract(from: source)

      #expect(extractor.types.count == 1)
      #expect(extractor.types[0].properties.count == 1)
      #expect(extractor.types[0].properties[0].name == "body")
      // TODO: global computed vars could be extractable with module-level replacement
    }
  }
}
