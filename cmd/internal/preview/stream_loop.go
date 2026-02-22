package preview

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"
)

// runStreamLoop is the per-stream event loop. It blocks until the context is
// cancelled or a fatal event occurs (boot companion crash, idb error).
//
// It handles:
//   - Debounced file change events (from the shared watcher)
//   - SwitchFile / NextPreview / Input commands (from command routing channels)
//   - Boot companion and idb_companion crash detection
func runStreamLoop(ctx context.Context, s *stream, sm *StreamManager,
	bs *buildSettings, idbErrCh <-chan error) error {

	wctx := watchContext{
		device:        s.deviceUDID,
		deviceSetPath: sm.deviceSetPath,
		loaderPath:    s.loaderPath,
		serve:         true,
	}

	sourceFile := s.file

	// Build tracked set for fast lookup.
	s.ws.mu.Lock()
	trackedSet := buildTrackedSet(s.ws.trackedFiles)
	s.ws.mu.Unlock()

	// Debounce state (mirrors watcher.go pattern).
	var trackedDebounce *time.Timer
	var depDebounce *time.Timer
	trackedDebounceCh := make(chan string, 1)
	depDebounceCh := make(chan struct{}, 1)

	// Safely handle nil channels: a receive on nil channel blocks forever,
	// effectively disabling that select case.
	var bootDiedCh <-chan struct{}
	if s.bootCompanion != nil {
		bootDiedCh = s.bootCompanion.Done()
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case path := <-s.fileChangeCh:
			if trackedSet[path] {
				// Tracked file: fast hot-reload path with 200ms debounce.
				if depDebounce != nil {
					// Dependency rebuild pending; it will include this change.
					continue
				}
				if trackedDebounce != nil {
					trackedDebounce.Stop()
				}
				changedFile := path
				trackedDebounce = time.AfterFunc(200*time.Millisecond, func() {
					select {
					case trackedDebounceCh <- changedFile:
					default:
					}
				})
			} else {
				// Untracked .swift file: full rebuild path with 500ms debounce.
				if trackedDebounce != nil {
					trackedDebounce.Stop()
					trackedDebounce = nil
				}
				if depDebounce != nil {
					depDebounce.Stop()
				}
				depDebounce = time.AfterFunc(500*time.Millisecond, func() {
					select {
					case depDebounceCh <- struct{}{}:
					default:
					}
				})
			}

		case changedFile := <-trackedDebounceCh:
			s.ws.mu.Lock()
			prev := s.ws.skeletonMap[changedFile]
			s.ws.mu.Unlock()

			strategy, newSkeleton := classifyChange(changedFile, prev)

			switch strategy {
			case strategyHotReload:
				s.ws.mu.Lock()
				s.ws.skeletonMap[changedFile] = newSkeleton
				s.ws.mu.Unlock()
				if err := reloadMultiFile(ctx, sourceFile, bs, s.dirs, wctx, s.ws); err != nil {
					slog.Warn("Hot-reload error", "streamId", s.id, "err", err)
				}
			case strategyRebuild:
				if err := rebuildAndRelaunch(ctx, sourceFile, sm.pc, bs, s.dirs, wctx, s.ws); err != nil {
					slog.Warn("Rebuild error", "streamId", s.id, "err", err)
				}
				// Recompute skeletons after rebuild.
				s.ws.mu.Lock()
				for _, tf := range s.ws.trackedFiles {
					if sk, _ := computeSkeleton(tf); sk != "" {
						s.ws.skeletonMap[filepath.Clean(tf)] = sk
					}
				}
				s.ws.mu.Unlock()
			}

		case <-depDebounceCh:
			depDebounce = nil
			if err := rebuildAndRelaunch(ctx, sourceFile, sm.pc, bs, s.dirs, wctx, s.ws); err != nil {
				slog.Warn("Dependency rebuild error", "streamId", s.id, "err", err)
			}

		case newFile := <-s.switchFileCh:
			if newFile == "" {
				continue
			}
			slog.Info("Switching file", "streamId", s.id, "file", newFile)
			if err := switchFile(ctx, newFile, sm.pc, bs, s.dirs, wctx, s.ws); err != nil {
				slog.Warn("File switch error", "streamId", s.id, "err", err)
			} else {
				sourceFile = newFile
				s.ws.mu.Lock()
				trackedSet = buildTrackedSet(s.ws.trackedFiles)
				s.ws.mu.Unlock()
			}

		case <-s.nextPreviewCh:
			s.ws.mu.Lock()
			count := s.ws.previewCount
			if count <= 1 {
				s.ws.mu.Unlock()
				continue
			}
			s.ws.previewIndex = (s.ws.previewIndex + 1) % count
			s.ws.previewSelector = strconv.Itoa(s.ws.previewIndex)
			newIdx := s.ws.previewIndex
			s.ws.mu.Unlock()
			slog.Info("Switching preview", "streamId", s.id, "index", newIdx+1, "count", count)
			if err := reloadMultiFile(ctx, sourceFile, bs, s.dirs, wctx, s.ws); err != nil {
				slog.Warn("Preview switch reload error", "streamId", s.id, "err", err)
			}

		case input := <-s.inputCh:
			s.hid.HandleInput(input)

		case <-bootDiedCh:
			msg := "simulator crashed unexpectedly"
			if s.bootCompanion != nil {
				msg = fmt.Sprintf("simulator crashed: %v", s.bootCompanion.Err())
			}
			s.sendStopped(sm.ew, "runtime_error", msg, "")
			return fmt.Errorf("boot companion died")

		case err, ok := <-idbErrCh:
			if ok && err != nil {
				s.sendStopped(sm.ew, "runtime_error", fmt.Sprintf("idb_companion error: %v", err), "")
				return fmt.Errorf("idb error: %w", err)
			}
		}
	}
}

// buildTrackedSet creates a set of cleaned file paths for efficient lookup.
func buildTrackedSet(files []string) map[string]bool {
	set := make(map[string]bool, len(files))
	for _, f := range files {
		set[filepath.Clean(f)] = true
	}
	return set
}
