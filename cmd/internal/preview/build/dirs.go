package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectDirs holds device-independent directory paths for build artifacts.
// These are derived solely from the project path and shared across all
// simulator devices working with the same project.
type ProjectDirs struct {
	Root  string // ~/.cache/axe/preview-<project-hash>
	Build string // Root/build (shared xcodebuild output)
}

// IndexStorePath returns the path to the Xcode index store data directory.
func (d ProjectDirs) IndexStorePath() string {
	return filepath.Join(d.Build, "Index.noindex", "DataStore")
}

// NewProjectDirs creates a ProjectDirs from a project/workspace path.
// Uses ~/Library/Caches/axe/ so that dylibs are accessible from within
// the iOS Simulator via dlopen (separated runtimes cannot resolve host
// /tmp paths).
func NewProjectDirs(projectPath string) (ProjectDirs, error) {
	abs, _ := filepath.Abs(projectPath)
	h := sha256.Sum256([]byte(abs))
	short := fmt.Sprintf("%x", h[:8])

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	}
	root := filepath.Join(cacheDir, "axe", "preview-"+short)

	return ProjectDirs{
		Root:  root,
		Build: filepath.Join(root, "build"),
	}, nil
}
