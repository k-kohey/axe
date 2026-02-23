package codegen

import (
	"log/slog"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

// ParseTrackedFiles parses all tracked files and builds FileThunkData slices.
// sourceFile is treated specially: analysis.SourceFile is used instead of
// analysis.DependencyFile.
// All parse errors (including sourceFile) are skipped with a debug log (lenient mode).
// This is intentional: hot-reload triggers while the user is editing, so syntax errors
// in the source file are expected and should not be fatal.
func ParseTrackedFiles(sourceFile string, trackedFiles []string) []analysis.FileThunkData {
	var files []analysis.FileThunkData
	for _, tf := range trackedFiles {
		var types []analysis.TypeInfo
		var imports []string
		var err error
		if tf == sourceFile {
			types, imports, err = analysis.SourceFile(tf)
		} else {
			types, imports, err = analysis.DependencyFile(tf)
		}
		if err != nil {
			slog.Debug("Skipping tracked file", "path", tf, "err", err)
			continue
		}
		if len(types) == 0 {
			continue
		}
		files = append(files, analysis.FileThunkData{
			FileName: filepath.Base(tf),
			AbsPath:  tf,
			Types:    types,
			Imports:  imports,
		})
	}
	return files
}

// HasFile reports whether files contains an entry for the given absolute path.
func HasFile(files []analysis.FileThunkData, absPath string) bool {
	for _, f := range files {
		if f.AbsPath == absPath {
			return true
		}
	}
	return false
}
