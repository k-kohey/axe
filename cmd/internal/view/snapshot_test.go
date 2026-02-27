package view

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fakePNG returns a minimal valid PNG-header byte slice for testing.
func fakePNG() []byte {
	png := make([]byte, 0, 64)
	png = append(png, pngMagic...)
	png = append(png, []byte("fake-png-data-for-testing")...)
	return png
}

func TestValidatePNG(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid PNG", fakePNG(), true},
		{"not PNG", []byte("not-png-data"), false},
		{"empty", []byte{}, false},
		{"nil", nil, false},
		{"too short", []byte{0x89, 0x50}, false},
		{"almost PNG magic", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x00}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validatePNG(tt.data)
			if got != tt.want {
				t.Errorf("validatePNG() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSaveSnapshotToTemp(t *testing.T) {
	pngData := fakePNG()
	address := "0x12345678"

	path, err := saveSnapshotToTemp(pngData, address)
	if err != nil {
		t.Fatalf("saveSnapshotToTemp failed: %v", err)
	}
	defer func() { _ = os.Remove(path) }()

	// Verify the file path.
	expectedName := "axe_snapshot_0x12345678.png"
	if filepath.Base(path) != expectedName {
		t.Errorf("expected filename %s, got %s", expectedName, filepath.Base(path))
	}

	// Verify the file contents.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read snapshot file: %v", err)
	}
	if !bytes.Equal(data, pngData) {
		t.Errorf("file contents mismatch: got %d bytes, want %d bytes", len(data), len(pngData))
	}
}

func TestSaveSnapshotToTemp_AddressValidation(t *testing.T) {
	pngData := fakePNG()

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"valid hex address", "0x12345678", false},
		{"valid uppercase hex", "0xABCDEF", false},
		{"valid mixed case", "0xaBcDeF01", false},
		{"missing 0x prefix", "12345678", true},
		{"injection attempt", "0x1234; rm -rf /", true},
		{"path traversal", "0x1234/../../../etc/passwd", true},
		{"empty string", "", true},
		{"only 0x", "0x", true},
		{"non-hex characters", "0xGHIJKL", true},
		{"spaces", "0x1234 5678", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := saveSnapshotToTemp(pngData, tt.address)
			if (err != nil) != tt.wantErr {
				t.Errorf("saveSnapshotToTemp(%q) error = %v, wantErr %v", tt.address, err, tt.wantErr)
			}
		})
	}
}
