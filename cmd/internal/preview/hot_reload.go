package preview

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
)

func sendWatchStatus(wctx watchContext, phase string) {
	if !wctx.serve || wctx.ew == nil {
		return
	}
	if err := wctx.ew.Send(&pb.Event{
		StreamId: wctx.streamID,
		Payload: &pb.Event_StreamStatus{
			StreamStatus: &pb.StreamStatus{Phase: phase},
		},
	}); err != nil {
		slog.Warn("Failed to send StreamStatus in watcher", "phase", phase, "err", err)
	}
}

// runWatcher sets up file watching and command dispatching, then delegates
// to the unified event loop. It uses SharedWatcher for file change detection
// and dispatchStdinCommands / dispatchProtocolCommands for stdin routing.
func runWatcher(ctx context.Context, sourceFile string, pc ProjectConfig,
	bs *build.Settings, dirs previewDirs, wctx watchContext,
	ws *watchState, hid *protocol.HIDHandler,
	idbErrCh <-chan error, bootDiedCh <-chan struct{}) error {

	// Set up shared file watcher.
	watchRoot := filepath.Dir(pc.PrimaryPath())
	sw, err := watch.NewSharedWatcher(ctx, watchRoot, wctx.sources)
	if err != nil {
		return fmt.Errorf("creating file watcher: %w", err)
	}
	defer sw.Close()

	fmt.Fprintf(os.Stderr, "Watching %s for changes (Enter to cycle previews, Ctrl+C to stop)...\n", watchRoot)

	// Create typed channels for the event loop.
	fileChangeCh := make(chan string, 1)
	switchFileCh := make(chan string, 1)
	nextPreviewCh := make(chan struct{}, 1)
	forceRebuildCh := make(chan struct{}, 1)
	inputCh := make(chan *pb.Input, 1)

	// Register as a listener on the shared watcher.
	const singleStreamID = "single"
	sw.AddListener(singleStreamID, fileChangeCh)

	// Dispatch stdin commands to typed channels.
	if wctx.serve {
		protoCmdCh := make(chan *pb.Command, 1)
		go readProtocolCommands(ctx, wctx.ew, protoCmdCh)
		go dispatchProtocolCommands(ctx, protoCmdCh, hid, switchFileCh, nextPreviewCh, forceRebuildCh, inputCh)
	} else {
		cmdCh := make(chan stdinCommand, 1)
		go readStdinCommands(cmdCh, false)
		go dispatchStdinCommands(ctx, cmdCh, hid, switchFileCh, nextPreviewCh, forceRebuildCh, inputCh)
	}

	cfg := &eventLoopConfig{
		sourceFile:     sourceFile,
		pc:             pc,
		bs:             bs,
		dirs:           dirs,
		wctx:           wctx,
		ws:             ws,
		hid:            hid,
		fileChangeCh:   fileChangeCh,
		switchFileCh:   switchFileCh,
		nextPreviewCh:  nextPreviewCh,
		forceRebuildCh: forceRebuildCh,
		inputCh:        inputCh,
		idbErrCh:       idbErrCh,
		bootDiedCh:     bootDiedCh,
		onCancel: func() {
			fmt.Fprintln(os.Stderr, "\nStopping watcher...")
		},
	}

	return runEventLoop(ctx, cfg)
}

// reloadMultiFile generates a combined thunk for all tracked files and hot-reloads.
func reloadMultiFile(ctx context.Context, sourceFile string, bs *build.Settings, dirs previewDirs, wctx watchContext, ws *watchState) error {
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

	var cache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		cache = ws.indexCache.Get()
	}

	sendWatchStatus(wctx, "compiling_thunk")
	dylibPath, err := compilePipeline(ctx, sourceFile, tracked, cache, bs, dirs, selector, counter, wctx.toolchain)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	if err := deploy(ctx, dylibPath, dirs, bs, wctx); err != nil {
		return err
	}
	sendWatchStatus(wctx, "running")

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	return nil
}

// switchFile handles switching to a new source file in serve mode.
// It resolves dependencies, generates a thunk, and attempts a hot-reload.
// Falls back to rebuild+relaunch if compile fails, and full restart as last resort.
func switchFile(ctx context.Context, newSourceFile string, pc ProjectConfig, bs *build.Settings, dirs previewDirs, wctx watchContext, ws *watchState) error {
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

	// 1. Resolve dependencies for the new file using index store.
	var cache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		cache = ws.indexCache.Get()
	}
	newGraph, _, err := analysis.ResolveTransitiveDependencies(ctx, newSourceFile, cache)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("Failed to resolve dependencies for new file", "err", err)
	}

	trackedFiles := []string{newSourceFile}
	if newGraph != nil {
		trackedFiles = append(trackedFiles, newGraph.DepsUpTo(ws.preThunkDepth)...)
	}

	// 2. Parse source and dependency files.
	files, trackedFiles, err := parseAndFilterTrackedFiles(newSourceFile, trackedFiles, cache)
	if err != nil {
		return err
	}

	// Determine preview count/index for the new file.
	previewCount := 0
	if blocks, err := analysis.PreviewBlocks(newSourceFile); err == nil {
		previewCount = len(blocks)
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	ws.previewSelector = "0"
	ws.mu.Unlock()

	// 3. Fast path: generate thunk → compile → hot-reload.
	cfg := compileConfigFromSettings(bs)
	thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, dirs.Thunk, "0", newSourceFile, counter)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPaths, cfg, dirs.Thunk, dirs.Build, counter, newSourceFile, wctx.toolchain)
	if err != nil {
		// If context was cancelled (e.g. Ctrl+C), skip retries.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 4. Retry: rebuild project then try compile again.
		slog.Info("Thunk compile failed, attempting rebuild", "err", err)
		if buildErr := build.Run(ctx, pc, dirs.ProjectDirs, wctx.build); buildErr != nil {
			return fmt.Errorf("rebuild: %w", buildErr)
		}
		dylibPath, err = codegen.CompileThunk(ctx, thunkPaths, cfg, dirs.Thunk, dirs.Build, counter, newSourceFile, wctx.toolchain)
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
	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.trackedFiles = trackedFiles
	ws.skeletonMap = buildSkeletonMap(trackedFiles)
	ws.depGraph = newGraph
	ws.previewIndex = 0
	ws.previewCount = previewCount
	ws.incrementalCount = 0
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
func rebuildAndRelaunch(ctx context.Context, sourceFile string, pc ProjectConfig, bs *build.Settings, dirs previewDirs, wctx watchContext, ws *watchState) error {
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
	sendWatchStatus(wctx, "building")

	if err := build.Run(ctx, pc, dirs.ProjectDirs, wctx.build); err != nil {
		return fmt.Errorf("incremental build: %w", err)
	}

	var rebuildCache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		rebuildCache = ws.indexCache.Get()
	}

	files := parseTrackedFiles(sourceFile, tracked, rebuildCache)

	// Fallback: if no tracked dependency files have types, use target only.
	if len(files) == 0 {
		types, imports, err := analysis.SourceFile(sourceFile, rebuildCache)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		var moduleName string
		if rebuildCache != nil {
			moduleName = rebuildCache.FileModuleName(sourceFile)
		}
		files = append(files, analysis.FileThunkData{
			FileName:   filepath.Base(sourceFile),
			AbsPath:    sourceFile,
			Types:      types,
			Imports:    imports,
			ModuleName: moduleName,
		})
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	selector := ws.previewSelector
	ws.mu.Unlock()

	thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, dirs.Thunk, selector, sourceFile, counter)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	sendWatchStatus(wctx, "compiling_thunk")
	dylibPath, err := codegen.CompileThunk(ctx, thunkPaths, compileConfigFromSettings(bs), dirs.Thunk, dirs.Build, counter, sourceFile, wctx.toolchain)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("compile: %w", err)
	}

	terminateApp(ctx, bs, wctx.device, wctx.deviceSetPath, wctx.app)

	sendWatchStatus(wctx, "installing")
	if _, err := installApp(ctx, bs, dirs, wctx.device, wctx.deviceSetPath, wctx.app, wctx.copier); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	sendWatchStatus(wctx, "running")
	if err := launchWithHotReload(ctx, bs, wctx.loaderPath, dylibPath, dirs.Socket, wctx.device, wctx.deviceSetPath, wctx.app); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	// Recompute transitive dependency graph after rebuild.
	// Skip if context is cancelled; the caller will handle shutdown.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Reload Index Store cache after rebuild (index data is updated by the build).
	// Update the shared cache so all streams (in multi-stream mode) see fresh data.
	projectRoot := filepath.Dir(pc.PrimaryPath())
	newCache, cacheErr := analysis.LoadIndexStore(ctx, dirs.IndexStorePath(), projectRoot)
	if cacheErr != nil && ctx.Err() == nil {
		slog.Warn("Failed to reload index store cache after rebuild", "err", cacheErr)
	}
	if ws.indexCache != nil {
		ws.indexCache.Set(newCache)
	}

	var resolveCache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		resolveCache = ws.indexCache.Get()
	}
	newGraph, _, err := analysis.ResolveTransitiveDependencies(ctx, sourceFile, resolveCache)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("Failed to recompute dependency graph after rebuild", "err", err)
	}

	newTracked := []string{sourceFile}
	if newGraph != nil {
		newTracked = append(newTracked, newGraph.DepsUpTo(ws.preThunkDepth)...)
	}

	ws.mu.Lock()
	ws.reloadCounter++
	ws.depGraph = newGraph
	ws.trackedFiles = newTracked
	ws.incrementalCount = 0
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Preview rebuilt and relaunched.")
	return nil
}

// tryIncrementalReload attempts to add the given dependency files to the
// tracked set and hot-reload without a full rebuild.
//
// Returns true if the incremental reload succeeded (or was a no-op because
// all files were already tracked). Returns false if a full rebuild is needed.
func tryIncrementalReload(
	ctx context.Context,
	depFiles []string,
	sourceFile string,
	pc ProjectConfig,
	bs *build.Settings,
	dirs previewDirs,
	wctx watchContext,
	ws *watchState,
) bool {
	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build in progress, falling back to rebuild")
		return false
	}
	if ws.incrementalCount >= defaultStaleThreshold {
		ws.mu.Unlock()
		slog.Info("Stale threshold reached, forcing rebuild",
			"incrementalCount", ws.incrementalCount,
			"threshold", defaultStaleThreshold)
		return false
	}

	// Filter: keep only files that are in the depGraph but not yet tracked.
	trackedSet := buildTrackedSet(ws.trackedFiles)
	graph := ws.depGraph
	ws.mu.Unlock()

	// Without a dependency graph we cannot determine whether the changed files
	// are relevant. Fall back to a full rebuild so the graph is recomputed.
	if graph == nil {
		slog.Info("No dependency graph available, falling back to rebuild")
		return false
	}

	var newFiles []string
	for _, f := range depFiles {
		clean := filepath.Clean(f)
		if trackedSet[clean] {
			continue // already tracked
		}
		if !graph.All[clean] {
			continue // outside dependency graph, can't affect preview
		}
		newFiles = append(newFiles, clean)
	}
	if len(newFiles) == 0 {
		slog.Debug("All dep files already tracked or outside graph, no-op")
		return true
	}

	// Check basename collisions.
	ws.mu.Lock()
	tracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	for _, nf := range newFiles {
		if codegen.HasBaseNameCollision(nf, tracked) {
			slog.Info("Basename collision detected, falling back to rebuild",
				"file", nf)
			return false
		}
	}

	// Evict oldest files if adding newFiles would exceed maxThunkFiles.
	ws.mu.Lock()
	maxFiles := ws.maxThunkFiles
	ws.mu.Unlock()

	if maxFiles > 0 {
		totalAfter := len(tracked) + len(newFiles)
		if totalAfter > maxFiles {
			evictCount := totalAfter - maxFiles
			tracked = evictOldest(tracked, sourceFile, evictCount)
		}
	}

	// Snapshot for rollback.
	ws.mu.Lock()
	snapTracked := append([]string{}, ws.trackedFiles...)
	snapSkeleton := make(map[string]string, len(ws.skeletonMap))
	maps.Copy(snapSkeleton, ws.skeletonMap)
	snapCounter := ws.reloadCounter
	counter := ws.reloadCounter

	// Apply new files.
	ws.trackedFiles = append(tracked, newFiles...)
	for _, nf := range newFiles {
		if sk, err := analysis.Skeleton(nf); err == nil {
			ws.skeletonMap[filepath.Clean(nf)] = sk
		}
	}
	selector := ws.previewSelector
	allTracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	slog.Info("Attempting incremental reload",
		"newFiles", len(newFiles),
		"totalTracked", len(allTracked))
	fmt.Fprintln(os.Stderr, "\nDependency changed, attempting incremental reload...")

	var cache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		cache = ws.indexCache.Get()
	}

	sendWatchStatus(wctx, "compiling_thunk")
	dylibPath, err := compilePipeline(ctx, sourceFile, allTracked, cache, bs, dirs, selector, counter, wctx.toolchain)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		slog.Info("Incremental compile failed, rolling back", "err", err)
		// Rollback.
		ws.mu.Lock()
		ws.trackedFiles = snapTracked
		ws.skeletonMap = snapSkeleton
		ws.reloadCounter = snapCounter
		ws.mu.Unlock()
		return false
	}

	if err := deploy(ctx, dylibPath, dirs, bs, wctx); err != nil {
		slog.Warn("Incremental deploy failed, rolling back", "err", err)
		ws.mu.Lock()
		ws.trackedFiles = snapTracked
		ws.skeletonMap = snapSkeleton
		ws.reloadCounter = snapCounter
		ws.mu.Unlock()
		return false
	}
	sendWatchStatus(wctx, "running")

	ws.mu.Lock()
	ws.reloadCounter++
	ws.incrementalCount++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Preview incrementally reloaded.")
	return true
}

// evictOldest removes the oldest entries from tracked to make room for new files.
// tracked[0] is the source file and is never evicted. Eviction starts from index 1.
func evictOldest(tracked []string, sourceFile string, evictCount int) []string {
	if evictCount <= 0 || len(tracked) <= 1 {
		return tracked
	}

	// Find the source file index (usually 0, but be safe).
	cleanSource := filepath.Clean(sourceFile)
	sourceIdx := -1
	for i, f := range tracked {
		if filepath.Clean(f) == cleanSource {
			sourceIdx = i
			break
		}
	}

	var result []string
	evicted := 0
	for i, f := range tracked {
		if i == sourceIdx {
			result = append(result, f) // always keep source
			continue
		}
		if evicted < evictCount {
			slog.Debug("Evicting tracked file for capacity", "file", f)
			evicted++
			continue
		}
		result = append(result, f)
	}
	return result
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
	newSkeleton, err := analysis.Skeleton(sourceFile)
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

// handleSwitchFileCmd handles a file switch command from either legacy stdin or
// the protocol. On success it returns the new sourceFile and updated trackedSet.
// On error it logs and returns the original values unchanged.
func handleSwitchFileCmd(
	ctx context.Context, newFile, sourceFile string, trackedSet map[string]bool,
	pc ProjectConfig, bs *build.Settings, dirs previewDirs, wctx watchContext, ws *watchState,
) (string, map[string]bool) {
	if newFile == "" {
		return sourceFile, trackedSet
	}
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
	bs *build.Settings, dirs previewDirs, wctx watchContext, ws *watchState,
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

// cleanOldDylibs removes thunk dylib, object, and Swift files older than keepAfter.
func cleanOldDylibs(thunkDir string, keepAfter int) {
	for i := range keepAfter {
		// Remove dylib and legacy .o files.
		for _, ext := range []string{".dylib", ".o"} {
			p := filepath.Join(thunkDir, fmt.Sprintf("thunk_%d%s", i, ext))
			if err := os.Remove(p); err == nil {
				slog.Debug("Cleaned old thunk artifact", "path", p)
			}
		}
		// Remove per-file thunk .swift files (thunk_{counter}_*.swift).
		pattern := filepath.Join(thunkDir, fmt.Sprintf("thunk_%d_*.swift", i))
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if err := os.Remove(m); err == nil {
				slog.Debug("Cleaned old thunk file", "path", m)
			}
		}
	}
}
