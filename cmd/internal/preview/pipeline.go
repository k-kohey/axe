package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
)

// parseTrackedFiles parses all tracked files and builds fileThunkData slices.
// sourceFile is treated specially: parseSourceFile is used instead of parseDependencyFile.
// All parse errors (including sourceFile) are skipped with a debug log (lenient mode).
// This is intentional: hot-reload triggers while the user is editing, so syntax errors
// in the source file are expected and should not be fatal.
// Callers that need stricter behavior (Run, switchFile) should check the result
// for sourceFile presence after calling this function.
func parseTrackedFiles(sourceFile string, trackedFiles []string, cache *analysis.IndexStoreCache) []analysis.FileThunkData {
	var files []analysis.FileThunkData
	for _, tf := range trackedFiles {
		var types []analysis.TypeInfo
		var imports []string
		var err error
		if tf == sourceFile {
			types, imports, err = analysis.SourceFile(tf, cache)
		} else {
			types, imports, err = analysis.DependencyFile(tf, cache)
		}
		if err != nil {
			slog.Debug("Skipping tracked file", "path", tf, "err", err)
			continue
		}
		if len(types) == 0 {
			continue
		}
		var moduleName string
		if cache != nil {
			moduleName = cache.FileModuleName(tf)
		}
		files = append(files, analysis.FileThunkData{
			FileName:   filepath.Base(tf),
			AbsPath:    tf,
			Types:      types,
			Imports:    imports,
			ModuleName: moduleName,
		})
	}

	return files
}

// hasFile reports whether files contains an entry for the given absolute path.
func hasFile(files []analysis.FileThunkData, absPath string) bool {
	for _, f := range files {
		if f.AbsPath == absPath {
			return true
		}
	}
	return false
}

// parseAndFilterTrackedFiles parses tracked files and syncs the trackedFiles
// list to match. Files that were excluded (e.g. no types found) are removed
// from trackedFiles so that changes to them trigger a full rebuild via the
// untracked dependency path.
//
// Returns the filtered files, the synced trackedFiles list, and an error if
// the sourceFile could not be parsed.
func parseAndFilterTrackedFiles(sourceFile string, trackedFiles []string, cache *analysis.IndexStoreCache) (
	[]analysis.FileThunkData, []string, error,
) {
	files := parseTrackedFiles(sourceFile, trackedFiles, cache)
	if !hasFile(files, sourceFile) {
		return nil, nil, fmt.Errorf("no types found in %s", sourceFile)
	}

	// Sync trackedFiles with the files that survived parsing.
	keptPaths := make(map[string]bool, len(files))
	for _, f := range files {
		keptPaths[f.AbsPath] = true
	}
	var filtered []string
	for _, tf := range trackedFiles {
		if keptPaths[tf] {
			filtered = append(filtered, tf)
		}
	}
	return files, filtered, nil
}

// compilePipeline runs the parse → thunk → compile pipeline and returns the
// resulting dylib path.
//
// Contract:
//   - parseTrackedFiles may return empty results (nil). compilePipeline
//     treats this as an error because thunk generation requires at least one type.
//   - Callers that need different empty-result handling (e.g. rebuildAndRelaunch's
//     sourceFile-only fallback) should call parseTrackedFiles directly.
func compilePipeline(
	ctx context.Context,
	sourceFile string,
	trackedFiles []string,
	cache *analysis.IndexStoreCache,
	bs *build.Settings,
	dirs previewDirs,
	previewSelector string,
	counter int,
	tc ToolchainRunner,
) (string, error) {
	files := parseTrackedFiles(sourceFile, trackedFiles, cache)
	if len(files) == 0 {
		return "", fmt.Errorf("no types found in tracked files")
	}

	thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, dirs.Thunk, previewSelector, sourceFile, counter)
	if err != nil {
		return "", fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPaths, compileConfigFromSettings(bs), dirs.Thunk, dirs.Build, counter, sourceFile, tc)
	if err != nil {
		return "", fmt.Errorf("compile: %w", err)
	}

	return dylibPath, nil
}

// compileMainOnlyPipeline runs the lightweight "main-only" thunk pipeline.
// It extracts imports from the source file, generates a single main thunk
// (no per-file dynamic replacements), and compiles it into a dylib.
func compileMainOnlyPipeline(
	ctx context.Context,
	sourceFile string,
	bs *build.Settings,
	dirs previewDirs,
	previewSelector string,
	tc ToolchainRunner,
) (string, error) {
	imports, err := analysis.SourceImports(sourceFile)
	if err != nil {
		return "", fmt.Errorf("source imports: %w", err)
	}

	thunkPaths, err := codegen.GenerateMainOnlyThunk(bs.ModuleName, dirs.Thunk, sourceFile, previewSelector, imports)
	if err != nil {
		return "", fmt.Errorf("main-only thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPaths, compileConfigFromSettings(bs), dirs.Thunk, dirs.Build, 0, sourceFile, tc)
	if err != nil {
		return "", fmt.Errorf("main-only compile: %w", err)
	}

	return dylibPath, nil
}

// deploy attempts hot-reload via socket, falling back to full app relaunch.
func deploy(ctx context.Context, dylibPath string, dirs previewDirs, bs *build.Settings, wctx watchContext) error {
	if err := codegen.SendReloadCommand(ctx, dirs.Socket, dylibPath); err != nil {
		slog.Warn("Hot-reload failed, falling back to full relaunch", "err", err)
		terminateApp(ctx, bs, wctx.device, wctx.deviceSetPath, wctx.app)
		if err := launchWithHotReload(ctx, bs, wctx.loaderPath, dylibPath, dirs.Socket, wctx.device, wctx.deviceSetPath, wctx.app); err != nil {
			return fmt.Errorf("launch: %w", err)
		}
		fmt.Fprintln(os.Stderr, "Preview relaunched (full restart).")
		return nil
	}
	fmt.Fprintln(os.Stderr, "Preview hot-reloaded.")
	return nil
}

// updatePreviewCount re-parses #Preview blocks from sourceFile and updates
// the preview count/index in ws. Called before hot-reload to detect newly
// added or removed previews.
func updatePreviewCount(sourceFile string, ws *watchState) {
	blocks, err := analysis.PreviewBlocks(sourceFile)
	if err != nil || len(blocks) == 0 {
		return
	}
	ws.mu.Lock()
	ws.previewCount = len(blocks)
	if ws.previewIndex >= len(blocks) {
		ws.previewIndex = 0
		ws.previewSelector = "0"
	}
	ws.mu.Unlock()
}
