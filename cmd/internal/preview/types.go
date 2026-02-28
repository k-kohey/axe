package preview

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	"github.com/k-kohey/axe/internal/preview/protocol"
)

// ProjectConfig is an alias for build.ProjectConfig, kept here for backward
// compatibility with cmd/axe and other callers that import the preview package.
type ProjectConfig = build.ProjectConfig

// NewProjectConfig is forwarded from build.NewProjectConfig.
func NewProjectConfig(project, workspace, scheme, configuration string) (ProjectConfig, error) {
	return build.NewProjectConfig(project, workspace, scheme, configuration)
}

// RunOptions holds all parameters for a single-stream preview Run invocation.
type RunOptions struct {
	SourceFile      string
	PC              ProjectConfig
	Watch           bool
	PreviewSelector string
	Serve           bool
	PreferredDevice string
	ReuseBuild      bool
	FullThunk       bool
	Strict          bool
	NoHeadless      bool

	// OnReady is called after the preview app has launched and is confirmed ready.
	// Receives the simulator device UDID and device set path.
	// Only invoked in oneshot mode (not watch, not serve).
	// If nil, oneshot returns immediately after verifying readiness.
	OnReady func(ctx context.Context, device, deviceSetPath string) error
}

// compileConfigFromSettings converts build.Settings to codegen.CompileConfig.
func compileConfigFromSettings(s *build.Settings) codegen.CompileConfig {
	return codegen.CompileConfig{
		ModuleName:          s.ModuleName,
		BuiltProductsDir:    s.BuiltProductsDir,
		DeploymentTarget:    s.DeploymentTarget,
		SwiftVersion:        s.SwiftVersion,
		ExtraIncludePaths:   s.ExtraIncludePaths,
		ExtraFrameworkPaths: s.ExtraFrameworkPaths,
		ExtraModuleMapFiles: s.ExtraModuleMapFiles,
	}
}

// sharedIndexCache is a thread-safe wrapper around IndexStoreCache.
// In multi-stream mode a single instance is shared across all streams via
// StreamManager, so that when any stream rebuilds (refreshing the on-disk
// Index Store) the new cache is visible to every other stream.
// In single-stream mode a dedicated instance is created locally.
type sharedIndexCache struct {
	mu    sync.RWMutex
	cache *analysis.IndexStoreCache
}

func newSharedIndexCache(c *analysis.IndexStoreCache) *sharedIndexCache {
	return &sharedIndexCache{cache: c}
}

func (s *sharedIndexCache) Get() *analysis.IndexStoreCache {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache
}

func (s *sharedIndexCache) Set(c *analysis.IndexStoreCache) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = c
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
	indexCache      *sharedIndexCache         // shared Index Store cache (nil = fallback to parser)
}

// watchContext holds immutable configuration for the watch loop.
// These values are set once during initialization and never modified.
type watchContext struct {
	device        string // simulator device identifier for simctl
	deviceSetPath string // custom device set path for simctl --set
	loaderPath    string // path to the compiled loader binary
	streamID      string // protocol stream id in serve mode
	serve         bool   // true when running in serve mode (IDE integration)
	ew            *protocol.EventWriter

	// Injected runners for testability.
	build     build.Runner
	toolchain ToolchainRunner
	app       AppRunner
	copier    FileCopier
	sources   SourceLister
}

// previewDirs manages temp directories scoped per project path.
// Build artifacts (via embedded ProjectDirs) are shared across sessions,
// while session-specific resources live under devices/<udid>/.
type previewDirs struct {
	build.ProjectDirs
	Session string // Root/devices/<device-udid>
	Thunk   string // Session/thunk
	Loader  string // Session/loader
	Staging string // Session/staging
	Socket  string // Session/loader.sock
}

// maxSunPathLen is the maximum length of sockaddr_un.sun_path on macOS.
// connect() returns EINVAL if the path exceeds this limit.
const maxSunPathLen = 104

// newPreviewDirs creates a previewDirs based on a hash of the project/workspace
// path, with session-specific directories scoped by deviceUDID.
//
// The Unix domain socket is placed directly under Root (not under Session)
// because macOS limits sun_path to 104 bytes. The full Session path with a
// UUID device identifier easily exceeds that limit.
func newPreviewDirs(projectPath string, deviceUDID string) (previewDirs, error) {
	pd, err := build.NewProjectDirs(projectPath)
	if err != nil {
		return previewDirs{}, err
	}

	session := filepath.Join(pd.Root, "devices", deviceUDID)

	// Hash the UDID to keep the socket path short while guaranteeing
	// uniqueness per device. 8 bytes (16 hex chars) gives 64-bit space,
	// more than enough for the handful of concurrent devices we support.
	uh := sha256.Sum256([]byte(deviceUDID))
	socketPath := filepath.Join(pd.Root, fmt.Sprintf("%x.sock", uh[:8]))

	if len(socketPath) >= maxSunPathLen {
		return previewDirs{}, fmt.Errorf(
			"socket path exceeds Unix domain socket limit (%d >= %d): %s. "+
				"Consider using a shorter cache directory path",
			len(socketPath), maxSunPathLen, socketPath)
	}

	return previewDirs{
		ProjectDirs: pd,
		Session:     session,
		Thunk:       filepath.Join(session, "thunk"),
		Loader:      filepath.Join(session, "loader"),
		Staging:     filepath.Join(session, "staging"),
		Socket:      socketPath,
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
