package view

import (
	"os"
	"strings"
	"testing"
)

func TestExtractJSONError(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "error response",
			data: `{"error":"The data couldn't be written because it isn't in the correct format."}`,
			want: "The data couldn't be written because it isn't in the correct format.",
		},
		{
			name: "valid node array",
			data: `[{"type":"SwiftUI.Text","children":[]}]`,
			want: "",
		},
		{
			name: "empty array",
			data: `[]`,
			want: "",
		},
		{
			name: "dict without error key",
			data: `{"status":"ok"}`,
			want: "",
		},
		{
			name: "invalid JSON",
			data: `not json`,
			want: "",
		},
		{
			name: "error value is not string",
			data: `{"error":123}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONError([]byte(tt.data))
			if got != tt.want {
				t.Errorf("extractJSONError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSnapshot(t *testing.T) {
	t.Run("valid PNG saves to temp file", func(t *testing.T) {
		node := rawViewNode{
			Address:   "0xABC123",
			ImageData: fakePNG(),
		}

		path := extractSnapshot(node)
		if path == "" {
			t.Fatal("expected non-empty path for valid PNG")
		}
		defer func() { _ = os.Remove(path) }()

		if !strings.Contains(path, "axe_snapshot_0xABC123.png") {
			t.Errorf("unexpected path: %s", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read snapshot: %v", err)
		}
		if !validatePNG(data) {
			t.Error("saved file is not valid PNG")
		}
	})

	t.Run("empty imageData returns empty string", func(t *testing.T) {
		node := rawViewNode{
			Address:   "0x100",
			ImageData: nil,
		}

		path := extractSnapshot(node)
		if path != "" {
			t.Errorf("expected empty path, got %q", path)
		}
	})

	t.Run("invalid PNG returns empty string", func(t *testing.T) {
		node := rawViewNode{
			Address:   "0x100",
			ImageData: []byte("not-a-png"),
		}

		path := extractSnapshot(node)
		if path != "" {
			t.Errorf("expected empty path for invalid PNG, got %q", path)
		}
	})
}

func TestBuildDetailWithSnapshot(t *testing.T) {
	classmap := map[string]string{
		"UIView": "UIView/UIResponder/NSObject",
	}

	t.Run("basic fields populated", func(t *testing.T) {
		hidden := false
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
			Frame:   []float64{0, 0, 320, 480},
			Hidden:  &hidden,
			Subviews: []rawViewNode{
				{Class: "UILabel", Address: "0x200"},
			},
		}

		detail := buildDetailWithSnapshot(node, classmap, nil)

		if detail.Class != "UIView" {
			t.Errorf("expected UIView, got %s", detail.Class)
		}
		if detail.Address != "0x100" {
			t.Errorf("expected 0x100, got %s", detail.Address)
		}
		if detail.Inheritance != "UIView/UIResponder/NSObject" {
			t.Errorf("expected inheritance, got %q", detail.Inheritance)
		}
		if detail.Frame == nil || detail.Frame.Width != 320 {
			t.Error("expected frame with width 320")
		}
		if detail.SubviewCount == nil || *detail.SubviewCount != 1 {
			t.Error("expected subviewCount=1")
		}
	})

	t.Run("with valid PNG sets snapshot", func(t *testing.T) {
		node := rawViewNode{
			Class:     "UIView",
			Address:   "0x100",
			ImageData: fakePNG(),
		}

		detail := buildDetailWithSnapshot(node, classmap, nil)

		if detail.Snapshot == "" {
			t.Fatal("expected snapshot path to be set")
		}
		defer func() { _ = os.Remove(detail.Snapshot) }()

		if !strings.Contains(detail.Snapshot, "axe_snapshot_0x100.png") {
			t.Errorf("unexpected snapshot path: %s", detail.Snapshot)
		}
	})

	t.Run("without imageData has empty snapshot", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
		}

		detail := buildDetailWithSnapshot(node, classmap, nil)

		if detail.Snapshot != "" {
			t.Errorf("expected empty snapshot, got %q", detail.Snapshot)
		}
	})
}
