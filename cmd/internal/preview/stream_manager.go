package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/k-kohey/axe/internal/idb"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
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

	// Per-stream command channels (buffered size 1).
	switchFileCh  chan string
	nextPreviewCh chan struct{}
	inputCh       chan *pb.Input
	fileChangeCh  chan string // from shared watcher

	// Runtime state (set during stream initialization in the launcher).
	dirs          previewDirs
	bootCompanion companionProcess
	idbCompanion  companionProcess
	idbClient     idb.IDBClient
	hid           *hidHandler
	ws            *watchState
	loaderPath    string

	// Prevents duplicate StreamStopped events.
	stoppedOnce sync.Once
}

// sendStopped sends a StreamStopped event exactly once per stream.
// Safe to call multiple times (from launcher error and from RemoveStream).
func (s *stream) sendStopped(ew *EventWriter, reason, message, diagnostic string) {
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
	ew      *EventWriter

	// Shared project configuration.
	pc            ProjectConfig
	deviceSetPath string

	// Shared build settings (lazy init, protected by bsMu).
	bsMu        sync.RWMutex
	bs          *buildSettings
	bsExtracted bool // true after extractCompilerPaths has been called

	// Shared file watcher (set by RunServe before starting command loop).
	watcher *sharedWatcher

	// StreamLauncher is called per-stream in a goroutine.
	// It should block until the stream ends (context cancelled or error).
	// The default implementation performs the full preview lifecycle
	// (boot, build, install, launch, watch). Tests override this with a fake.
	StreamLauncher func(ctx context.Context, sm *StreamManager, s *stream)
}

// NewStreamManager creates a StreamManager with the default stream launcher.
func NewStreamManager(pool DevicePoolInterface, ew *EventWriter, pc ProjectConfig, deviceSetPath string) *StreamManager {
	sm := &StreamManager{
		streams:       make(map[string]*stream),
		pool:          pool,
		ew:            ew,
		pc:            pc,
		deviceSetPath: deviceSetPath,
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
		id:            streamID,
		file:          add.GetFile(),
		deviceType:    add.GetDeviceType(),
		runtime:       add.GetRuntime(),
		cancel:        cancel,
		done:          make(chan struct{}),
		switchFileCh:  make(chan string, 1),
		nextPreviewCh: make(chan struct{}, 1),
		inputCh:       make(chan *pb.Input, 1),
		fileChangeCh:  make(chan string, 1),
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
	s.cancel()
	<-s.done

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
	}
}

// runStream executes the stream lifecycle in a goroutine with panic recovery
// and coordinated cleanup.
func (sm *StreamManager) runStream(ctx context.Context, s *stream) {
	defer close(s.done)
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
		sm.watcher.removeListener(s.id)
	}

	// Terminate the app on the device.
	if s.deviceUDID != "" {
		sm.bsMu.RLock()
		bs := sm.bs
		sm.bsMu.RUnlock()
		if bs != nil {
			terminateApp(bs, s.deviceUDID, sm.deviceSetPath)
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
		if err := sm.pool.Release(context.Background(), s.deviceUDID); err != nil {
			slog.Warn("Failed to release device", "streamId", s.id, "udid", s.deviceUDID, "err", err)
		}
	}
}

// ensureBuildSettings fetches build settings once (lazy init) and caches the
// result. Thread-safe via double-checked locking on bsMu.
func (sm *StreamManager) ensureBuildSettings(ctx context.Context, dirs previewDirs) (*buildSettings, error) {
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

	bs, err := fetchBuildSettings(sm.pc, dirs)
	if err != nil {
		return nil, err
	}
	sm.bs = bs
	return bs, nil
}

// ensureCompilerPathsExtracted calls extractCompilerPaths exactly once.
// Must be called after at least one successful buildProject invocation.
func (sm *StreamManager) ensureCompilerPathsExtracted(ctx context.Context, bs *buildSettings, dirs previewDirs) {
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

	// 1. Acquire a device from the pool.
	sendStatus("booting")
	udid, err := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
	if err != nil {
		s.sendStopped(sm.ew, "resource_error", fmt.Sprintf("acquiring device: %v", err), "")
		return
	}
	s.deviceUDID = udid

	// 2. Create per-stream preview directories.
	s.dirs = newPreviewDirs(sm.pc.primaryPath(), udid)

	// 3. Boot simulator headlessly via idb_companion.
	bootCompanion, err := idb.BootHeadless(udid, sm.deviceSetPath)
	if err != nil {
		s.sendStopped(sm.ew, "boot_error", fmt.Sprintf("booting simulator: %v", err), "")
		return
	}
	s.bootCompanion = bootCompanion

	// 4. Verify the simulator didn't crash immediately after boot.
	select {
	case <-bootCompanion.Done():
		s.sendStopped(sm.ew, "boot_error",
			fmt.Sprintf("simulator crashed immediately after boot: %v", bootCompanion.Err()), "")
		return
	default:
	}

	// 5. Fetch build settings (lazy, shared across streams).
	bs, err := sm.ensureBuildSettings(ctx, s.dirs)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}

	// 6. Build the project.
	sendStatus("building")
	if err := buildProject(ctx, sm.pc, s.dirs); err != nil {
		s.sendStopped(sm.ew, "build_error", "Build failed", err.Error())
		return
	}

	// 7. Extract compiler paths from build output (once).
	sm.ensureCompilerPathsExtracted(ctx, bs, s.dirs)

	// 8. Resolve dependencies and parse source.
	projectRoot := filepath.Dir(sm.pc.primaryPath())
	depFiles, err := resolveDependencies(s.file, projectRoot)
	if err != nil {
		slog.Warn("Failed to resolve dependencies, proceeding with target only",
			"streamId", s.id, "err", err)
	}
	trackedFiles := append([]string{s.file}, depFiles...)

	files, trackedFiles, err := parseAndFilterTrackedFiles(s.file, trackedFiles)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}

	// 9. Generate thunk and compile.
	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, s.dirs, "0", s.file)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}

	dylibPath, err := compileThunk(ctx, thunkPath, bs, s.dirs, 0, s.file)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}

	// 10. Install app and compile loader.
	sendStatus("installing")
	terminateApp(bs, udid, sm.deviceSetPath)

	if _, err := installApp(ctx, bs, s.dirs, udid, sm.deviceSetPath); err != nil {
		s.sendStopped(sm.ew, "install_error", err.Error(), "")
		return
	}

	loaderPath, err := compileLoader(s.dirs, bs.DeploymentTarget)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}
	s.loaderPath = loaderPath

	// 11. Launch app with hot-reload.
	sendStatus("running")
	if err := launchWithHotReload(bs, loaderPath, dylibPath, s.dirs.Socket, udid, sm.deviceSetPath); err != nil {
		s.sendStopped(sm.ew, "runtime_error", err.Error(), "")
		return
	}

	// 12. Count previews and send StreamStarted.
	previewCount := 0
	if blocks, parseErr := parsePreviewBlocks(s.file); parseErr == nil {
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
	voc := &videoOutputConfig{
		ew:       sm.ew,
		streamID: s.id,
		device:   udid,
		file:     s.file,
	}
	go relayVideoStreamEvents(ctx, idbClient, idbErrCh, voc)

	// 14. Create HID handler.
	if w, h, err := idbClient.ScreenSize(ctx); err == nil {
		s.hid = newHIDHandler(idbClient, w, h)
	}

	// 15. Initialize watch state.
	skeletonMap := make(map[string]string, len(trackedFiles))
	for _, tf := range trackedFiles {
		if sk, err := computeSkeleton(tf); err == nil {
			skeletonMap[filepath.Clean(tf)] = sk
		}
	}
	s.ws = &watchState{
		reloadCounter:   1, // 0 was used for the initial launch
		previewSelector: "0",
		previewIndex:    0,
		previewCount:    previewCount,
		skeletonMap:     skeletonMap,
		trackedFiles:    trackedFiles,
	}

	// 16. Register with shared watcher for file change notifications.
	if sm.watcher != nil {
		sm.watcher.addListener(s.id, s.fileChangeCh)
	}

	// 17. Enter the per-stream event loop (blocks until context cancelled or crash).
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
		<-s.done
	}

	sm.pool.ShutdownAll(context.Background())
}
