package preview

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// buildSettings holds values extracted from xcodebuild -showBuildSettings.
type buildSettings struct {
	ModuleName       string
	BundleID         string // axe-prefixed bundle ID (used for terminate/launch)
	OriginalBundleID string // original bundle ID from xcodebuild
	BuiltProductsDir string
	DeploymentTarget string
	SwiftVersion     string

	// Fields below are populated from the swiftc response file after build.
	ExtraIncludePaths   []string // additional -I paths (SPM C module headers)
	ExtraFrameworkPaths []string // additional -F paths (e.g. PackageFrameworks)
	ExtraModuleMapFiles []string // -fmodule-map-file= paths (generated ObjC module maps)
}

// propertyInfo describes a computed property inside a Swift type.
type propertyInfo struct {
	Name     string // "body", "backgroundColor", etc.
	TypeExpr string // "some View", "Color", etc.
	BodyLine int
	Source   string
}

// methodInfo describes a method (func) inside a Swift type.
type methodInfo struct {
	Name      string // "greet"
	Selector  string // "greet(name:)" — @_dynamicReplacement 用セレクタ
	Signature string // "(name: String) -> String" — ( から { の直前まで
	BodyLine  int
	Source    string
}

// typeInfo describes a Swift type parsed from a source file.
type typeInfo struct {
	Name           string
	Kind           string
	AccessLevel    string
	InheritedTypes []string
	Properties     []propertyInfo
	Methods        []methodInfo
}

// isView returns true if this type conforms to SwiftUI.View.
func (t typeInfo) isView() bool {
	for _, inherited := range t.InheritedTypes {
		if inherited == "View" || inherited == "SwiftUI.View" {
			return true
		}
	}
	return false
}

// fileThunkData holds one file's worth of data for combined thunk generation.
type fileThunkData struct {
	FileName string     // e.g. "ChildView.swift"
	AbsPath  string     // absolute path to the source file
	Types    []typeInfo // types with computed properties/methods
	Imports  []string   // non-SwiftUI imports
}

// previewBlock describes a #Preview { ... } block in the source.
type previewBlock struct {
	StartLine int
	Title     string // e.g. "Dark Mode", empty for unnamed
	Source    string
}

// previewableProperty holds a single @Previewable declaration
// extracted from a #Preview block, with the @Previewable prefix removed.
type previewableProperty struct {
	Source string // e.g. "@State var modelData = ModelData()"
}

// transformedPreview holds the result of transforming a #Preview block:
// @Previewable lines become wrapper struct properties, the rest becomes body source.
type transformedPreview struct {
	Properties []previewableProperty
	BodySource string
}

// ProjectConfig abstracts --project / --workspace + --scheme.
// Paths are stored as absolute paths.
type ProjectConfig struct {
	Project       string
	Workspace     string
	Scheme        string
	Configuration string // e.g. "Debug", "Release"; empty means xcodebuild default
}

// NewProjectConfig creates a ProjectConfig with absolute paths resolved.
func NewProjectConfig(project, workspace, scheme, configuration string) (ProjectConfig, error) {
	pc := ProjectConfig{Scheme: scheme, Configuration: configuration}
	if workspace != "" {
		abs, err := filepath.Abs(workspace)
		if err != nil {
			return pc, fmt.Errorf("resolving workspace path: %w", err)
		}
		pc.Workspace = abs
	}
	if project != "" {
		abs, err := filepath.Abs(project)
		if err != nil {
			return pc, fmt.Errorf("resolving project path: %w", err)
		}
		pc.Project = abs
	}
	return pc, nil
}

// xcodebuildArgs returns the project/workspace arguments for xcodebuild.
func (pc ProjectConfig) xcodebuildArgs() []string {
	var args []string
	if pc.Workspace != "" {
		args = []string{"-workspace", pc.Workspace, "-scheme", pc.Scheme}
	} else {
		args = []string{"-project", pc.Project, "-scheme", pc.Scheme}
	}
	if pc.Configuration != "" {
		args = append(args, "-configuration", pc.Configuration)
	}
	return args
}

// primaryPath returns the workspace or project path (whichever is set).
func (pc ProjectConfig) primaryPath() string {
	if pc.Workspace != "" {
		return pc.Workspace
	}
	return pc.Project
}

// watchContext holds immutable configuration for the watch loop.
// These values are set once during initialization and never modified.
type watchContext struct {
	device        string // simulator device identifier for simctl
	deviceSetPath string // custom device set path for simctl --set
	loaderPath    string // path to the compiled loader binary
	serve         bool   // true when running in serve mode (IDE integration)
}

// watchEvents groups external event channels for the watch loop.
type watchEvents struct {
	idbErr   <-chan error    // idb_companion error channel (nil when not in serve mode)
	bootDied <-chan struct{} // closed when boot companion process exits unexpectedly
}

// previewDirs manages temp directories scoped per project path.
// Session-specific resources (Thunk, Loader, Staging, Socket) live under
// devices/<udid>/ so that multiple preview processes for the same project
// do not collide. Build artifacts are shared at the project level.
type previewDirs struct {
	Root    string // ~/.cache/axe/preview-<project-hash>
	Build   string // Root/build (shared across sessions)
	Session string // Root/devices/<device-udid>
	Thunk   string // Session/thunk
	Loader  string // Session/loader
	Staging string // Session/staging
	Socket  string // Session/loader.sock
}

// newPreviewDirs creates a previewDirs based on a hash of the project/workspace
// path, with session-specific directories scoped by deviceUDID.
// Uses ~/Library/Caches/axe/ instead of /tmp so that dylibs are accessible
// from within the iOS Simulator via dlopen (separated runtimes cannot resolve
// host /tmp paths).
func newPreviewDirs(projectPath string, deviceUDID string) previewDirs {
	abs, _ := filepath.Abs(projectPath)
	h := sha256.Sum256([]byte(abs))
	short := fmt.Sprintf("%x", h[:8])

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	root := filepath.Join(cacheDir, "axe", "preview-"+short)
	session := filepath.Join(root, "devices", deviceUDID)
	return previewDirs{
		Root:    root,
		Build:   filepath.Join(root, "build"),
		Session: session,
		Thunk:   filepath.Join(session, "thunk"),
		Loader:  filepath.Join(session, "loader"),
		Staging: filepath.Join(session, "staging"),
		Socket:  filepath.Join(session, "loader.sock"),
	}
}
