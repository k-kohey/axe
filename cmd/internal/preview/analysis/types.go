package analysis

// PropertyInfo describes a computed property inside a Swift type.
type PropertyInfo struct {
	Name     string // "body", "backgroundColor", etc.
	TypeExpr string // "some View", "Color", etc.
	BodyLine int
	Source   string
}

// MethodInfo describes a method (func) inside a Swift type.
type MethodInfo struct {
	Name      string // "greet"
	Selector  string // "greet(name:)" — @_dynamicReplacement 用セレクタ
	Signature string // "(name: String) -> String" — ( から { の直前まで
	BodyLine  int
	Source    string
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
	StartLine int
	Title     string // e.g. "Dark Mode", empty for unnamed
	Source    string
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
