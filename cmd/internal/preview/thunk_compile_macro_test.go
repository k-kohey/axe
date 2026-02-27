package preview

// SwiftUI macro / property wrapper tests for thunk generation.
//
// SwiftUI relies heavily on macros (@Observable, @Previewable, @Entry)
// and property wrappers (@State, @Binding, @FocusState, @Environment).
// These tests verify that thunk generation handles them correctly.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Macro 1: @FocusState in View
//
// @FocusState is a property wrapper. The View has a stored property
// with @FocusState, and the computed body uses $focusedField.
// ============================================================

const fixtureFocusState = `import SwiftUI

struct FocusView: View {
    enum Field {
        case username
        case password
    }

    @FocusState private var focusedField: Field?

    var body: some View {
        VStack {
            TextField("Username", text: .constant(""))
                .focused($focusedField, equals: .username)
            SecureField("Password", text: .constant(""))
                .focused($focusedField, equals: .password)
        }
    }
}

#Preview {
    FocusView()
}
`

func TestMacro_FocusState(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"FocusView.swift": fixtureFocusState},
		"FocusView.swift",
	)
}

// ============================================================
// Macro 2: @Observable (Swift 5.9 macro)
//
// @Observable generates conformance and storage transformation.
// The thunk must handle computed properties inside @Observable classes.
// ============================================================

const fixtureObservable = `import SwiftUI

@Observable
class UserModel {
    var name: String = "World"

    var greeting: String {
        "Hello, \(name)"
    }
}

struct ObservableView: View {
    var model = UserModel()
    var body: some View {
        Text(model.greeting)
    }
}

#Preview {
    ObservableView()
}
`

func TestMacro_Observable(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"ObservableView.swift": fixtureObservable},
		"ObservableView.swift",
	)
}

// ============================================================
// Macro 3: @Observable with computed property — thunk content check
//
// Verify that the computed property inside @Observable class
// actually gets a @_dynamicReplacement thunk.
// ============================================================

func TestMacro_Observable_PropertyInThunk(t *testing.T) {
	sdk := simulatorSDKPath(t)

	thunkPaths, _ := runThunkCompileTestWithPaths(t, sdk,
		map[string]string{"ObservableView.swift": fixtureObservable},
		"ObservableView.swift",
	)

	foundGreeting := false
	for _, p := range thunkPaths {
		if strings.Contains(filepath.Base(p), "_main") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if strings.Contains(content, "__preview__greeting") {
			foundGreeting = true
		}
	}
	if !foundGreeting {
		t.Error("computed property 'greeting' inside @Observable class is not in thunk — " +
			"@Observable macro may change how Index Store reports members")
	}
}

// Macro 4: @Previewable @FocusState — known bug.
// Moved to thunk_compile_known_bugs_test.go (TestKnownBug_PreviewableFocusState).

// ============================================================
// Macro 5: @Environment with custom key
//
// @Environment(\.customKey) requires the key to be defined.
// Using built-in keys like colorScheme for simplicity.
// ============================================================

const fixtureEnvironmentView = `import SwiftUI

struct ThemeView: View {
    @Environment(\.colorScheme) var colorScheme
    @Environment(\.dynamicTypeSize) var typeSize

    var themeDescription: String {
        colorScheme == .dark ? "dark" : "light"
    }

    var body: some View {
        Text(themeDescription)
    }
}

#Preview {
    ThemeView()
        .environment(\.colorScheme, .dark)
}
`

func TestMacro_EnvironmentProperties(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"ThemeView.swift": fixtureEnvironmentView},
		"ThemeView.swift",
	)
}

// ============================================================
// Macro 6: @Previewable @State with complex type
//
// @Previewable @State with array, dictionary, optional types.
// ============================================================

const fixturePreviewableComplexState = `import SwiftUI

struct ListContentView: View {
    var items: [String]
    var body: some View {
        ForEach(items, id: \.self) { item in
            Text(item)
        }
    }
}

#Preview {
    @Previewable @State var items = ["Apple", "Banana", "Cherry"]
    ListContentView(items: items)
}
`

func TestMacro_PreviewableComplexState(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"ListContentView.swift": fixturePreviewableComplexState},
		"ListContentView.swift",
	)
}

// ============================================================
// Macro 7: @Previewable @State + @Previewable @Binding together
//
// Multiple @Previewable with different wrappers in one preview.
// ============================================================

const fixturePreviewableMultipleWrappers = `import SwiftUI

struct EditorView: View {
    @Binding var text: String
    var isEditing: Bool

    var body: some View {
        if isEditing {
            TextField("Edit", text: $text)
        } else {
            Text(text)
        }
    }
}

#Preview {
    @Previewable @State var editing = true
    @Previewable @Binding var content = "Hello"
    EditorView(text: $content, isEditing: editing)
}
`

func TestMacro_PreviewableMultipleWrappers(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"EditorView.swift": fixturePreviewableMultipleWrappers},
		"EditorView.swift",
	)
}

// ============================================================
// Macro 8: @Observable class as @Previewable @State
//
// Using @Observable class inside @Previewable @State in preview.
// ============================================================

const fixturePreviewableObservable = `import SwiftUI

@Observable
class CounterModel {
    var count = 0
}

struct CounterContentView: View {
    var model: CounterModel
    var body: some View {
        Text("\(model.count)")
    }
}

#Preview {
    @Previewable @State var model = CounterModel()
    CounterContentView(model: model)
}
`

func TestMacro_PreviewableObservable(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"CounterContentView.swift": fixturePreviewableObservable},
		"CounterContentView.swift",
	)
}

// ============================================================
// Macro 9: @AppStorage in View
//
// @AppStorage is a property wrapper backed by UserDefaults.
// ============================================================

const fixtureAppStorage = `import SwiftUI

struct SettingsContentView: View {
    @AppStorage("username") var username = "Guest"

    var displayName: String {
        username.isEmpty ? "Anonymous" : username
    }

    var body: some View {
        Text(displayName)
    }
}

#Preview {
    SettingsContentView()
}
`

func TestMacro_AppStorage(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"SettingsContentView.swift": fixtureAppStorage},
		"SettingsContentView.swift",
	)
}

// ============================================================
// Macro 10: @SceneStorage in View
// ============================================================

const fixtureSceneStorage = `import SwiftUI

struct RestorationView: View {
    @SceneStorage("selectedTab") var selectedTab = 0

    var tabLabel: String {
        "Tab \(selectedTab)"
    }

    var body: some View {
        Text(tabLabel)
    }
}

#Preview {
    RestorationView()
}
`

func TestMacro_SceneStorage(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"RestorationView.swift": fixtureSceneStorage},
		"RestorationView.swift",
	)
}

// ============================================================
// Macro 11: @MainActor annotated View
//
// @MainActor annotation may affect @_dynamicReplacement behavior.
// ============================================================

const fixtureMainActor = `import SwiftUI

@MainActor
struct MainActorView: View {
    var label: String {
        "MainActor"
    }

    var body: some View {
        Text(label)
    }
}

#Preview {
    MainActorView()
}
`

func TestMacro_MainActor(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"MainActorView.swift": fixtureMainActor},
		"MainActorView.swift",
	)
}

// ============================================================
// Macro 12: @MainActor annotated computed property
// ============================================================

const fixtureMainActorProperty = `import SwiftUI

struct ActorPropertyView: View {
    @MainActor
    var configuredLabel: String {
        "Configured"
    }

    var body: some View {
        Text(configuredLabel)
    }
}

#Preview {
    ActorPropertyView()
}
`

func TestMacro_MainActorProperty(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"ActorPropertyView.swift": fixtureMainActorProperty},
		"ActorPropertyView.swift",
	)
}

// ============================================================
// Macro 13: #Preview with no @Previewable but complex body
//
// Preview body with if/else, ForEach, etc.
// ============================================================

const fixturePreviewComplexBody = `import SwiftUI

struct SimpleItemView: View {
    let title: String
    var body: some View {
        Text(title)
    }
}

#Preview {
    VStack {
        ForEach(0..<5) { i in
            SimpleItemView(title: "Item \(i)")
        }
    }
    .padding()
}
`

func TestMacro_PreviewComplexBody(t *testing.T) {
	sdk := simulatorSDKPath(t)

	runThunkCompileTest(t, sdk,
		map[string]string{"SimpleItemView.swift": fixturePreviewComplexBody},
		"SimpleItemView.swift",
	)
}
