package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/codegen"
	"github.com/k-kohey/axe/internal/preview/parsing"
)

// parseTrackedFiles parses all tracked files and builds fileThunkData slices.
// sourceFile is treated specially: parseSourceFile is used instead of parseDependencyFile.
// All parse errors (including sourceFile) are skipped with a debug log (lenient mode).
// This is intentional: hot-reload triggers while the user is editing, so syntax errors
// in the source file are expected and should not be fatal.
// Callers that need stricter behavior (Run, switchFile) should check the result
// for sourceFile presence after calling this function.
func parseTrackedFiles(sourceFile string, trackedFiles []string) []parsing.FileThunkData {
	var files []parsing.FileThunkData
	for _, tf := range trackedFiles {
		var types []parsing.TypeInfo
		var imports []string
		var err error
		if tf == sourceFile {
			types, imports, err = parsing.SourceFile(tf)
		} else {
			types, imports, err = parsing.DependencyFile(tf)
		}
		if err != nil {
			slog.Debug("Skipping tracked file", "path", tf, "err", err)
			continue
		}
		if len(types) == 0 {
			continue
		}
		files = append(files, parsing.FileThunkData{
			FileName: filepath.Base(tf),
			AbsPath:  tf,
			Types:    types,
			Imports:  imports,
		})
	}
	return files
}

// hasFile reports whether files contains an entry for the given absolute path.
func hasFile(files []parsing.FileThunkData, absPath string) bool {
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
	[]parsing.FileThunkData, []string, error,
) {
	files := parseTrackedFiles(sourceFile, trackedFiles)
	if !hasFile(files, sourceFile) {
		return nil, nil, fmt.Errorf("no types found in %s", sourceFile)
	}

	files, excludedPaths := parsing.FilterPrivateCollisions(files, sourceFile)
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
	tc ToolchainRunner,
) (string, error) {
	files := parseTrackedFiles(sourceFile, trackedFiles)
	if len(files) == 0 {
		return "", fmt.Errorf("no types found in tracked files")
	}

	thunkPath, err := codegen.GenerateCombinedThunk(files, bs.ModuleName, dirs.Thunk, previewSelector, sourceFile)
	if err != nil {
		return "", fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := codegen.CompileThunk(ctx, thunkPath, compileConfigFromBS(bs), dirs.Thunk, dirs.Build, counter, sourceFile, tc)
	if err != nil {
		return "", fmt.Errorf("compile: %w", err)
	}

	return dylibPath, nil
}

// deploy attempts hot-reload via socket, falling back to full app relaunch.
func deploy(ctx context.Context, dylibPath string, dirs previewDirs, bs *buildSettings, wctx watchContext) error {
	if err := codegen.SendReloadCommand(dirs.Socket, dylibPath); err != nil {
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
	blocks, err := parsing.PreviewBlocks(sourceFile)
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
