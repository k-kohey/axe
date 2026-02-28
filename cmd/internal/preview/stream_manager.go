package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
)

// DevicePoolInterface abstracts DevicePool for testability.
type DevicePoolInterface interface {
	Acquire(ctx context.Context, deviceType, runtime string) (string, error)
	Release(ctx context.Context, udid string) error
	ShutdownAll(ctx context.Context)
	CleanupOrphans(ctx context.Context) error
	GarbageCollect(ctx context.Context)
}

// companionProcess abstracts idb.Companion for testability.
// Both boot and idb companions satisfy this interface.
type companionProcess interface {
	Done() <-chan struct{}
	Err() error
	Stop() error
}

// stream represents a single preview stream's state.
type stream struct {
	id         string
	file       string
	deviceType string
	runtime    string
	deviceUDID string
	cancel     context.CancelFunc
	done       chan struct{} // closed when stream goroutine exits

	// degraded is true when the stream launched using main-only thunk fallback.
	// Hot-reload is not available in this mode.
	degraded bool

	// Per-stream command channels (buffered size 1).
	switchFileCh   chan string
	nextPreviewCh  chan struct{}
	forceRebuildCh chan struct{}
	inputCh        chan *pb.Input
	fileChangeCh   chan string // from shared watcher

	// Runtime state (set during stream initialization in the launcher).
	dirs          previewDirs
	bootCompanion companionProcess
	idbCompanion  companionProcess
	idbClient     idb.IDBClient
	hid           *protocol.HIDHandler
	ws            *watchState
	loaderPath    string

	// Prevents duplicate StreamStopped events.
	stoppedOnce sync.Once
}

// sendStopped sends a StreamStopped event exactly once per stream.
// Safe to call multiple times (from launcher error and from RemoveStream).
func (s *stream) sendStopped(ew *protocol.EventWriter, reason, message, diagnostic string) {
	s.stoppedOnce.Do(func() {
		if err := ew.Send(&pb.Event{
			StreamId: s.id,
			Payload: &pb.Event_StreamStopped{StreamStopped: &pb.StreamStopped{
				Reason:     reason,
				Message:    message,
				Diagnostic: diagnostic,
			}},
		}); err != nil {
			slog.Warn("Failed to send StreamStopped", "streamId", s.id, "err", err)
		}
	})
}

// StreamManager manages multiple preview streams.
// It routes commands to the appropriate stream and coordinates shared resources.
type StreamManager struct {
	mu      sync.Mutex
	streams map[string]*stream
	pool    DevicePoolInterface
	ew      *protocol.EventWriter

	// strict mode disables degraded fallback.
	strict bool

	// Shared project configuration.
	pc            ProjectConfig
	deviceSetPath string

	// Shared build settings (lazy init, protected by bsMu).
	bsMu        sync.RWMutex
	bs          *build.Settings
	bsExtracted bool // true after extractCompilerPaths has been called

	// Shared Index Store cache across all streams.
	// When any stream rebuilds, it updates this cache so other streams
	// see fresh type/reference data without a stale in-memory snapshot.
	indexCache *sharedIndexCache

	// Shared file watcher (set by RunServe before starting command loop).
	watcher *watch.SharedWatcher

	// Injected runners for testability.
	build     BuildRunner
	toolchain ToolchainRunner
	app       AppRunner
	copier    FileCopier
	sources   SourceLister

	// StreamLauncher is called per-stream in a goroutine.
	// It should block until the stream ends (context cancelled or error).
	// The default implementation performs the full preview lifecycle
	// (boot, build, install, launch, watch). Tests override this with a fake.
	StreamLauncher func(ctx context.Context, sm *StreamManager, s *stream)
}

// NewStreamManager creates a StreamManager with the default stream launcher.
func NewStreamManager(pool DevicePoolInterface, ew *protocol.EventWriter, pc ProjectConfig, deviceSetPath string,
	br BuildRunner, tc ToolchainRunner, ar AppRunner, fc FileCopier, sl SourceLister, strict bool) *StreamManager {
	sm := &StreamManager{
		streams:       make(map[string]*stream),
		pool:          pool,
		ew:            ew,
		strict:        strict,
		pc:            pc,
		deviceSetPath: deviceSetPath,
		indexCache:    newSharedIndexCache(nil),
		build:         br,
		toolchain:     tc,
		app:           ar,
		copier:        fc,
		sources:       sl,
	}
	sm.StreamLauncher = sm.defaultStreamLauncher
	return sm
}

// HandleCommand dispatches a Command to the appropriate stream.
func (sm *StreamManager) HandleCommand(ctx context.Context, cmd *pb.Command) {
	switch {
	case cmd.GetAddStream() != nil:
		sm.handleAddStream(ctx, cmd.GetStreamId(), cmd.GetAddStream())
	case cmd.GetRemoveStream() != nil:
		sm.handleRemoveStream(cmd.GetStreamId())
	case cmd.GetSwitchFile() != nil:
		sm.handleSwitchFile(cmd.GetStreamId(), cmd.GetSwitchFile())
	case cmd.GetNextPreview() != nil:
		sm.handleNextPreview(cmd.GetStreamId())
	case cmd.GetForceRebuild() != nil:
		sm.handleForceRebuild(cmd.GetStreamId())
	case cmd.GetInput() != nil:
		sm.handleInput(cmd.GetStreamId(), cmd.GetInput())
	default:
		slog.Warn("Command has no payload", "streamId", cmd.GetStreamId())
	}
}

func (sm *StreamManager) handleAddStream(ctx context.Context, streamID string, add *pb.AddStream) {
	sm.mu.Lock()
	if _, exists := sm.streams[streamID]; exists {
		sm.mu.Unlock()
		slog.Warn("Duplicate streamId in AddStream, ignoring", "streamId", streamID)
		return
	}

	streamCtx, cancel := context.WithCancel(ctx)
	s := &stream{
		id:             streamID,
		file:           add.GetFile(),
		deviceType:     add.GetDeviceType(),
		runtime:        add.GetRuntime(),
		cancel:         cancel,
		done:           make(chan struct{}),
		switchFileCh:   make(chan string, 1),
		nextPreviewCh:  make(chan struct{}, 1),
		forceRebuildCh: make(chan struct{}, 1),
		inputCh:        make(chan *pb.Input, 1),
		fileChangeCh:   make(chan string, 1),
	}
	sm.streams[streamID] = s
	sm.mu.Unlock()

	go sm.runStream(streamCtx, s)
}

func (sm *StreamManager) handleRemoveStream(streamID string) {
	sm.mu.Lock()
	s, exists := sm.streams[streamID]
	if !exists {
		sm.mu.Unlock()
		slog.Warn("RemoveStream for unknown streamId", "streamId", streamID)
		return
	}
	delete(sm.streams, streamID)
	sm.mu.Unlock()

	// Cancel the stream goroutine and wait for cleanup to finish.
	// Resource cleanup (device release, companion stop, etc.) is handled by
	// runStream's defer chain, not here.
	// A 30-second timeout prevents a hung stream from blocking the command loop.
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		slog.Error("Stream cleanup timed out, proceeding without waiting", "streamId", streamID)
	}

	s.sendStopped(sm.ew, "removed", "", "")
}

func (sm *StreamManager) handleSwitchFile(streamID string, sf *pb.SwitchFile) {
	sm.mu.Lock()
	s, ok := sm.streams[streamID]
	sm.mu.Unlock()
	if !ok {
		slog.Warn("SwitchFile for unknown streamId", "streamId", streamID)
		return
	}
	select {
	case s.switchFileCh <- sf.GetFile():
	default:
		slog.Warn("SwitchFile command dropped (stream busy)", "streamId", streamID)
	}
}

func (sm *StreamManager) handleNextPreview(streamID string) {
	sm.mu.Lock()
	s, ok := sm.streams[streamID]
	sm.mu.Unlock()
	if !ok {
		slog.Warn("NextPreview for unknown streamId", "streamId", streamID)
		return
	}
	select {
	case s.nextPreviewCh <- struct{}{}:
	default:
		slog.Warn("NextPreview command dropped (stream busy)", "streamId", streamID)
	}
}

func (sm *StreamManager) handleForceRebuild(streamID string) {
	sm.mu.Lock()
	s, ok := sm.streams[streamID]
	sm.mu.Unlock()
	if !ok {
		slog.Warn("ForceRebuild for unknown streamId", "streamId", streamID)
		return
	}
	select {
	case s.forceRebuildCh <- struct{}{}:
	default:
		slog.Warn("ForceRebuild command dropped (stream busy)", "streamId", streamID)
	}
}

func (sm *StreamManager) handleInput(streamID string, input *pb.Input) {
	sm.mu.Lock()
	s, ok := sm.streams[streamID]
	sm.mu.Unlock()
	if !ok {
		slog.Warn("Input for unknown streamId", "streamId", streamID)
		return
	}
	select {
	case s.inputCh <- input:
	default:
		slog.Debug("Input command dropped (stream busy)", "streamId", streamID)
	}
}

// runStream executes the stream lifecycle in a goroutine with panic recovery
// and coordinated cleanup.
func (sm *StreamManager) runStream(ctx context.Context, s *stream) {
	defer close(s.done)
	defer s.cancel() // Ensure launcher goroutines (e.g. RelayVideoStreamEvents) stop on normal return.
	defer sm.cleanupStreamResources(s)
	defer func() {
		// Self-remove from map. If handleRemoveStream already deleted us,
		// this is a no-op.
		sm.mu.Lock()
		delete(sm.streams, s.id)
		sm.mu.Unlock()
	}()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Stream panicked", "streamId", s.id, "panic", r)
			s.sendStopped(sm.ew, "internal_error", fmt.Sprintf("%v", r), "")
		}
	}()

	sm.StreamLauncher(ctx, sm, s)
}

// cleanupStreamResources releases all per-stream resources. Each nil check
// makes this function idempotent and safe when called from partial initialization.
func (sm *StreamManager) cleanupStreamResources(s *stream) {
	// Unregister from shared watcher.
	if sm.watcher != nil {
		sm.watcher.RemoveListener(s.id)
	}

	// Terminate the app on the device.
	if s.deviceUDID != "" {
		sm.bsMu.RLock()
		bs := sm.bs
		sm.bsMu.RUnlock()
		if bs != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			terminateApp(cleanupCtx, bs, s.deviceUDID, sm.deviceSetPath, sm.app)
			cleanupCancel()
		}
	}

	// Remove loader socket.
	if s.dirs.Socket != "" {
		if err := os.Remove(s.dirs.Socket); err != nil && !os.IsNotExist(err) {
			slog.Debug("Failed to remove socket", "streamId", s.id, "path", s.dirs.Socket, "err", err)
		}
	}

	// Close idb gRPC client.
	if s.idbClient != nil {
		if err := s.idbClient.Close(); err != nil {
			slog.Debug("Failed to close idb client", "streamId", s.id, "err", err)
		}
	}

	// Stop idb companion (video/HID).
	if s.idbCompanion != nil {
		if err := s.idbCompanion.Stop(); err != nil {
			slog.Debug("Failed to stop idb companion", "streamId", s.id, "err", err)
		}
	}

	// Stop boot companion (simulator).
	if s.bootCompanion != nil {
		if err := s.bootCompanion.Stop(); err != nil {
			slog.Debug("Failed to stop boot companion", "streamId", s.id, "err", err)
		}
	}

	// Release the device back to pool.
	if s.deviceUDID != "" {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := sm.pool.Release(releaseCtx, s.deviceUDID); err != nil {
			slog.Warn("Failed to release device", "streamId", s.id, "udid", s.deviceUDID, "err", err)
		}
		releaseCancel()
	}
}

// ensureBuildSettings fetches build settings once (lazy init) and caches the
// result. Thread-safe via double-checked locking on bsMu.
func (sm *StreamManager) ensureBuildSettings(ctx context.Context, dirs previewDirs) (*build.Settings, error) {
	sm.bsMu.RLock()
	if sm.bs != nil {
		bs := sm.bs
		sm.bsMu.RUnlock()
		return bs, nil
	}
	sm.bsMu.RUnlock()

	sm.bsMu.Lock()
	defer sm.bsMu.Unlock()
	if sm.bs != nil {
		return sm.bs, nil
	}

	bs, err := fetchBuildSettings(ctx, sm.pc, dirs, sm.build)
	if err != nil {
		return nil, err
	}
	sm.bs = bs
	return bs, nil
}

// ensureCompilerPathsExtracted calls extractCompilerPaths exactly once.
// Must be called after at least one successful buildProject invocation.
func (sm *StreamManager) ensureCompilerPathsExtracted(ctx context.Context, bs *build.Settings, dirs previewDirs) {
	sm.bsMu.Lock()
	defer sm.bsMu.Unlock()
	if sm.bsExtracted {
		return
	}
	extractCompilerPaths(ctx, bs, dirs)
	sm.bsExtracted = true
}

// defaultStreamLauncher is the production stream lifecycle.
// Steps: Boot → Build → Install → Launch → Video relay → event loop.
func (sm *StreamManager) defaultStreamLauncher(ctx context.Context, _ *StreamManager, s *stream) {
	sendStatus := func(phase string) {
		if err := sm.ew.Send(&pb.Event{StreamId: s.id, Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: phase}}}); err != nil {
			slog.Warn("Failed to send StreamStatus", "streamId", s.id, "phase", phase, "err", err)
		}
	}

	// 1. Acquire a device from pool.
	sendStatus("booting")

	udid, err := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
	if err != nil {
		s.sendStopped(sm.ew, "resource_error", fmt.Sprintf("acquiring device: %v", err), "")
		return
	}
	s.deviceUDID = udid

	// 2. Create per-stream preview directories.
	dirs, err := newPreviewDirs(sm.pc.PrimaryPath(), udid)
	if err != nil {
		s.sendStopped(sm.ew, "resource_error", err.Error(), "")
		return
	}
	s.dirs = dirs

	launcherCtx, launcherCancel := context.WithCancel(ctx)
	defer launcherCancel()

	type bootResult struct {
		companion companionProcess
		err       error
	}
	type compileResult struct {
		bs           *build.Settings
		depGraph     *analysis.DependencyGraph
		trackedFiles []string
		dylibPath    string
		degraded     bool
		buildFailed  bool
		buildDiag    string
		err          error
	}

	bootResCh := make(chan bootResult, 1)
	compileResCh := make(chan compileResult, 1)

	// 3. Boot simulator in parallel with build/compile preparation.
	go func() {
		var res bootResult
		res.companion, res.err = bootWithRetry(launcherCtx, udid, sm.deviceSetPath, true)
		if res.err != nil {
			res.err = fmt.Errorf("booting simulator: %w", res.err)
		}
		bootResCh <- res
	}()

	// 4. Build + compile path (runs in parallel with boot).
	go func() {
		res := compileResult{}
		bs, err := sm.ensureBuildSettings(launcherCtx, s.dirs)
		if err != nil {
			res.err = err
			compileResCh <- res
			return
		}
		res.bs = bs

		// Optimistic path: reuse existing app build artifacts if available.
		// If thunk compilation fails, fall back to full build and retry once.
		builtThisLaunch := false
		if !hasPreviousBuild(bs, s.dirs) {
			sendStatus("building")
			if err := buildProject(launcherCtx, sm.pc, s.dirs, sm.build); err != nil {
				res.buildFailed = true
				res.buildDiag = err.Error()
				res.err = fmt.Errorf("build failed")
				compileResCh <- res
				return
			}
			builtThisLaunch = true
			sm.ensureCompilerPathsExtracted(launcherCtx, bs, s.dirs)
		} else {
			sendStatus("reusing_build")
			slog.Info("Reusing previous app build artifacts for stream launch", "streamId", s.id, "buildDir", s.dirs.Build)
		}

		projectRoot := filepath.Dir(sm.pc.PrimaryPath())
		compileAttempt := func() (*analysis.DependencyGraph, []string, string, error) {
			sendStatus("compiling_thunk")
			// Load (or refresh) the shared Index Store cache. The first stream to
			// build populates it; subsequent streams reuse the same instance.
			cache, cacheErr := analysis.LoadIndexStore(launcherCtx, s.dirs.IndexStorePath(), projectRoot)
			if cacheErr != nil && launcherCtx.Err() == nil {
				slog.Warn("Index store cache unavailable for stream",
					"streamId", s.id, "err", cacheErr)
			}
			sm.indexCache.Set(cache)

			depGraph, depFiles, err := analysis.ResolveTransitiveDependencies(launcherCtx, s.file, sm.indexCache.Get())
			if err != nil && launcherCtx.Err() == nil {
				slog.Warn("Failed to resolve dependencies, proceeding with target only",
					"streamId", s.id, "err", err)
			}
			trackedFiles := append([]string{s.file}, depFiles...)

			files, trackedFiles, err := parseAndFilterTrackedFiles(s.file, trackedFiles, sm.indexCache.Get())
			if err != nil {
				return nil, nil, "", err
			}

			thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, s.dirs.Thunk, "0", s.file, 0)
			if err != nil {
				return nil, nil, "", err
			}

			dylibPath, err := codegen.CompileThunk(launcherCtx, thunkPaths, compileConfigFromSettings(bs), s.dirs.Thunk, s.dirs.Build, 0, s.file, sm.toolchain)
			if err != nil {
				return nil, nil, "", err
			}
			return depGraph, trackedFiles, dylibPath, nil
		}

		strategy := NewCompileStrategy(true, true, false, sm.strict)
		compilers := map[CompileMode]CompileFunc{
			CompileModeFull: func(_ context.Context) (string, error) {
				dg, tf, dylibPath, err := compileAttempt()
				if err != nil && !builtThisLaunch {
					slog.Info("Optimistic launch failed; rebuilding and retrying once", "streamId", s.id, "err", err)
					sendStatus("building")
					if buildErr := buildProject(launcherCtx, sm.pc, s.dirs, sm.build); buildErr != nil {
						res.buildFailed = true
						res.buildDiag = buildErr.Error()
						return "", fmt.Errorf("build failed")
					}
					sm.ensureCompilerPathsExtracted(launcherCtx, bs, s.dirs)
					dg, tf, dylibPath, err = compileAttempt()
				}
				if err != nil {
					return "", err
				}
				res.depGraph = dg
				res.trackedFiles = tf
				return dylibPath, nil
			},
			CompileModeMainOnly: func(_ context.Context) (string, error) {
				sm.ensureCompilerPathsExtracted(launcherCtx, bs, s.dirs)
				return compileMainOnlyPipeline(launcherCtx, s.file, bs, s.dirs, "0", sm.toolchain)
			},
		}

		compileStratResult, stratErr := ExecuteCompileStrategy(launcherCtx, strategy, compilers)
		if stratErr != nil {
			res.err = stratErr
		} else {
			res.dylibPath = compileStratResult.DylibPath
			res.degraded = compileStratResult.Degraded
		}
		compileResCh <- res
	}()

	var (
		bootRes    bootResult
		compileRes compileResult
		bootDone   bool
		compDone   bool
	)
	for !bootDone || !compDone {
		select {
		case br := <-bootResCh:
			bootRes = br
			bootDone = true
			if br.err != nil {
				launcherCancel()
			}
		case cr := <-compileResCh:
			compileRes = cr
			compDone = true
			if cr.err != nil {
				launcherCancel()
			}
		}
	}
	if bootRes.err != nil || compileRes.err != nil {
		launcherCancel()
		if bootRes.companion != nil {
			if stopErr := bootRes.companion.Stop(); stopErr != nil {
				slog.Debug("Failed to stop boot companion after parallel launch failure", "streamId", s.id, "err", stopErr)
			}
		}
		if bootRes.err != nil {
			s.sendStopped(sm.ew, "boot_error", bootRes.err.Error(), "")
			return
		}
		if compileRes.buildFailed {
			s.sendStopped(sm.ew, "build_error", "Build failed", compileRes.buildDiag)
			return
		}
		s.sendStopped(sm.ew, "build_error", compileRes.err.Error(), "")
		return
	}

	s.bootCompanion = bootRes.companion
	s.degraded = compileRes.degraded
	bs := compileRes.bs
	depGraph := compileRes.depGraph
	trackedFiles := compileRes.trackedFiles
	dylibPath := compileRes.dylibPath

	// Verify the simulator didn't crash immediately after boot.
	select {
	case <-s.bootCompanion.Done():
		s.sendStopped(sm.ew, "boot_error",
			fmt.Sprintf("simulator crashed immediately after boot: %v", s.bootCompanion.Err()), "")
		return
	default:
	}

	// 8. Install app and compile loader.
	sendStatus("installing")
	terminateApp(ctx, bs, udid, sm.deviceSetPath, sm.app)

	if _, err := installApp(ctx, bs, s.dirs, udid, sm.deviceSetPath, sm.app, sm.copier); err != nil {
		s.sendStopped(sm.ew, "install_error", err.Error(), "")
		return
	}

	loaderPath, err := codegen.CompileLoader(ctx, s.dirs.Loader, bs.DeploymentTarget, sm.toolchain)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}
	s.loaderPath = loaderPath

	// 9. Launch app with hot-reload.
	sendStatus("running")
	if err := launchWithHotReload(ctx, bs, loaderPath, dylibPath, s.dirs.Socket, udid, sm.deviceSetPath, sm.app); err != nil {
		s.sendStopped(sm.ew, "runtime_error", err.Error(), "")
		return
	}

	// 10. Count previews and send StreamStarted.
	previewCount := 0
	if blocks, parseErr := analysis.PreviewBlocks(s.file); parseErr == nil {
		previewCount = len(blocks)
	}
	if err := sm.ew.Send(&pb.Event{
		StreamId: s.id,
		Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: int32(previewCount)}},
	}); err != nil {
		slog.Warn("Failed to send StreamStarted", "streamId", s.id, "err", err)
	}

	// 13. Start idb_companion for video relay and HID.
	companion, err := idb.Start(udid, sm.deviceSetPath)
	if err != nil {
		s.sendStopped(sm.ew, "runtime_error", fmt.Sprintf("starting idb_companion: %v", err), "")
		return
	}
	s.idbCompanion = companion

	idbClient, err := idb.NewClient(companion.Address())
	if err != nil {
		s.sendStopped(sm.ew, "runtime_error", fmt.Sprintf("connecting to idb_companion: %v", err), "")
		return
	}
	s.idbClient = idbClient

	idbErrCh := make(chan error, 1)
	voc := &protocol.VideoOutputConfig{
		EW:       sm.ew,
		StreamID: s.id,
		Device:   udid,
		File:     s.file,
	}
	go protocol.RelayVideoStreamEvents(ctx, idbClient, idbErrCh, voc)

	// 14. Create HID handler.
	if w, h, err := idbClient.ScreenSize(ctx); err == nil {
		s.hid = protocol.NewHIDHandler(idbClient, w, h)
	}

	// 15. Degraded mode: skip watcher, run simplified event loop.
	if s.degraded {
		sendStatus("degraded")
		slog.Warn("Stream running in degraded mode: hot-reload not available", "streamId", s.id)
		if err := runDegradedStreamLoop(ctx, s, sm, idbErrCh); err != nil {
			slog.Info("Degraded stream loop exited", "streamId", s.id, "err", err)
		}
		return
	}

	// 16. Initialize watch state (full mode only).
	s.ws = &watchState{
		reloadCounter:   1, // 0 was used for the initial launch
		previewSelector: "0",
		previewIndex:    0,
		previewCount:    previewCount,
		skeletonMap:     buildSkeletonMap(trackedFiles),
		trackedFiles:    trackedFiles,
		depGraph:        depGraph,
		indexCache:      sm.indexCache, // shared across all streams
	}

	// 17. Register with shared watcher for file change notifications.
	if sm.watcher != nil {
		sm.watcher.AddListener(s.id, s.fileChangeCh)
	}

	// 18. Enter the per-stream event loop (blocks until context cancelled or crash).
	if err := runStreamLoop(ctx, s, sm, bs, idbErrCh); err != nil {
		slog.Info("Stream loop exited", "streamId", s.id, "err", err)
	}
}

// StopAll stops all active streams and shuts down the device pool.
func (sm *StreamManager) StopAll() {
	sm.mu.Lock()
	streams := make([]*stream, 0, len(sm.streams))
	for _, s := range sm.streams {
		streams = append(streams, s)
	}
	sm.streams = make(map[string]*stream)
	sm.mu.Unlock()

	// Cancel all stream goroutines.
	for _, s := range streams {
		s.cancel()
	}
	// Wait for all goroutines to finish (cleanup happens in runStream defer).
	for _, s := range streams {
		select {
		case <-s.done:
		case <-time.After(30 * time.Second):
			slog.Error("Stream cleanup timed out during StopAll", "streamId", s.id)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	sm.pool.ShutdownAll(shutdownCtx)
	shutdownCancel()
}
