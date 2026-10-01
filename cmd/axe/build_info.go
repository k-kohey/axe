package main

import (
	"github.com/k-kohey/axe/internal/preview"
	"github.com/k-kohey/axe/internal/preview/build"
)

// resolveBuildInfo runs BUILD_INFO_COMMAND (from .axerc) for sourceFile when no
// --build-info file was given, stores its JSON output and points pc at it.
//
// The template placeholders are {file} (absolute source path) and {device}
// (the --device value, or "generic"). The command stays in pc so that watch mode
// can run it again to rebuild.
func resolveBuildInfo(pc preview.ProjectConfig, sourceFile string) (preview.ProjectConfig, error) {
	if pc.BuildInfoPath != "" || pc.BuildInfoCommand == "" {
		return pc, nil
	}
	path, err := build.RunBuildInfoCommand(pc, sourceFile, previewDevice)
	if err != nil {
		return pc, err
	}
	pc.BuildInfoPath = path
	pc.BuildInfoDevice = previewDevice
	pc.BuildInfoSource = sourceFile
	return pc, nil
}
