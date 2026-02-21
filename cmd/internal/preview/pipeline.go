package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// parseTrackedFiles parses all tracked files and builds fileThunkData slices.
// sourceFile is treated specially: parseSourceFile is used instead of parseDependencyFile.
// All parse errors (including sourceFile) are skipped with a debug log (lenient mode).
// This is intentional: hot-reload triggers while the user is editing, so syntax errors
// in the source file are expected and should not be fatal.
// Callers that need stricter behavior (Run, switchFile) should check the result
// for sourceFile presence after calling this function.
func parseTrackedFiles(sourceFile string, trackedFiles []string) []fileThunkData {
	var files []fileThunkData
	for _, tf := range trackedFiles {
		var types []typeInfo
		var imports []string
		var err error
		if tf == sourceFile {
			types, imports, err = parseSourceFile(tf)
		} else {
			types, imports, err = parseDependencyFile(tf)
		}
		if err != nil {
			slog.Debug("Skipping tracked file", "path", tf, "err", err)
			continue
		}
		if len(types) == 0 {
			continue
		}
		files = append(files, fileThunkData{
			FileName: filepath.Base(tf),
			AbsPath:  tf,
			Types:    types,
			Imports:  imports,
		})
	}
	return files
}

// hasFile reports whether files contains an entry for the given absolute path.
func hasFile(files []fileThunkData, absPath string) bool {
	for _, f := range files {
		if f.AbsPath == absPath {
			return true
		}
	}
	return false
}

// parseAndFilterTrackedFiles parses tracked files and removes private type
// name collisions. Used by Run() and switchFile() where collision filtering
// is needed. Returns the filtered files, the filtered trackedFiles list, and
// an error if the sourceFile could not be parsed.
func parseAndFilterTrackedFiles(sourceFile string, trackedFiles []string) (
	[]fileThunkData, []string, error,
) {
	files := parseTrackedFiles(sourceFile, trackedFiles)
	if !hasFile(files, sourceFile) {
		return nil, nil, fmt.Errorf("no types found in %s", sourceFile)
	}

	files, excludedPaths := filterPrivateCollisions(files, sourceFile)
	if len(excludedPaths) > 0 {
		excludeSet := make(map[string]bool, len(excludedPaths))
		for _, p := range excludedPaths {
			excludeSet[p] = true
		}
		var filtered []string
		for _, tf := range trackedFiles {
			if !excludeSet[tf] {
				filtered = append(filtered, tf)
			}
		}
		trackedFiles = filtered
	}
	return files, trackedFiles, nil
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
	bs *buildSettings,
	dirs previewDirs,
	previewSelector string,
	counter int,
) (string, error) {
	files := parseTrackedFiles(sourceFile, trackedFiles)
	if len(files) == 0 {
		return "", fmt.Errorf("no types found in tracked files")
	}

	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, dirs, previewSelector, sourceFile)
	if err != nil {
		return "", fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := compileThunk(ctx, thunkPath, bs, dirs, counter, sourceFile)
	if err != nil {
		return "", fmt.Errorf("compile: %w", err)
	}

	return dylibPath, nil
}

// deploy attempts hot-reload via socket, falling back to full app relaunch.
func deploy(dylibPath string, dirs previewDirs, bs *buildSettings, wctx watchContext) error {
	if err := sendReloadCommand(dirs.Socket, dylibPath); err != nil {
		slog.Warn("Hot-reload failed, falling back to full relaunch", "err", err)
		terminateApp(bs, wctx.device, wctx.deviceSetPath)
		if err := launchWithHotReload(bs, wctx.loaderPath, dylibPath, dirs.Socket, wctx.device, wctx.deviceSetPath); err != nil {
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
	blocks, err := parsePreviewBlocks(sourceFile)
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
