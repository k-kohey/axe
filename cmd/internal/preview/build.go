package preview

import (
	"context"

	"github.com/k-kohey/axe/internal/preview/build"
)

// fetchBuildSettings delegates to build.FetchSettings.
func fetchBuildSettings(ctx context.Context, pc ProjectConfig, dirs previewDirs, br build.Runner) (*build.Settings, error) {
	return build.FetchSettings(ctx, pc, dirs.ProjectDirs, br)
}

// buildProject delegates to build.Run.
func buildProject(ctx context.Context, pc ProjectConfig, dirs previewDirs, br build.Runner) error {
	return build.Run(ctx, pc, dirs.ProjectDirs, br)
}

// extractCompilerPaths delegates to build.ExtractCompilerPaths.
func extractCompilerPaths(ctx context.Context, s *build.Settings, dirs previewDirs) {
	build.ExtractCompilerPaths(ctx, s, dirs.ProjectDirs)
}

// hasPreviousBuild delegates to build.HasPreviousBuild.
func hasPreviousBuild(s *build.Settings, dirs previewDirs) bool {
	return build.HasPreviousBuild(s, dirs.ProjectDirs)
}
