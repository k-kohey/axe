package preview

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// --- cleanOldDylibs tests ---

func TestCleanOldDylibs_RemovesOldArtifacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create thunk_0 through thunk_2 (.dylib and .o).
	for i := range 3 {
		for _, ext := range []string{".dylib", ".o"} {
			p := filepath.Join(dir, fmt.Sprintf("thunk_%d%s", i, ext))
			if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Clean artifacts with index < 2 (i.e., thunk_0 and thunk_1).
	cleanOldDylibs(dir, 2)

	// thunk_0 and thunk_1 should be removed.
	for i := range 2 {
		for _, ext := range []string{".dylib", ".o"} {
			p := filepath.Join(dir, fmt.Sprintf("thunk_%d%s", i, ext))
			if _, err := os.Stat(p); err == nil {
				t.Errorf("expected %s to be removed", p)
			}
		}
	}

	// thunk_2 should still exist.
	for _, ext := range []string{".dylib", ".o"} {
		p := filepath.Join(dir, fmt.Sprintf("thunk_%d%s", 2, ext))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to still exist", p)
		}
	}
}

func TestCleanOldDylibs_NoopWhenNoFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Should not panic on empty directory.
	cleanOldDylibs(dir, 5)
}

func TestCleanOldDylibs_KeepAfterZero(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	p := filepath.Join(dir, "thunk_0.dylib")
	if err := os.WriteFile(p, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	// keepAfter=0 means range(0) = nothing to clean.
	cleanOldDylibs(dir, 0)

	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected %s to still exist with keepAfter=0", p)
	}
}
