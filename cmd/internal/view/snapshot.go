package view

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// pngMagic is the first 8 bytes of any valid PNG file.
var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// validatePNG checks that data starts with PNG magic bytes.
func validatePNG(data []byte) bool {
	return len(data) >= len(pngMagic) && bytes.HasPrefix(data, pngMagic)
}

// saveSnapshotToTemp writes PNG data to a temp file and returns the path.
func saveSnapshotToTemp(pngData []byte, address string) (string, error) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("axe_snapshot_%s.png", address))
	if err := os.WriteFile(path, pngData, 0644); err != nil {
		return "", fmt.Errorf("failed to write snapshot: %w", err)
	}
	return path, nil
}
