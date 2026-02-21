package platform

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

//go:embed lldb_commands/*.py
var lldbCommandsFS embed.FS

// ExtractScripts extracts embedded LLDB Python scripts to a temporary directory.
// Returns the path to the directory, a cleanup function, and any error.
func ExtractScripts() (pythonDir string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "axe-lldb-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	removeDir := func() {
		if err := os.RemoveAll(dir); err != nil {
			slog.Debug("Failed to remove temp dir", "dir", dir, "err", err)
		}
	}

	entries, err := lldbCommandsFS.ReadDir("lldb_commands")
	if err != nil {
		removeDir()
		return "", nil, fmt.Errorf("failed to read embedded scripts: %w", err)
	}

	for _, entry := range entries {
		data, err := lldbCommandsFS.ReadFile("lldb_commands/" + entry.Name())
		if err != nil {
			removeDir()
			return "", nil, fmt.Errorf("failed to read embedded file %s: %w", entry.Name(), err)
		}
		dst := filepath.Join(dir, entry.Name())
		if err := os.WriteFile(dst, data, 0644); err != nil {
			removeDir()
			return "", nil, fmt.Errorf("failed to write file %s: %w", dst, err)
		}
	}

	cleanup = removeDir
	return dir, cleanup, nil
}
