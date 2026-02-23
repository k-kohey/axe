package preview

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
)

// eventLoopConfig holds all parameters for the unified event loop.
// Both single-stream (runWatcher) and multi-stream (runStreamLoop) paths
// build this struct and delegate to runEventLoop.
type eventLoopConfig struct {
	sourceFile string
	pc         ProjectConfig
	bs         *buildSettings
	dirs       previewDirs
	wctx       watchContext
	ws         *watchState
	hid        *protocol.HIDHandler

	// Event sources (receive-only channels).
	// A nil channel blocks forever in select, effectively disabling that case.
	fileChangeCh  <-chan string
	switchFileCh  <-chan string
	nextPreviewCh <-chan struct{}
	inputCh       <-chan *pb.Input
	idbErrCh      <-chan error
	bootDiedCh    <-chan struct{}

	// onCancel is called when the context is cancelled (e.g. Ctrl+C).
	// May be nil if no cleanup message is needed.
	onCancel func()

	// bootErr returns the error from the boot companion process, if available.
	// Used to enrich the boot crash error message. May be nil.
	bootErr func() error

	// onFatal is called when a fatal event occurs (boot crash, idb error)
	// before returning the error. Multi-stream uses this to send StreamStopped.
	// May be nil for single-stream mode (no protocol events to send).
	onFatal func(reason, message string)
}

// runEventLoop is the unified event loop shared by single-stream and multi-stream modes.
// It blocks until the context is cancelled or a fatal event occurs (boot crash, idb error).
func runEventLoop(ctx context.Context, cfg *eventLoopConfig) error {
	sourceFile := cfg.sourceFile

	cfg.ws.mu.Lock()
	trackedSet := buildTrackedSet(cfg.ws.trackedFiles)
	cfg.ws.mu.Unlock()

	db := watch.NewDebouncer()
	defer db.Stop()

	for {
		select {
		case <-ctx.Done():
			if cfg.onCancel != nil {
				cfg.onCancel()
			}
			return nil

		case path := <-cfg.fileChangeCh:
			// If a transitive dependency graph is available, ignore files
			// outside it entirely — they cannot affect the current preview.
			cfg.ws.mu.Lock()
			graph := cfg.ws.depGraph
			cfg.ws.mu.Unlock()
			if graph != nil && !graph.All[filepath.Clean(path)] {
				slog.Debug("Ignoring file change outside dependency graph", "path", path)
				continue
			}
			db.HandleFileChange(path, trackedSet)

		case changedFile := <-db.TrackedCh:
			cfg.ws.mu.Lock()
			prev := cfg.ws.skeletonMap[changedFile]
			cfg.ws.mu.Unlock()

			strategy, newSkeleton := classifyChange(changedFile, prev)

			switch strategy {
			case strategyHotReload:
				cfg.ws.mu.Lock()
				cfg.ws.skeletonMap[changedFile] = newSkeleton
				cfg.ws.mu.Unlock()
				if err := reloadMultiFile(ctx, sourceFile, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws); err != nil {
					slog.Warn("Hot-reload error", "err", err)
				}
				// NOTE: The depGraph is intentionally NOT refreshed after hot-reload.
				// A body-only change could introduce a new type reference (e.g. NewView()),
				// but recomputing the graph here would add latency to the fastest path.
				// The graph is refreshed on the next structural change (rebuild) or file switch.
			case strategyRebuild:
				if err := rebuildAndRelaunch(ctx, sourceFile, cfg.pc, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws); err != nil {
					slog.Warn("Rebuild error", "err", err)
				}
				// Recompute skeletons and trackedSet after rebuild
				// (rebuildAndRelaunch may update trackedFiles and depGraph).
				cfg.ws.mu.Lock()
				cfg.ws.skeletonMap = buildSkeletonMap(cfg.ws.trackedFiles)
				trackedSet = buildTrackedSet(cfg.ws.trackedFiles)
				cfg.ws.mu.Unlock()
			}

		case <-db.DepCh:
			db.ClearDepTimer()
			if err := rebuildAndRelaunch(ctx, sourceFile, cfg.pc, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws); err != nil {
				slog.Warn("Dependency rebuild error", "err", err)
			}

		case newFile := <-cfg.switchFileCh:
			db.Reset()
			newSrc, newSet := handleSwitchFileCmd(ctx, newFile, sourceFile, trackedSet, cfg.pc, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws)
			sourceFile = newSrc
			trackedSet = newSet

		case <-cfg.nextPreviewCh:
			handleNextPreviewCmd(ctx, sourceFile, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws)

		case input := <-cfg.inputCh:
			if cfg.hid != nil {
				cfg.hid.HandleInput(ctx, input)
			}

		case <-cfg.bootDiedCh:
			msg := "simulator crashed unexpectedly"
			if cfg.bootErr != nil {
				if err := cfg.bootErr(); err != nil {
					msg = fmt.Sprintf("simulator crashed: %v", err)
				}
			}
			if cfg.onFatal != nil {
				cfg.onFatal("runtime_error", msg)
			}
			return fmt.Errorf("boot companion died")

		case err, ok := <-cfg.idbErrCh:
			if ok && err != nil {
				msg := fmt.Sprintf("idb_companion error: %v", err)
				if cfg.onFatal != nil {
					cfg.onFatal("runtime_error", msg)
				}
				return fmt.Errorf("idb error: %w", err)
			}
		}
	}
}

// runStreamLoop is the per-stream event loop for multi-stream mode.
// It assembles an eventLoopConfig from the stream and delegates to runEventLoop.
func runStreamLoop(ctx context.Context, s *stream, sm *StreamManager,
	bs *buildSettings, idbErrCh <-chan error) error {

	wctx := watchContext{
		device:        s.deviceUDID,
		deviceSetPath: sm.deviceSetPath,
		loaderPath:    s.loaderPath,
		serve:         true,
		compiler:      s.compiler,
		build:         sm.build,
		toolchain:     sm.toolchain,
		app:           sm.app,
		copier:        sm.copier,
		sources:       sm.sources,
	}

	var bootDiedCh <-chan struct{}
	if s.bootCompanion != nil {
		bootDiedCh = s.bootCompanion.Done()
	}

	cfg := &eventLoopConfig{
		sourceFile:    s.file,
		pc:            sm.pc,
		bs:            bs,
		dirs:          s.dirs,
		wctx:          wctx,
		ws:            s.ws,
		hid:           s.hid,
		fileChangeCh:  s.fileChangeCh,
		switchFileCh:  s.switchFileCh,
		nextPreviewCh: s.nextPreviewCh,
		inputCh:       s.inputCh,
		idbErrCh:      idbErrCh,
		bootDiedCh:    bootDiedCh,
		bootErr: func() error {
			if s.bootCompanion != nil {
				return s.bootCompanion.Err()
			}
			return nil
		},
		onFatal: func(reason, message string) {
			s.sendStopped(sm.ew, reason, message, "")
		},
	}

	return runEventLoop(ctx, cfg)
}

// buildTrackedSet creates a set of cleaned file paths for efficient lookup.
func buildTrackedSet(files []string) map[string]bool {
	set := make(map[string]bool, len(files))
	for _, f := range files {
		set[filepath.Clean(f)] = true
	}
	return set
}
