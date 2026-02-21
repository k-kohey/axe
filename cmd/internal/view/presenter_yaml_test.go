package view

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPresentTreeYAML(t *testing.T) {
	tree := TreeOutput{
		Views: []UIKitView{
			{
				Class:   "UIWindow",
				Address: "0x100",
				Frame:   &Rect{X: 0, Y: 0, Width: 402, Height: 874},
				Subviews: []UIKitView{
					{
						Class:         "UIView",
						Address:       "0x200",
						Frame:         &Rect{X: 0, Y: 0, Width: 402, Height: 874},
						IsHostingView: true,
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	err := PresentTreeYAML(&buf, tree)
	if err != nil {
		t.Fatalf("PresentTreeYAML failed: %v", err)
	}

	// Verify it round-trips
	var result TreeOutput
	if err := yaml.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if len(result.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(result.Views))
	}
	if result.Views[0].Class != "UIWindow" {
		t.Errorf("expected UIWindow, got %s", result.Views[0].Class)
	}
	if len(result.Views[0].Subviews) != 1 {
		t.Fatalf("expected 1 subview, got %d", len(result.Views[0].Subviews))
	}
	if !result.Views[0].Subviews[0].IsHostingView {
		t.Error("expected isHostingView=true")
	}
}

func TestPresentDetailYAML(t *testing.T) {
	hidden := false
	subviewCount := 1

	detail := DetailOutput{
		UIKit: UIKitView{
			Class:         "_UIHostingView<MainContentView>",
			Address:       "0x101518a00",
			Inheritance:   "_UIHostingView/UIView/UIResponder",
			Frame:         &Rect{X: 0, Y: 0, Width: 402, Height: 874},
			SubviewCount:  &subviewCount,
			Hidden:        &hidden,
			IsHostingView: true,
		},
		SwiftUI: &SwiftUIOutput{
			Tree: []SwiftUINode{
				{
					Name: "MainContentView",
					Size: &Size{Width: 402, Height: 874},
				},
			},
		},
	}

	var buf bytes.Buffer
	err := PresentDetailYAML(&buf, detail)
	if err != nil {
		t.Fatalf("PresentDetailYAML failed: %v", err)
	}

	// Verify round-trip
	var result DetailOutput
	if err := yaml.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if result.UIKit.Class != "_UIHostingView<MainContentView>" {
		t.Errorf("expected class, got %s", result.UIKit.Class)
	}
	if result.SwiftUI == nil {
		t.Fatal("expected SwiftUI output")
	}
	if len(result.SwiftUI.Tree) != 1 {
		t.Fatalf("expected 1 swiftui root, got %d", len(result.SwiftUI.Tree))
	}
}

func TestPresentDetailYAMLWithoutSwiftUI(t *testing.T) {
	detail := DetailOutput{
		UIKit: UIKitView{
			Class:   "UIView",
			Address: "0x100",
		},
	}

	var buf bytes.Buffer
	err := PresentDetailYAML(&buf, detail)
	if err != nil {
		t.Fatalf("PresentDetailYAML failed: %v", err)
	}

	// Should not contain swiftui key
	var raw map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if _, ok := raw["swiftui"]; ok {
		t.Error("expected no swiftui key when SwiftUI is nil")
	}
}
