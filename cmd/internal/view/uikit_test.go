package view

import (
	"strings"
	"testing"

	"howett.net/plist"
)

// helper: build a plist dict representing a view node and marshal to binary plist.
func mustMarshalBplistMap(t *testing.T, data map[string]interface{}) []byte {
	t.Helper()
	b, err := plist.Marshal(data, plist.BinaryFormat)
	if err != nil {
		t.Fatalf("plist.Marshal failed: %v", err)
	}
	return b
}

// helper: build a simple bplist map with views and classmap.
func makeBplistMap(views []map[string]interface{}, classmap map[string]string) map[string]interface{} {
	cm := make(map[string]interface{}, len(classmap))
	for k, v := range classmap {
		cm[k] = v
	}
	vs := make([]interface{}, len(views))
	for i, v := range views {
		vs[i] = v
	}
	return map[string]interface{}{
		"views":    vs,
		"classmap": cm,
	}
}

func TestParseBplist(t *testing.T) {
	original := makeBplistMap(
		[]map[string]interface{}{
			{
				"class":   "UIWindow",
				"address": "0x100",
				"frame":   []interface{}{0.0, 0.0, 402.0, 874.0},
			},
		},
		map[string]string{
			"UIWindow": "UIWindow/UIView/UIResponder",
		},
	)

	data := mustMarshalBplistMap(t, original)
	result, err := parseBplist(data)
	if err != nil {
		t.Fatalf("parseBplist failed: %v", err)
	}

	if len(result.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(result.Views))
	}
	if result.Views[0].Class != "UIWindow" {
		t.Errorf("expected UIWindow, got %s", result.Views[0].Class)
	}
	if result.Views[0].Address != "0x100" {
		t.Errorf("expected 0x100, got %s", result.Views[0].Address)
	}
	if result.Classmap["UIWindow"] != "UIWindow/UIView/UIResponder" {
		t.Errorf("unexpected classmap: %v", result.Classmap)
	}
}

func TestParseBplistInvalid(t *testing.T) {
	_, err := parseBplist([]byte("not a plist"))
	if err == nil {
		t.Fatal("expected error for invalid plist data")
	}
}

func TestBuildRect(t *testing.T) {
	tests := []struct {
		name    string
		input   []float64
		wantNil bool
		wantX   float64
	}{
		{"valid", []float64{1, 2, 3, 4}, false, 1},
		{"short", []float64{1, 2}, true, 0},
		{"nil", nil, true, 0},
		{"empty", []float64{}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRect(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.X != tt.wantX {
				t.Errorf("expected X=%v, got %v", tt.wantX, got.X)
			}
			if got.Width != 3 || got.Height != 4 {
				t.Errorf("expected 3x4, got %vx%v", got.Width, got.Height)
			}
		})
	}
}

func TestBuildPoint(t *testing.T) {
	tests := []struct {
		name    string
		input   []float64
		wantNil bool
		wantX   float64
		wantY   float64
	}{
		{"valid", []float64{10, 20}, false, 10, 20},
		{"short", []float64{10}, true, 0, 0},
		{"nil", nil, true, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPoint(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.X != tt.wantX || got.Y != tt.wantY {
				t.Errorf("expected (%v,%v), got (%v,%v)", tt.wantX, tt.wantY, got.X, got.Y)
			}
		})
	}
}

func TestBuildInsets(t *testing.T) {
	tests := []struct {
		name    string
		input   []float64
		wantNil bool
	}{
		{"valid", []float64{1, 2, 3, 4}, false},
		{"short", []float64{1, 2}, true},
		{"nil", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildInsets(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.Top != 1 || got.Left != 2 || got.Bottom != 3 || got.Right != 4 {
				t.Errorf("unexpected insets: %+v", got)
			}
		})
	}
}

func TestBuildConstraint(t *testing.T) {
	tests := []struct {
		name     string
		input    rawConstraint
		wantAttr string
		wantRel  string
	}{
		{
			name: "known attributes",
			input: rawConstraint{
				Class:           "NSLayoutConstraint",
				Address:         "0x500",
				FirstItem:       "0x100",
				FirstAttribute:  7, // width
				Relation:        0, // ==
				SecondItem:      "0x200",
				SecondAttribute: 8, // height
				Multiplier:      1.0,
				Constant:        0.0,
				Priority:        1000.0,
			},
			wantAttr: "width",
			wantRel:  "==",
		},
		{
			name: "unknown attribute becomes numeric string",
			input: rawConstraint{
				FirstAttribute:  99,
				SecondAttribute: 100,
				Relation:        1, // >=
			},
			wantAttr: "99",
			wantRel:  ">=",
		},
		{
			name: "less-than-or-equal relation",
			input: rawConstraint{
				Relation: -1,
			},
			wantAttr: "notAnAttribute",
			wantRel:  "<=",
		},
		{
			name:     "defaults for empty fields",
			input:    rawConstraint{},
			wantAttr: "notAnAttribute",
			wantRel:  "==",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildConstraint(tt.input)
			if got.FirstAttribute != tt.wantAttr {
				t.Errorf("firstAttribute: expected %q, got %q", tt.wantAttr, got.FirstAttribute)
			}
			if got.Relation != tt.wantRel {
				t.Errorf("relation: expected %q, got %q", tt.wantRel, got.Relation)
			}
		})
	}

	// Test default values for empty class/address/items
	t.Run("default values", func(t *testing.T) {
		got := buildConstraint(rawConstraint{})
		if got.Class != "NSLayoutConstraint" {
			t.Errorf("expected default class, got %q", got.Class)
		}
		if got.Address != "?" {
			t.Errorf("expected default address '?', got %q", got.Address)
		}
		if got.FirstItem != "?" {
			t.Errorf("expected default firstItem '?', got %q", got.FirstItem)
		}
		if got.SecondItem != "?" {
			t.Errorf("expected default secondItem '?', got %q", got.SecondItem)
		}
	})
}

func TestIsHostingView(t *testing.T) {
	tests := []struct {
		name     string
		node     rawViewNode
		classmap map[string]string
		want     bool
	}{
		{
			name:     "class name contains HostingView",
			node:     rawViewNode{Class: "_UIHostingView<ContentView>"},
			classmap: map[string]string{},
			want:     true,
		},
		{
			name: "classmap path contains HostingView",
			node: rawViewNode{Class: "MyCustomView"},
			classmap: map[string]string{
				"MyCustomView": "_UIHostingView/UIView/UIResponder",
			},
			want: true,
		},
		{
			name:     "not a hosting view",
			node:     rawViewNode{Class: "UIView"},
			classmap: map[string]string{"UIView": "UIView/UIResponder"},
			want:     false,
		},
		{
			name:     "empty classmap",
			node:     rawViewNode{Class: "UIView"},
			classmap: map[string]string{},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHostingView(tt.node, tt.classmap)
			if got != tt.want {
				t.Errorf("isHostingView() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindNodeByAddress(t *testing.T) {
	views := []rawViewNode{
		{
			Address: "0x100",
			Subviews: []rawViewNode{
				{
					Address: "0x200",
					Subviews: []rawViewNode{
						{Address: "0x300"},
					},
				},
			},
		},
		{Address: "0x400"},
	}

	tests := []struct {
		name    string
		address string
		wantNil bool
	}{
		{"root node", "0x100", false},
		{"nested node", "0x200", false},
		{"deeply nested node", "0x300", false},
		{"second root", "0x400", false},
		{"not found", "0x999", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findNodeByAddress(views, tt.address)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got node at %s", got.Address)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.Address != tt.address {
				t.Errorf("expected address %s, got %s", tt.address, got.Address)
			}
		})
	}
}

func TestBuildTreeNode(t *testing.T) {
	classmap := map[string]string{
		"_UIHostingView<CV>": "_UIHostingView/UIView/UIResponder",
	}

	t.Run("simple node", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
			Frame:   []float64{0, 0, 402, 874},
		}
		got := buildTreeNode(node, classmap, nil, 0, -1)
		if got.Class != "UIView" {
			t.Errorf("expected UIView, got %s", got.Class)
		}
		if got.Frame == nil || got.Frame.Width != 402 {
			t.Error("expected frame with width 402")
		}
		if got.IsHostingView {
			t.Error("should not be hosting view")
		}
	})

	t.Run("nested with subviews", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
			Frame:   []float64{0, 0, 402, 874},
			Subviews: []rawViewNode{
				{Class: "UILabel", Address: "0x200", Frame: []float64{10, 20, 100, 30}},
				{Class: "UIButton", Address: "0x300", Frame: []float64{10, 60, 100, 40}},
			},
		}
		got := buildTreeNode(node, classmap, nil, 0, -1)
		if len(got.Subviews) != 2 {
			t.Fatalf("expected 2 subviews, got %d", len(got.Subviews))
		}
		if got.Subviews[0].Class != "UILabel" {
			t.Errorf("expected UILabel, got %s", got.Subviews[0].Class)
		}
	})

	t.Run("depth limit", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
			Subviews: []rawViewNode{
				{
					Class:   "UIView",
					Address: "0x200",
					Subviews: []rawViewNode{
						{Class: "UILabel", Address: "0x300"},
					},
				},
			},
		}
		got := buildTreeNode(node, classmap, nil, 0, 1)
		if len(got.Subviews) != 1 {
			t.Fatalf("expected 1 subview at depth 0, got %d", len(got.Subviews))
		}
		// depth 1 subview should have no subviews (depth limit reached)
		if len(got.Subviews[0].Subviews) != 0 {
			t.Errorf("expected 0 subviews at depth 1 (limit=1), got %d", len(got.Subviews[0].Subviews))
		}
	})

	t.Run("hosting view flag", func(t *testing.T) {
		node := rawViewNode{
			Class:   "_UIHostingView<CV>",
			Address: "0x100",
		}
		got := buildTreeNode(node, classmap, nil, 0, -1)
		if !got.IsHostingView {
			t.Error("expected isHostingView=true")
		}
	})

	t.Run("empty subviews omitted", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
		}
		got := buildTreeNode(node, classmap, nil, 0, -1)
		if got.Subviews != nil {
			t.Error("expected nil subviews when there are none")
		}
	})
}

func TestBuildDetailNode(t *testing.T) {
	classmap := map[string]string{
		"_UIHostingView<CV>": "_UIHostingView/UIView/UIResponder",
		"UIView":             "UIView/UIResponder",
	}

	t.Run("full fields", func(t *testing.T) {
		hidden := false
		ambiguous := true
		node := rawViewNode{
			Class:              "UIView",
			Address:            "0x100",
			Frame:              []float64{0, 0, 402, 874},
			Bounds:             []float64{0, 0, 402, 874},
			Position:           []float64{201, 437},
			Hidden:             &hidden,
			LayoutMargins:      []float64{0, 16, 0, 16},
			HasAmbiguousLayout: &ambiguous,
			Layer:              map[string]string{"class": "CALayer", "address": "0x600"},
			Constraints: []rawConstraint{
				{
					Class:           "NSLayoutConstraint",
					Address:         "0x500",
					FirstItem:       "0x100",
					FirstAttribute:  7,
					Relation:        0,
					SecondItem:      "0x200",
					SecondAttribute: 7,
					Multiplier:      1.0,
					Constant:        0.0,
					Priority:        1000.0,
				},
			},
			Subviews: []rawViewNode{{Address: "0x200"}, {Address: "0x300"}},
		}
		got := buildDetailNode(node, classmap, nil)

		if got.Class != "UIView" {
			t.Errorf("class: expected UIView, got %s", got.Class)
		}
		if got.Inheritance != "UIView/UIResponder" {
			t.Errorf("inheritance: expected UIView/UIResponder, got %s", got.Inheritance)
		}
		if got.Frame == nil || got.Frame.Width != 402 {
			t.Error("expected frame")
		}
		if got.Bounds == nil || got.Bounds.Width != 402 {
			t.Error("expected bounds")
		}
		if got.Position == nil || got.Position.X != 201 {
			t.Error("expected position")
		}
		if got.Hidden == nil || *got.Hidden != false {
			t.Error("expected hidden=false")
		}
		if got.LayoutMargins == nil || got.LayoutMargins.Left != 16 {
			t.Error("expected layout margins")
		}
		if got.HasAmbiguousLayout == nil || *got.HasAmbiguousLayout != true {
			t.Error("expected hasAmbiguousLayout=true")
		}
		if got.Layer == nil || got.Layer.Class != "CALayer" {
			t.Error("expected layer")
		}
		if len(got.Constraints) != 1 {
			t.Fatalf("expected 1 constraint, got %d", len(got.Constraints))
		}
		if got.Constraints[0].FirstAttribute != "width" {
			t.Errorf("expected width, got %s", got.Constraints[0].FirstAttribute)
		}
		if got.SubviewCount == nil || *got.SubviewCount != 2 {
			t.Error("expected subviewCount=2")
		}
		if got.IsHostingView {
			t.Error("should not be hosting view")
		}
	})

	t.Run("optional fields absent", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
		}
		got := buildDetailNode(node, classmap, nil)

		if got.Frame != nil {
			t.Error("expected nil frame")
		}
		if got.Bounds != nil {
			t.Error("expected nil bounds")
		}
		if got.Position != nil {
			t.Error("expected nil position")
		}
		if got.Hidden != nil {
			t.Error("expected nil hidden")
		}
		if got.LayoutMargins != nil {
			t.Error("expected nil layoutMargins")
		}
		if got.HasAmbiguousLayout != nil {
			t.Error("expected nil hasAmbiguousLayout")
		}
		if got.Layer != nil {
			t.Error("expected nil layer")
		}
	})

	t.Run("subviewCount always set", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
		}
		got := buildDetailNode(node, classmap, nil)
		if got.SubviewCount == nil {
			t.Fatal("expected subviewCount to be set")
		}
		if *got.SubviewCount != 0 {
			t.Errorf("expected subviewCount=0, got %d", *got.SubviewCount)
		}
	})

	t.Run("hosting view", func(t *testing.T) {
		node := rawViewNode{
			Class:   "_UIHostingView<CV>",
			Address: "0x100",
		}
		got := buildDetailNode(node, classmap, nil)
		if !got.IsHostingView {
			t.Error("expected isHostingView=true")
		}
		if got.Inheritance != "_UIHostingView/UIView/UIResponder" {
			t.Errorf("expected inheritance, got %q", got.Inheritance)
		}
	})

	t.Run("layer defaults", func(t *testing.T) {
		node := rawViewNode{
			Class:   "UIView",
			Address: "0x100",
			Layer:   map[string]string{},
		}
		got := buildDetailNode(node, classmap, nil)
		if got.Layer == nil {
			t.Fatal("expected layer")
		}
		if got.Layer.Class != "CALayer" {
			t.Errorf("expected default CALayer, got %s", got.Layer.Class)
		}
		if got.Layer.Address != "?" {
			t.Errorf("expected default address '?', got %s", got.Layer.Address)
		}
	})
}

func TestBuildTree(t *testing.T) {
	original := makeBplistMap(
		[]map[string]interface{}{
			{
				"class":   "UIWindow",
				"address": "0x100",
				"frame":   []interface{}{0.0, 0.0, 402.0, 874.0},
				"subviews": []interface{}{
					map[string]interface{}{
						"class":   "_UIHostingView<CV>",
						"address": "0x200",
						"frame":   []interface{}{0.0, 0.0, 402.0, 874.0},
						"subviews": []interface{}{
							map[string]interface{}{
								"class":   "UILabel",
								"address": "0x300",
								"frame":   []interface{}{10.0, 20.0, 100.0, 30.0},
							},
						},
					},
				},
			},
		},
		map[string]string{
			"_UIHostingView<CV>": "_UIHostingView/UIView/UIResponder",
		},
	)

	// Round-trip through bplist
	data := mustMarshalBplistMap(t, original)
	parsed, err := parseBplist(data)
	if err != nil {
		t.Fatalf("parseBplist failed: %v", err)
	}

	tree := buildTree(parsed, -1, nil)
	if len(tree.Views) != 1 {
		t.Fatalf("expected 1 root view, got %d", len(tree.Views))
	}

	root := tree.Views[0]
	if root.Class != "UIWindow" {
		t.Errorf("expected UIWindow, got %s", root.Class)
	}
	if len(root.Subviews) != 1 {
		t.Fatalf("expected 1 subview, got %d", len(root.Subviews))
	}
	hosting := root.Subviews[0]
	if !hosting.IsHostingView {
		t.Error("expected isHostingView=true")
	}
	if len(hosting.Subviews) != 1 {
		t.Fatalf("expected 1 subview under hosting, got %d", len(hosting.Subviews))
	}
	if hosting.Subviews[0].Class != "UILabel" {
		t.Errorf("expected UILabel, got %s", hosting.Subviews[0].Class)
	}
}

func TestBuildTreeWithDepthLimit(t *testing.T) {
	data := &rawBplistData{
		Views: []rawViewNode{
			{
				Class:   "UIView",
				Address: "0x100",
				Subviews: []rawViewNode{
					{
						Class:   "UIView",
						Address: "0x200",
						Subviews: []rawViewNode{
							{Class: "UILabel", Address: "0x300"},
						},
					},
				},
			},
		},
	}

	tree := buildTree(data, 1, nil)
	root := tree.Views[0]
	if len(root.Subviews) != 1 {
		t.Fatalf("expected 1 subview at depth 0, got %d", len(root.Subviews))
	}
	if len(root.Subviews[0].Subviews) != 0 {
		t.Errorf("expected no subviews at depth 1 (limit=1), got %d", len(root.Subviews[0].Subviews))
	}
}

func TestLookupAttribute(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "notAnAttribute"},
		{1, "left"},
		{7, "width"},
		{8, "height"},
		{9, "centerX"},
		{39, "centerYWithinMargins"},
		{99, "99"},
		{-5, "-5"},
	}
	for _, tt := range tests {
		got := lookupAttribute(tt.input)
		if got != tt.want {
			t.Errorf("lookupAttribute(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLookupRelation(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{-1, "<="},
		{0, "=="},
		{1, ">="},
		{99, "=="},
	}
	for _, tt := range tests {
		got := lookupRelation(tt.input)
		if got != tt.want {
			t.Errorf("lookupRelation(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsHostingViewClassname(t *testing.T) {
	if !isHostingView(rawViewNode{Class: "_UIHostingView<CV>"}, nil) {
		t.Error("expected true for class containing HostingView")
	}
	if isHostingView(rawViewNode{Class: "UIView"}, map[string]string{"UIView": "UIView/UIResponder"}) {
		t.Error("expected false for UIView")
	}
}

func TestBuildTreeIntegration(t *testing.T) {
	// Integration test: bplist -> parse -> buildTree -> verify structure
	original := makeBplistMap(
		[]map[string]interface{}{
			{
				"class":   "UIWindow",
				"address": "0x1",
				"frame":   []interface{}{0.0, 0.0, 320.0, 568.0},
				"subviews": []interface{}{
					map[string]interface{}{
						"class":   "UITransitionView",
						"address": "0x2",
						"frame":   []interface{}{0.0, 0.0, 320.0, 568.0},
						"subviews": []interface{}{
							map[string]interface{}{
								"class":   "_UIHostingView<ContentView>",
								"address": "0x3",
								"frame":   []interface{}{0.0, 0.0, 320.0, 568.0},
							},
						},
					},
				},
			},
		},
		map[string]string{
			"UIWindow":                    "UIWindow/UIView/UIResponder/NSObject",
			"UITransitionView":            "UITransitionView/UIView/UIResponder/NSObject",
			"_UIHostingView<ContentView>": "_UIHostingView/UIView/UIResponder/NSObject",
		},
	)

	data := mustMarshalBplistMap(t, original)
	parsed, err := parseBplist(data)
	if err != nil {
		t.Fatalf("parseBplist failed: %v", err)
	}

	tree := buildTree(parsed, -1, nil)

	// Verify the tree structure
	if len(tree.Views) != 1 {
		t.Fatalf("expected 1 root view, got %d", len(tree.Views))
	}

	window := tree.Views[0]
	if window.Class != "UIWindow" {
		t.Errorf("expected UIWindow, got %s", window.Class)
	}

	transition := window.Subviews[0]
	if transition.Class != "UITransitionView" {
		t.Errorf("expected UITransitionView, got %s", transition.Class)
	}

	hosting := transition.Subviews[0]
	if !hosting.IsHostingView {
		t.Error("expected _UIHostingView to be marked as hosting view")
	}

	// Verify findNodeByAddress works on parsed data
	found := findNodeByAddress(parsed.Views, "0x3")
	if found == nil {
		t.Fatal("expected to find node at 0x3")
	}
	if !strings.Contains(found.Class, "HostingView") {
		t.Errorf("expected HostingView class, got %s", found.Class)
	}
}
