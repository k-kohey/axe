package view

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTreeOutputYAMLMarshal(t *testing.T) {
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

	yamlBytes, err := yaml.Marshal(tree)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	// Verify it round-trips
	var result TreeOutput
	if err := yaml.Unmarshal(yamlBytes, &result); err != nil {
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

func TestDetailOutputYAMLMarshal(t *testing.T) {
	hidden := false
	ambiguous := false
	subviewCount := 1

	detail := DetailOutput{
		UIKit: UIKitView{
			Class:              "_UIHostingView<MainContentView>",
			Address:            "0x101518a00",
			Inheritance:        "_UIHostingView/UIView/UIResponder",
			Frame:              &Rect{X: 0, Y: 0, Width: 402, Height: 874},
			Bounds:             &Rect{X: 0, Y: 0, Width: 402, Height: 874},
			Position:           &Point{X: 201, Y: 437},
			Hidden:             &hidden,
			LayoutMargins:      &Insets{Top: 0, Left: 16, Bottom: 0, Right: 16},
			HasAmbiguousLayout: &ambiguous,
			Layer:              &LayerInfo{Class: "CALayer", Address: "0x600012345"},
			Constraints:        []Constraint{},
			SubviewCount:       &subviewCount,
			IsHostingView:      true,
		},
		SwiftUI: &SwiftUIOutput{
			Tree: []SwiftUINode{
				{
					Name: "MainContentView",
					Size: &Size{Width: 402, Height: 874},
					Children: []SwiftUINode{
						{
							Name: "Text",
							Size: &Size{Width: 100, Height: 20},
						},
					},
				},
			},
		},
	}

	yamlBytes, err := yaml.Marshal(detail)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	// Verify round-trip
	var result DetailOutput
	if err := yaml.Unmarshal(yamlBytes, &result); err != nil {
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
	if result.SwiftUI.Tree[0].Name != "MainContentView" {
		t.Errorf("expected MainContentView, got %s", result.SwiftUI.Tree[0].Name)
	}
}

func TestDetailOutputWithoutSwiftUI(t *testing.T) {
	detail := DetailOutput{
		UIKit: UIKitView{
			Class:   "UIView",
			Address: "0x100",
		},
	}

	yamlBytes, err := yaml.Marshal(detail)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	// Should not contain swiftui key
	var raw map[string]any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if _, ok := raw["swiftui"]; ok {
		t.Error("expected no swiftui key when SwiftUI is nil")
	}
}

func TestUIKitViewJSONUnmarshal(t *testing.T) {
	input := `{
		"class": "UIView",
		"address": "0x100",
		"inheritance": "UIView/UIResponder",
		"frame": {"x": 0, "y": 0, "width": 402, "height": 874},
		"bounds": {"x": 0, "y": 0, "width": 402, "height": 874},
		"position": {"x": 201, "y": 437},
		"hidden": false,
		"layoutMargins": {"top": 0, "left": 16, "bottom": 0, "right": 16},
		"hasAmbiguousLayout": false,
		"layer": {"class": "CALayer", "address": "0x600"},
		"constraints": [
			{
				"firstItem": "0x100",
				"firstAttribute": "width",
				"relation": "==",
				"secondItem": "0x200",
				"secondAttribute": "width",
				"multiplier": 1.0,
				"constant": 0.0,
				"priority": 1000.0
			}
		],
		"subviewCount": 1,
		"isHostingView": true
	}`

	var view UIKitView
	if err := json.Unmarshal([]byte(input), &view); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if view.Class != "UIView" {
		t.Errorf("expected UIView, got %s", view.Class)
	}
	if !view.IsHostingView {
		t.Error("expected isHostingView=true")
	}
	if view.Frame == nil || view.Frame.Width != 402 {
		t.Error("expected frame width 402")
	}
	if len(view.Constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(view.Constraints))
	}
	if view.Constraints[0].FirstAttribute != "width" {
		t.Errorf("expected firstAttribute=width, got %s", view.Constraints[0].FirstAttribute)
	}
}
