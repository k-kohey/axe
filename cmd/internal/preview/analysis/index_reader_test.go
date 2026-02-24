package analysis

import (
	"os"
	"testing"
)

func TestXcodeToolchainLibPath(t *testing.T) {
	libPath := xcodeToolchainLibPath()
	if libPath == "" {
		t.Skip("xcode-select not available; skipping")
	}

	if _, err := os.Stat(libPath); os.IsNotExist(err) {
		t.Errorf("xcodeToolchainLibPath() = %q, but directory does not exist", libPath)
	}
}
