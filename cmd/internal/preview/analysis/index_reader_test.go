package analysis

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

func TestIndexReaderCommandRunsInSourceRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := indexReaderCommand(context.Background(), "/bin/reader", "/idx", root)
	if cmd.Dir != root {
		t.Errorf("Dir = %q, want %q", cmd.Dir, root)
	}
	if want := []string{"/bin/reader", "/idx", "--source-root", root}; !slices.Equal(cmd.Args, want) {
		t.Errorf("Args = %v, want %v", cmd.Args, want)
	}

	cmd = indexReaderCommand(context.Background(), "/bin/reader", "/idx", "")
	if cmd.Dir != "" {
		t.Errorf("Dir without sourceRoot = %q, want empty", cmd.Dir)
	}
	if want := []string{"/bin/reader", "/idx"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("Args without sourceRoot = %v, want %v", cmd.Args, want)
	}
}
