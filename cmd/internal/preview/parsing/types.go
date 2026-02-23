package parsing

import "context"

// PropertyInfo describes a computed property inside a Swift type.
type PropertyInfo struct {
	Name     string `json:"name"`     // "body", "backgroundColor", etc.
	TypeExpr string `json:"typeExpr"` // "some View", "Color", etc.
	BodyLine int    `json:"bodyLine"`
	Source   string `json:"source"`
}

// MethodInfo describes a method (func) inside a Swift type.
type MethodInfo struct {
	Name      string `json:"name"`      // "greet"
	Selector  string `json:"selector"`  // "greet(name:)" — @_dynamicReplacement 用セレクタ
	Signature string `json:"signature"` // "(name: String) -> String" — ( から { の直前まで
	BodyLine  int    `json:"bodyLine"`
	Source    string `json:"source"`
}

// TypeInfo describes a Swift type parsed from a source file.
type TypeInfo struct {
	Name           string
	Kind           string
	AccessLevel    string
	InheritedTypes []string
	Properties     []PropertyInfo
	Methods        []MethodInfo
}

// IsView returns true if this type conforms to SwiftUI.View.
func (t TypeInfo) IsView() bool {
	for _, inherited := range t.InheritedTypes {
		if inherited == "View" || inherited == "SwiftUI.View" {
			return true
		}
	}
	return false
}

// FileThunkData holds one file's worth of data for combined thunk generation.
type FileThunkData struct {
	FileName string     // e.g. "ChildView.swift"
	AbsPath  string     // absolute path to the source file
	Types    []TypeInfo // types with computed properties/methods
	Imports  []string   // non-SwiftUI imports
}

// PreviewBlock describes a #Preview { ... } block in the source.
type PreviewBlock struct {
	StartLine int    `json:"startLine"`
	Title     string `json:"title"` // e.g. "Dark Mode", empty for unnamed
	Source    string `json:"source"`
}

// PreviewableProperty holds a single @Previewable declaration
// extracted from a #Preview block, with the @Previewable prefix removed.
type PreviewableProperty struct {
	Source string // e.g. "@State var modelData = ModelData()"
}

// TransformedPreview holds the result of transforming a #Preview block:
// @Previewable lines become wrapper struct properties, the rest becomes body source.
type TransformedPreview struct {
	Properties []PreviewableProperty
	BodySource string
}

// SwiftFileLister provides access to Swift source files in a project.
// This is the minimal interface required by ResolveDependencies.
type SwiftFileLister interface {
	SwiftFiles(ctx context.Context, root string) ([]string, error)
}

// SwiftFileParser extracts type definitions and references from a Swift file.
// ResolveDependencies uses this to determine inter-file dependencies.
type SwiftFileParser interface {
	ParseTypes(path string) (referencedTypes []string, definedTypes []string, err error)
}
