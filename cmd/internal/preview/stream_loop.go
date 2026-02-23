package preview

import (
	"context"
	"fmt"
	"log/slog"

	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/parsing"
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
			return nil

		case path := <-cfg.fileChangeCh:
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
			case strategyRebuild:
				if err := rebuildAndRelaunch(ctx, sourceFile, cfg.pc, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws); err != nil {
					slog.Warn("Rebuild error", "err", err)
				}
				// Recompute skeletons after rebuild.
				cfg.ws.mu.Lock()
				for _, tf := range cfg.ws.trackedFiles {
					if sk, _ := parsing.Skeleton(tf); sk != "" {
						cfg.ws.skeletonMap[filepath.Clean(tf)] = sk
					}
				}
				cfg.ws.mu.Unlock()
			}

		case <-db.DepCh:
			db.ClearDepTimer()
			if err := rebuildAndRelaunch(ctx, sourceFile, cfg.pc, cfg.bs, cfg.dirs, cfg.wctx, cfg.ws); err != nil {
				slog.Warn("Dependency rebuild error", "err", err)
			}

		case newFile := <-cfg.switchFileCh:
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
