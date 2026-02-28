package preview

import (
	"context"

	"github.com/k-kohey/axe/internal/preview/build"
)

// buildProject delegates to build.Run.
// Used by hot_reload.go for incremental rebuilds during watch mode.
func buildProject(ctx context.Context, pc ProjectConfig, dirs previewDirs, br build.Runner) error {
	return build.Run(ctx, pc, dirs.ProjectDirs, br)
}
