package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/preview/build"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
	"github.com/k-kohey/axe/internal/simruntime"
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
	id               string
	file             string
	deviceType       string
	runtime          string
	deviceUDID       string
	runtimeSessionID string
	cancel           context.CancelFunc
	done             chan struct{} // closed when stream goroutine exits

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
	dirs       previewDirs
	hid        inputHandler
	appRunner  AppRunner
	ws         *watchState
	loaderPath string

	// Prevents duplicate StreamStopped events.
	stoppedOnce sync.Once

	// Prevents duplicate resource cleanup when multiple callers race to cleanup.
	cleanupOnce sync.Once
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
	runtime simruntime.Manager
	ew      *protocol.EventWriter

	// strict mode disables degraded fallback.
	strict bool

	// Shared project configuration.
	pc            ProjectConfig
	deviceSetPath string

	// Preparer caches the build pipeline result (FetchSettings + Build +
	// ExtractCompilerPaths) so only the first stream pays the cost.
	preparer *build.Preparer

	// Shared Index Store cache across all streams.
	// When any stream rebuilds, it updates this cache so other streams
	// see fresh type/reference data without a stale in-memory snapshot.
	indexCache *sharedIndexCache

	// Shared file watcher (set by RunServe before starting command loop).
	watcher *watch.SharedWatcher

	// Incremental thunk configuration.
	maxThunkFiles int // max tracked files for incremental thunk
	preThunkDepth int // initial thunk generation depth

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
	preparer *build.Preparer, br BuildRunner, tc ToolchainRunner, ar AppRunner, fc FileCopier, sl SourceLister,
	strict bool, maxThunkFiles, preThunkDepth int) *StreamManager {
	return newStreamManager(pool, nil, ew, pc, deviceSetPath, preparer, br, tc, ar, fc, sl, strict, maxThunkFiles, preThunkDepth)
}

// NewRuntimeStreamManager creates a StreamManager backed by simruntime.Manager.
func NewRuntimeStreamManager(runtime simruntime.Manager, ew *protocol.EventWriter, pc ProjectConfig, deviceSetPath string,
	preparer *build.Preparer, br BuildRunner, tc ToolchainRunner, ar AppRunner, fc FileCopier, sl SourceLister,
	strict bool, maxThunkFiles, preThunkDepth int) *StreamManager {
	return newStreamManager(nil, runtime, ew, pc, deviceSetPath, preparer, br, tc, ar, fc, sl, strict, maxThunkFiles, preThunkDepth)
}

func newStreamManager(pool DevicePoolInterface, runtime simruntime.Manager, ew *protocol.EventWriter, pc ProjectConfig, deviceSetPath string,
	preparer *build.Preparer, br BuildRunner, tc ToolchainRunner, ar AppRunner, fc FileCopier, sl SourceLister,
	strict bool, maxThunkFiles, preThunkDepth int) *StreamManager {
	sm := &StreamManager{
		streams:       make(map[string]*stream),
		pool:          pool,
		runtime:       runtime,
		ew:            ew,
		strict:        strict,
		pc:            pc,
		deviceSetPath: deviceSetPath,
		preparer:      preparer,
		indexCache:    newSharedIndexCache(nil),
		maxThunkFiles: maxThunkFiles,
		preThunkDepth: preThunkDepth,
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
	defer s.cancel() // Ensure launcher goroutines stop on normal return.
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
	s.cleanupOnce.Do(func() {
		// Unregister from shared watcher.
		if sm.watcher != nil {
			sm.watcher.RemoveListener(s.id)
		}

		// Terminate the app on the device.
		if s.deviceUDID != "" {
			if p := sm.preparer.Cached(); p != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
				appRunner := sm.app
				if s.appRunner != nil {
					appRunner = s.appRunner
				}
				terminateApp(cleanupCtx, p.Settings, s.deviceUDID, sm.deviceSetPath, appRunner)
				cleanupCancel()
			}
		}

		// Remove loader socket.
		if s.dirs.Socket != "" {
			if err := os.Remove(s.dirs.Socket); err != nil && !os.IsNotExist(err) {
				slog.Debug("Failed to remove socket", "streamId", s.id, "path", s.dirs.Socket, "err", err)
			}
		}

		if s.runtimeSessionID != "" && sm.runtime != nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := sm.runtime.StopSession(stopCtx, s.runtimeSessionID); err != nil {
				slog.Warn("Failed to stop simruntime session", "streamId", s.id, "sessionId", s.runtimeSessionID, "err", err)
			}
			stopCancel()
			return
		}

		// Release the device back to pool.
		if s.deviceUDID != "" && sm.pool != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := sm.pool.Release(releaseCtx, s.deviceUDID); err != nil {
				slog.Warn("Failed to release device", "streamId", s.id, "udid", s.deviceUDID, "err", err)
			}
			releaseCancel()
		}
	})
}

// defaultStreamLauncher is the production stream lifecycle.
func (sm *StreamManager) defaultStreamLauncher(ctx context.Context, _ *StreamManager, s *stream) {
	if sm.runtime != nil {
		sm.simruntimeStreamLauncher(ctx, s)
		return
	}
	s.sendStopped(sm.ew, "internal_error", "simruntime manager is required for serve streams", "")
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
	if sm.runtime != nil {
		sm.runtime.Shutdown(shutdownCtx)
	} else if sm.pool != nil {
		sm.pool.ShutdownAll(shutdownCtx)
	}
	shutdownCancel()
}
