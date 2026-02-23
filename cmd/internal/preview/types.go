package preview

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/codegen"
	"github.com/k-kohey/axe/internal/preview/protocol"
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

// compileConfigFromBS converts buildSettings to codegen.CompileConfig.
func compileConfigFromBS(bs *buildSettings) codegen.CompileConfig {
	return codegen.CompileConfig{
		ModuleName:          bs.ModuleName,
		BuiltProductsDir:    bs.BuiltProductsDir,
		DeploymentTarget:    bs.DeploymentTarget,
		SwiftVersion:        bs.SwiftVersion,
		ExtraIncludePaths:   bs.ExtraIncludePaths,
		ExtraFrameworkPaths: bs.ExtraFrameworkPaths,
		ExtraModuleMapFiles: bs.ExtraModuleMapFiles,
	}
}

// watchState holds mutable state for the watch loop, protected by a mutex.
// Immutable configuration (device, loaderPath, etc.) lives in watchContext.
type watchState struct {
	mu              sync.Mutex
	reloadCounter   int
	previewSelector string
	previewIndex    int                       // current 0-based preview index
	previewCount    int                       // total number of #Preview blocks (0 = unknown)
	building        bool                      // true while rebuildAndRelaunch is running
	skeletonMap     map[string]string         // file path → skeleton hash
	trackedFiles    []string                  // target + 1-level dependency file paths
	depGraph        *analysis.DependencyGraph // transitive dependency graph (nil = fallback to rebuild)
}

// watchContext holds immutable configuration for the watch loop.
// These values are set once during initialization and never modified.
type watchContext struct {
	device        string // simulator device identifier for simctl
	deviceSetPath string // custom device set path for simctl --set
	loaderPath    string // path to the compiled loader binary
	serve         bool   // true when running in serve mode (IDE integration)
	ew            *protocol.EventWriter

	// ThunkCompiler encapsulates the parse → thunk generation → compile pipeline.
	compiler codegen.ThunkCompiler

	// Injected runners for testability.
	build     BuildRunner
	toolchain ToolchainRunner
	app       AppRunner
	copier    FileCopier
	sources   SourceLister
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

// IndexStorePath returns the path to the Xcode index store data directory.
func (d previewDirs) IndexStorePath() string {
	return filepath.Join(d.Build, "Index.noindex", "DataStore")
}

// maxSunPathLen is the maximum length of sockaddr_un.sun_path on macOS.
// connect() returns EINVAL if the path exceeds this limit.
const maxSunPathLen = 104

// newPreviewDirs creates a previewDirs based on a hash of the project/workspace
// path, with session-specific directories scoped by deviceUDID.
// Uses ~/Library/Caches/axe/ instead of /tmp so that dylibs are accessible
// from within the iOS Simulator via dlopen (separated runtimes cannot resolve
// host /tmp paths).
//
// The Unix domain socket is placed directly under Root (not under Session)
// because macOS limits sun_path to 104 bytes. The full Session path with a
// UUID device identifier easily exceeds that limit.
func newPreviewDirs(projectPath string, deviceUDID string) (previewDirs, error) {
	abs, _ := filepath.Abs(projectPath)
	h := sha256.Sum256([]byte(abs))
	short := fmt.Sprintf("%x", h[:8])

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	root := filepath.Join(cacheDir, "axe", "preview-"+short)
	session := filepath.Join(root, "devices", deviceUDID)

	// Hash the UDID to keep the socket path short while guaranteeing
	// uniqueness per device. 8 bytes (16 hex chars) gives 64-bit space,
	// more than enough for the handful of concurrent devices we support.
	uh := sha256.Sum256([]byte(deviceUDID))
	socketPath := filepath.Join(root, fmt.Sprintf("%x.sock", uh[:8]))

	if len(socketPath) >= maxSunPathLen {
		return previewDirs{}, fmt.Errorf(
			"socket path exceeds Unix domain socket limit (%d >= %d): %s. "+
				"Consider using a shorter cache directory path",
			len(socketPath), maxSunPathLen, socketPath)
	}

	return previewDirs{
		Root:    root,
		Build:   filepath.Join(root, "build"),
		Session: session,
		Thunk:   filepath.Join(session, "thunk"),
		Loader:  filepath.Join(session, "loader"),
		Staging: filepath.Join(session, "staging"),
		Socket:  socketPath,
	}, nil
}

// buildSkeletonMap computes skeleton hashes for the given files.
func buildSkeletonMap(files []string) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		if sk, err := analysis.Skeleton(f); err == nil {
			m[filepath.Clean(f)] = sk
		}
	}
	return m
}
