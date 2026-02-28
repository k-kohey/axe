package preview

import (
	"errors"
	"testing"

	"github.com/k-kohey/axe/internal/preview/analysis"
)

func TestEffectiveConcurrency(t *testing.T) {
	tests := []struct {
		name       string
		totalFiles int
		requested  int
		want       int
	}{
		{"auto with 1 file", 1, 0, 1},
		{"auto with 2 files", 2, 0, 2},
		{"auto with 4 files", 4, 0, 4},
		{"auto with 10 files caps at 4", 10, 0, 4},
		{"explicit 1", 5, 1, 1},
		{"explicit 2", 5, 2, 2},
		{"explicit exceeds files", 2, 8, 2},
		{"explicit 10 caps at files", 3, 10, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveConcurrency(tt.totalFiles, tt.requested)
			if got != tt.want {
				t.Errorf("effectiveConcurrency(%d, %d) = %d, want %d",
					tt.totalFiles, tt.requested, got, tt.want)
			}
		})
	}
}

func TestAllFailures(t *testing.T) {
	blocks := []fileBlocks{
		{
			file: "/a/Foo.swift",
			previews: []analysis.PreviewBlock{
				{Title: "P0", StartLine: 10},
				{Title: "P1", StartLine: 20},
			},
		},
		{
			file: "/b/Bar.swift",
			previews: []analysis.PreviewBlock{
				{Title: "B0", StartLine: 5},
			},
		},
	}

	err := &testError{"setup failed"}
	result := allFailures(blocks, err)

	if len(result.captures) != 0 {
		t.Fatalf("expected 0 captures, got %d", len(result.captures))
	}
	if len(result.failures) != 3 {
		t.Fatalf("expected 3 failures, got %d", len(result.failures))
	}

	// Verify order: Foo P0, Foo P1, Bar B0
	want := []struct {
		file  string
		index int
		title string
	}{
		{"/a/Foo.swift", 0, "P0"},
		{"/a/Foo.swift", 1, "P1"},
		{"/b/Bar.swift", 0, "B0"},
	}
	for i, f := range result.failures {
		if f.file != want[i].file || f.index != want[i].index || f.title != want[i].title {
			t.Errorf("failure[%d] = {%s, %d, %s}, want {%s, %d, %s}",
				i, f.file, f.index, f.title, want[i].file, want[i].index, want[i].title)
		}
		if !errors.Is(f.err, err) {
			t.Errorf("failure[%d].err = %v, want %v", i, f.err, err)
		}
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
