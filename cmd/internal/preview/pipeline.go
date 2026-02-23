package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/codegen"
)

// parseAndFilterTrackedFiles parses tracked files and removes private type
// name collisions. Used by Run() and switchFile() where collision filtering
// is needed. Returns the filtered files, the filtered trackedFiles list, and
// an error if the sourceFile could not be parsed.
func parseAndFilterTrackedFiles(sourceFile string, trackedFiles []string) (
	[]analysis.FileThunkData, []string, error,
) {
	files := codegen.ParseTrackedFiles(sourceFile, trackedFiles)
	if !codegen.HasFile(files, sourceFile) {
		return nil, nil, fmt.Errorf("no types found in %s", sourceFile)
	}

	files, excludedPaths := analysis.FilterPrivateCollisions(files, sourceFile)
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
