package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/watch"
)

// runWatcher sets up file watching and command dispatching, then delegates
// to the unified event loop. It uses SharedWatcher for file change detection
// and dispatchStdinCommands / dispatchProtocolCommands for stdin routing.
func runWatcher(ctx context.Context, sourceFile string, pc ProjectConfig,
	bs *buildSettings, dirs previewDirs, wctx watchContext,
	ws *watchState, hid *protocol.HIDHandler,
	idbErrCh <-chan error, bootDiedCh <-chan struct{}) error {

	// Set up shared file watcher.
	watchRoot := filepath.Dir(pc.primaryPath())
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
	inputCh := make(chan *pb.Input, 1)

	// Register as a listener on the shared watcher.
	const singleStreamID = "single"
	sw.AddListener(singleStreamID, fileChangeCh)

	// Dispatch stdin commands to typed channels.
	if wctx.serve {
		protoCmdCh := make(chan *pb.Command, 1)
		go readProtocolCommands(ctx, wctx.ew, protoCmdCh)
		go dispatchProtocolCommands(ctx, protoCmdCh, hid, switchFileCh, nextPreviewCh, inputCh)
	} else {
		cmdCh := make(chan stdinCommand, 1)
		go readStdinCommands(cmdCh, false)
		go dispatchStdinCommands(ctx, cmdCh, hid, switchFileCh, nextPreviewCh, inputCh)
	}

	cfg := &eventLoopConfig{
		sourceFile:    sourceFile,
		pc:            pc,
		bs:            bs,
		dirs:          dirs,
		wctx:          wctx,
		ws:            ws,
		hid:           hid,
		fileChangeCh:  fileChangeCh,
		switchFileCh:  switchFileCh,
		nextPreviewCh: nextPreviewCh,
		inputCh:       inputCh,
		idbErrCh:      idbErrCh,
		bootDiedCh:    bootDiedCh,
		onCancel: func() {
			fmt.Fprintln(os.Stderr, "\nStopping watcher...")
		},
	}

	return runEventLoop(ctx, cfg)
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

	var cache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		cache = ws.indexCache.Get()
	}

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

	// 1. Resolve dependencies for the new file using index store.
	var cache *analysis.IndexStoreCache
	if ws.indexCache != nil {
		cache = ws.indexCache.Get()
	}
	newGraph, depFiles, err := analysis.ResolveTransitiveDependencies(ctx, newSourceFile, cache)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("Failed to resolve dependencies for new file", "err", err)
	}

	trackedFiles := []string{newSourceFile}
	trackedFiles = append(trackedFiles, depFiles...)

	// 2. Parse source and dependency files, filter private type collisions.
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
	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.trackedFiles = trackedFiles
	ws.skeletonMap = buildSkeletonMap(trackedFiles)
	ws.depGraph = newGraph
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
		files = append(files, analysis.FileThunkData{
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

	// Recompute transitive dependency graph after rebuild.
	// Skip if context is cancelled; the caller will handle shutdown.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Reload Index Store cache after rebuild (index data is updated by the build).
	// Update the shared cache so all streams (in multi-stream mode) see fresh data.
	projectRoot := filepath.Dir(pc.primaryPath())
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
	newGraph, newDeps, err := analysis.ResolveTransitiveDependencies(ctx, sourceFile, resolveCache)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("Failed to recompute dependency graph after rebuild", "err", err)
	}

	newTracked := []string{sourceFile}
	newTracked = append(newTracked, newDeps...)

	ws.mu.Lock()
	ws.reloadCounter++
	ws.depGraph = newGraph
	ws.trackedFiles = newTracked
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
	pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState,
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
