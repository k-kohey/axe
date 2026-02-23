package watch

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
)

// DirLister abstracts directory listing for Swift source discovery.
// The production implementation uses git ls-files for efficiency.
type DirLister interface {
	SwiftDirs(ctx context.Context, root string) ([]string, error)
}

// WalkSwiftDirs is the fallback for non-git projects. It walks the directory tree
// skipping hidden directories and common dependency/build artifact directories.
func WalkSwiftDirs(root string) ([]string, error) {
	seen := make(map[string]bool)
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "build" || name == "DerivedData" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".swift") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	return dirs, err
}
