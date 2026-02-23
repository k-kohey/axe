package preview

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/k-kohey/axe/internal/preview/codegen"

	"github.com/fsnotify/fsnotify"
	"github.com/k-kohey/axe/internal/preview/parsing"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
)

// watchState holds mutable state for the watch loop, protected by a mutex.
// Immutable configuration (device, loaderPath, etc.) lives in watchContext.
type watchState struct {
	mu              sync.Mutex
	reloadCounter   int
	previewSelector string
	previewIndex    int               // current 0-based preview index
	previewCount    int               // total number of #Preview blocks (0 = unknown)
	building        bool              // true while rebuildAndRelaunch is running
	skeletonMap     map[string]string // file path → skeleton hash
	trackedFiles    []string          // target + 1-level dependency file paths
}

func runWatcher(ctx context.Context, sourceFile string, pc ProjectConfig,
	bs *buildSettings, dirs previewDirs, wctx watchContext,
	ws *watchState, hid *protocol.HIDHandler, events watchEvents) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating file watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Watch directories containing Swift source files.
	// Use git to discover them (fast, respects .gitignore), falling back to WalkDir.
	watchRoot := filepath.Dir(pc.primaryPath())
	watchDirs, err := wctx.sources.SwiftDirs(ctx, watchRoot)
	if err != nil {
		slog.Debug("git ls-files unavailable, falling back to WalkDir", "err", err)
		watchDirs, err = watch.WalkSwiftDirs(watchRoot)
		if err != nil {
			return fmt.Errorf("setting up directory watch: %w", err)
		}
	}
	for _, d := range watchDirs {
		if err := watcher.Add(d); err != nil {
			slog.Debug("Cannot watch directory", "path", d, "err", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Watching %s for changes (Enter to cycle previews, Ctrl+C to stop)...\n", watchRoot)

	// Read commands from stdin (preview cycling or file switching in serve mode).
	// In serve mode, new Command protocol is used. In non-serve mode, legacy stdinCommand.
	cmdCh := make(chan stdinCommand, 1)
	protoCmdCh := make(chan *pb.Command, 1)
	if wctx.serve {
		go readProtocolCommands(ctx, wctx.ew, protoCmdCh)
	} else {
		go readStdinCommands(cmdCh, false)
	}

	db := watch.NewDebouncer()
	defer db.Stop()

	// Build a set of tracked file paths for efficient lookup.
	ws.mu.Lock()
	trackedFiles := ws.trackedFiles
	ws.mu.Unlock()
	trackedSet := buildTrackedSet(trackedFiles)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "\nStopping watcher...")
			return nil // cleanup handled by defer in Run

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Only react to .swift files
			if !strings.HasSuffix(event.Name, ".swift") {
				continue
			}

			// Accept Write and Create (atomic save = rename creates new file)
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			db.HandleFileChange(filepath.Clean(event.Name), trackedSet)

		case changedFile := <-db.TrackedCh:
			ws.mu.Lock()
			prev := ws.skeletonMap[changedFile]
			ws.mu.Unlock()

			strategy, newSkeleton := classifyChange(changedFile, prev)

			switch strategy {
			case strategyHotReload:
				ws.mu.Lock()
				ws.skeletonMap[changedFile] = newSkeleton
				ws.mu.Unlock()
				if err := reloadMultiFile(ctx, sourceFile, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "Reload error: %v\n", err)
				}
			case strategyRebuild:
				if err := rebuildAndRelaunch(ctx, sourceFile, pc, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "Rebuild error: %v\n", err)
				}
				// Recompute skeletons for all tracked files after rebuild.
				ws.mu.Lock()
				for _, tf := range ws.trackedFiles {
					if s, _ := parsing.Skeleton(tf); s != "" {
						ws.skeletonMap[filepath.Clean(tf)] = s
					}
				}
				ws.mu.Unlock()
			}

		case <-db.DepCh:
			db.ClearDepTimer()
			if err := rebuildAndRelaunch(ctx, sourceFile, pc, bs, dirs, wctx, ws); err != nil {
				fmt.Fprintf(os.Stderr, "Rebuild error: %v\n", err)
			}

		case cmd := <-cmdCh:
			switch cmd.Type {
			case "switchFile":
				if cmd.Path == "" {
					continue
				}
				sourceFile, trackedSet = handleSwitchFileCmd(ctx, cmd.Path, sourceFile, trackedSet, pc, bs, dirs, wctx, ws)
			case "nextPreview":
				handleNextPreviewCmd(ctx, sourceFile, bs, dirs, wctx, ws)
			case "tap":
				hid.HandleTap(ctx, cmd.X, cmd.Y)
			case "swipe":
				hid.HandleSwipe(ctx, cmd.StartX, cmd.StartY, cmd.EndX, cmd.EndY, cmd.Duration)
			case "text":
				hid.HandleInput(ctx, &pb.Input{Event: &pb.Input_Text{Text: &pb.TextEvent{Value: cmd.Value}}})
			case "touchDown":
				hid.HandleInput(ctx, &pb.Input{Event: &pb.Input_TouchDown{TouchDown: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}})
			case "touchMove":
				hid.HandleInput(ctx, &pb.Input{Event: &pb.Input_TouchMove{TouchMove: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}})
			case "touchUp":
				hid.HandleInput(ctx, &pb.Input{Event: &pb.Input_TouchUp{TouchUp: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}})
			}

		case protoCmd, ok := <-protoCmdCh:
			if !ok {
				protoCmdCh = nil // channel closed (EOF) → disable this case
				continue
			}
			switch {
			case protoCmd.GetSwitchFile() != nil:
				if protoCmd.GetSwitchFile().GetFile() == "" {
					continue
				}
				sourceFile, trackedSet = handleSwitchFileCmd(ctx, protoCmd.GetSwitchFile().GetFile(), sourceFile, trackedSet, pc, bs, dirs, wctx, ws)
			case protoCmd.GetNextPreview() != nil:
				handleNextPreviewCmd(ctx, sourceFile, bs, dirs, wctx, ws)
			case protoCmd.GetInput() != nil:
				hid.HandleInput(ctx, protoCmd.GetInput())
			default:
				slog.Warn("Ignoring unhandled command in single-stream mode", "streamId", protoCmd.GetStreamId())
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("Watcher error", "err", err)

		case err, ok := <-events.idbErr:
			if ok && err != nil {
				return fmt.Errorf("idb_companion error: %w", err)
			}

		case <-events.bootDied:
			return fmt.Errorf("simulator crashed unexpectedly (boot companion process exited)")
		}
	}
}

// reloadMultiFile generates a combined thunk for all tracked files and hot-reloads.
func reloadMultiFile(ctx context.Context, sourceFile string, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build in progress, skipping hot-reload")
		return nil
	}
	counter := ws.reloadCounter
	tracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "\nFile changed, reloading...")

	updatePreviewCount(sourceFile, ws)

	// Read selector AFTER updatePreviewCount so that a reset (e.g. preview
	// removed) is reflected in this compile cycle.
	ws.mu.Lock()
	selector := ws.previewSelector
	ws.mu.Unlock()

	dylibPath, err := compilePipeline(ctx, sourceFile, tracked, bs, dirs, selector, counter, wctx.toolchain)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	if err := deploy(ctx, dylibPath, dirs, bs, wctx); err != nil {
		return err
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	return nil
}

// switchFile handles switching to a new source file in serve mode.
// It resolves dependencies, generates a thunk, and attempts a hot-reload.
// Falls back to rebuild+relaunch if compile fails, and full restart as last resort.
func switchFile(ctx context.Context, newSourceFile string, pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	if _, err := os.Stat(newSourceFile); err != nil {
		return fmt.Errorf("source file not found: %s", newSourceFile)
	}

	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build in progress, skipping file switch")
		return nil
	}
	ws.mu.Unlock()

	// 1. Resolve dependencies for the new file.
	projectRoot := filepath.Dir(pc.primaryPath())
	depFiles, err := parsing.ResolveDependencies(ctx, newSourceFile, projectRoot, wctx.sources)
	if err != nil {
		slog.Warn("Failed to resolve dependencies for new file", "err", err)
	}

	trackedFiles := []string{newSourceFile}
	trackedFiles = append(trackedFiles, depFiles...)

	// 2. Parse source and dependency files, filter private type collisions.
	files, trackedFiles, err := parseAndFilterTrackedFiles(newSourceFile, trackedFiles)
	if err != nil {
		return err
	}

	// Determine preview count/index for the new file.
	previewCount := 0
	if blocks, err := parsing.PreviewBlocks(newSourceFile); err == nil {
		previewCount = len(blocks)
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	ws.previewSelector = "0"
	ws.mu.Unlock()

	// 3. Fast path: generate thunk → compile → hot-reload.
	cfg := compileConfigFromBS(bs)
	thunkPath, err := codegen.GenerateCombinedThunk(files, bs.ModuleName, dirs.Thunk, "0", newSourceFile)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPath, cfg, dirs.Thunk, dirs.Build, counter, newSourceFile, wctx.toolchain)
	if err != nil {
		// If context was cancelled (e.g. Ctrl+C), skip retries.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 4. Retry: rebuild project then try compile again.
		slog.Info("Thunk compile failed, attempting rebuild", "err", err)
		if buildErr := buildProject(ctx, pc, dirs, wctx.build); buildErr != nil {
			return fmt.Errorf("rebuild: %w", buildErr)
		}
		dylibPath, err = codegen.CompileThunk(ctx, thunkPath, cfg, dirs.Thunk, dirs.Build, counter, newSourceFile, wctx.toolchain)
		if err != nil {
			// 5. Full restart as last resort.
			slog.Warn("Compile still failing after rebuild, performing full restart", "err", err)
			return rebuildAndRelaunch(ctx, newSourceFile, pc, bs, dirs, wctx, ws)
		}
	}

	if err := deploy(ctx, dylibPath, dirs, bs, wctx); err != nil {
		return err
	}

	// 6. Update watch state.
	newSkeletonMap := make(map[string]string, len(trackedFiles))
	for _, tf := range trackedFiles {
		if s, err := parsing.Skeleton(tf); err == nil {
			newSkeletonMap[filepath.Clean(tf)] = s
		}
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.trackedFiles = trackedFiles
	ws.skeletonMap = newSkeletonMap
	ws.previewIndex = 0
	ws.previewCount = previewCount
	ws.mu.Unlock()

	// Update the trackedSet used by the watcher loop.
	// Note: The caller should update sourceFile after this returns.
	return nil
}

// rebuildAndRelaunch performs an incremental build, regenerates the thunk,
// and restarts the app. Used when an untracked dependency .swift file changes.
//
// This function does NOT use compilePipeline because it has a unique fallback:
// when parseTrackedFiles returns empty, it retries with sourceFile only.
// It also uses terminate → install → launch (not the hot-reload deploy path).
func rebuildAndRelaunch(ctx context.Context, sourceFile string, pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build already in progress, skipping")
		return nil
	}
	ws.building = true
	tracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	defer func() {
		ws.mu.Lock()
		ws.building = false
		ws.mu.Unlock()
	}()

	fmt.Fprintln(os.Stderr, "\nDependency changed, rebuilding...")

	if err := buildProject(ctx, pc, dirs, wctx.build); err != nil {
		return fmt.Errorf("incremental build: %w", err)
	}

	files := parseTrackedFiles(sourceFile, tracked)

	// Fallback: if no tracked dependency files have types, use target only.
	if len(files) == 0 {
		types, imports, err := parsing.SourceFile(sourceFile)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		files = append(files, parsing.FileThunkData{
			FileName: filepath.Base(sourceFile),
			AbsPath:  sourceFile,
			Types:    types,
			Imports:  imports,
		})
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	selector := ws.previewSelector
	ws.mu.Unlock()

	thunkPath, err := codegen.GenerateCombinedThunk(files, bs.ModuleName, dirs.Thunk, selector, sourceFile)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPath, compileConfigFromBS(bs), dirs.Thunk, dirs.Build, counter, sourceFile, wctx.toolchain)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("compile: %w", err)
	}

	terminateApp(ctx, bs, wctx.device, wctx.deviceSetPath, wctx.app)

	if _, err := installApp(ctx, bs, dirs, wctx.device, wctx.deviceSetPath, wctx.app, wctx.copier); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := launchWithHotReload(ctx, bs, wctx.loaderPath, dylibPath, dirs.Socket, wctx.device, wctx.deviceSetPath, wctx.app); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Preview rebuilt and relaunched.")
	return nil
}

// reloadStrategy describes whether a source file change can be handled via
// hot-reload or requires a full rebuild.
type reloadStrategy int

const (
	strategyHotReload reloadStrategy = iota
	strategyRebuild
)

// classifyChange compares the current source skeleton against prevSkeleton and
// returns the appropriate reload strategy plus the new skeleton hash.
// If the skeleton cannot be computed, strategyRebuild is returned with an empty
// skeleton (the caller should recompute after rebuilding).
func classifyChange(sourceFile string, prevSkeleton string) (reloadStrategy, string) {
	newSkeleton, err := parsing.Skeleton(sourceFile)
	if err != nil {
		slog.Warn("Skeleton computation failed, falling back to rebuild", "err", err)
		return strategyRebuild, ""
	}
	if newSkeleton == prevSkeleton {
		return strategyHotReload, newSkeleton
	}
	slog.Info("Structural change detected, performing full rebuild")
	return strategyRebuild, newSkeleton
}

// stdinCommand represents a command received from stdin (JSON Lines protocol).
type stdinCommand struct {
	Type     string  `json:"type"`
	Path     string  `json:"path,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	StartX   float64 `json:"startX,omitempty"`
	StartY   float64 `json:"startY,omitempty"`
	EndX     float64 `json:"endX,omitempty"`
	EndY     float64 `json:"endY,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Value    string  `json:"value,omitempty"`
}

// readStdinCommands reads JSON Lines from stdin and sends commands on ch.
// In serve mode, JSON objects are parsed; non-JSON lines are treated as legacy
// file path commands for backwards compatibility.
// In non-serve mode, any input triggers a preview cycle.
func readStdinCommands(ch chan<- stdinCommand, serve bool) {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		var cmd stdinCommand

		if serve && line != "" {
			// Try JSON parse first.
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				// Legacy: treat non-JSON as file path.
				cmd = stdinCommand{Type: "switchFile", Path: line}
			}
		} else {
			// Empty line or any input in non-serve mode → preview cycle.
			cmd = stdinCommand{Type: "nextPreview"}
		}

		select {
		case ch <- cmd:
		default: // don't block if previous command hasn't been processed
		}
	}
}

// readProtocolCommands reads JSON Lines from stdin and parses them as Command structs.
// Invalid JSON lines are logged and skipped. EOF causes the channel to close.
func readProtocolCommands(ctx context.Context, ew *protocol.EventWriter, ch chan<- *pb.Command) {
	protocol.ReadCommands(ctx, os.Stdin, ew, func(cmd *pb.Command) {
		select {
		case ch <- cmd:
		default:
		}
	})
	close(ch)
}

// handleSwitchFileCmd handles a file switch command from either legacy stdin or
// the protocol. On success it returns the new sourceFile and updated trackedSet.
// On error it logs and returns the original values unchanged.
func handleSwitchFileCmd(
	ctx context.Context, newFile, sourceFile string, trackedSet map[string]bool,
	pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState,
) (string, map[string]bool) {
	fmt.Fprintf(os.Stderr, "\nSwitching file to %s...\n", newFile)
	if err := switchFile(ctx, newFile, pc, bs, dirs, wctx, ws); err != nil {
		fmt.Fprintf(os.Stderr, "File switch error: %v\n", err)
		return sourceFile, trackedSet
	}
	ws.mu.Lock()
	ts := buildTrackedSet(ws.trackedFiles)
	ws.mu.Unlock()
	return newFile, ts
}

// handleNextPreviewCmd cycles to the next preview index and hot-reloads.
func handleNextPreviewCmd(
	ctx context.Context, sourceFile string,
	bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState,
) {
	ws.mu.Lock()
	count := ws.previewCount
	if count <= 1 {
		ws.mu.Unlock()
		return
	}
	ws.previewIndex = (ws.previewIndex + 1) % count
	ws.previewSelector = strconv.Itoa(ws.previewIndex)
	newIdx := ws.previewIndex
	ws.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\nSwitching to preview %d/%d...\n", newIdx+1, count)
	if err := reloadMultiFile(ctx, sourceFile, bs, dirs, wctx, ws); err != nil {
		fmt.Fprintf(os.Stderr, "Reload error: %v\n", err)
	}
}

// cleanOldDylibs removes thunk dylib and object files older than keepAfter.
func cleanOldDylibs(thunkDir string, keepAfter int) {
	for i := range keepAfter {
		for _, ext := range []string{".dylib", ".o"} {
			p := filepath.Join(thunkDir, fmt.Sprintf("thunk_%d%s", i, ext))
			if err := os.Remove(p); err == nil {
				slog.Debug("Cleaned old thunk artifact", "path", p)
			}
		}
	}
}
