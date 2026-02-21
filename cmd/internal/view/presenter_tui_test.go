package view

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"
)

func TestBuildTreeNodeLabel(t *testing.T) {
	tests := []struct {
		name     string
		node     rawViewNode
		classmap map[string]string
		want     string
	}{
		{
			name: "class with frame",
			node: rawViewNode{
				Class: "UIWindow",
				Frame: []float64{0, 0, 440, 956},
			},
			classmap: nil,
			want:     "UIWindow [gray]440x956[-]",
		},
		{
			name: "class without frame",
			node: rawViewNode{
				Class: "UIView",
			},
			classmap: nil,
			want:     "UIView",
		},
		{
			name: "hosting view with frame",
			node: rawViewNode{
				Class: "_UIHostingView",
				Frame: []float64{0, 0, 440, 956},
			},
			classmap: nil,
			want:     "_UIHostingView ★ [gray]440x956[-]",
		},
		{
			name: "hosting view via classmap",
			node: rawViewNode{
				Class: "SomeClass",
				Frame: []float64{0, 0, 320, 480},
			},
			classmap: map[string]string{
				"SomeClass": "UIView/_UIHostingView",
			},
			want: "SomeClass ★ [gray]320x480[-]",
		},
		{
			name: "incomplete frame",
			node: rawViewNode{
				Class: "UILabel",
				Frame: []float64{10, 20},
			},
			classmap: nil,
			want:     "UILabel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTreeNodeLabel(tt.node, tt.classmap, nil)
			if got != tt.want {
				t.Errorf("buildTreeNodeLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderDetailText(t *testing.T) {
	hidden := false
	ambiguous := false
	subviewCount := 1

	detail := UIKitView{
		Class:              "UIWindow",
		Address:            "0x104b05bb0",
		Inheritance:        "UIWindow/UIView/UIResponder/NSObject",
		Frame:              &Rect{X: 0, Y: 0, Width: 440, Height: 956},
		Bounds:             &Rect{X: 0, Y: 0, Width: 440, Height: 956},
		Position:           &Point{X: 220, Y: 478},
		Hidden:             &hidden,
		LayoutMargins:      &Insets{Top: 20, Left: 16, Bottom: 34, Right: 16},
		HasAmbiguousLayout: &ambiguous,
		Layer:              &LayerInfo{Class: "UIWindowLayer", Address: "0x600000270580"},
		Constraints: []Constraint{
			{
				FirstItem:       "0x104b05bb0",
				FirstAttribute:  "width",
				Relation:        "==",
				SecondItem:      "0x104c06c60",
				SecondAttribute: "width",
				Multiplier:      1.0,
				Constant:        0,
				Priority:        1000,
			},
		},
		SubviewCount: &subviewCount,
	}

	addrMap := map[string]string{
		"0x104b05bb0": "UIWindow",
		"0x104c06c60": "UIView",
	}
	text := renderDetailText(detail, addrMap)

	mustContain := []string{
		"[yellow]Class:[-]        UIWindow",
		"[yellow]Address:[-]      0x104b05bb0",
		"[yellow]Inheritance:[-]  UIWindow/UIView/UIResponder/NSObject",
		"[yellow]Frame:[-]        (0, 0) 440x956",
		"[yellow]Bounds:[-]       (0, 0) 440x956",
		"[yellow]Position:[-]     (220, 478)",
		"[yellow]Hidden:[-]       false",
		"[yellow]LayoutMargins:[-]",
		"[yellow]AmbiguousLayout:[-] false",
		"[yellow]Layer:[-]        UIWindowLayer",
		"[yellow]Subviews:[-]     1",
		"[yellow]Constraints:[-]  1",
		"[cyan]UIWindow[-].width == [green]UIView[-].width",
	}

	for _, s := range mustContain {
		if !strings.Contains(text, s) {
			t.Errorf("renderDetailText() output missing %q\n\nGot:\n%s", s, text)
		}
	}
}

func TestRenderSwiftUIText(t *testing.T) {
	tests := []struct {
		name   string
		nodes  []SwiftUINode
		prefix string
		want   string
	}{
		{
			name: "single node with size",
			nodes: []SwiftUINode{
				{Name: "Text", Size: &Size{Width: 100, Height: 20}},
			},
			prefix: "",
			want:   "└── Text  100x20\n",
		},
		{
			name: "node with value",
			nodes: []SwiftUINode{
				{Name: "Text", Value: "Hello", Size: &Size{Width: 100, Height: 20}},
			},
			prefix: "",
			want:   "└── Text \"Hello\"  100x20\n",
		},
		{
			name: "nested nodes",
			nodes: []SwiftUINode{
				{
					Name: "VStack",
					Size: &Size{Width: 402, Height: 800},
					Children: []SwiftUINode{
						{Name: "Text", Value: "Hello", Size: &Size{Width: 100, Height: 20}},
						{Name: "Button", Size: &Size{Width: 200, Height: 44}},
					},
				},
			},
			prefix: "",
			want: "└── VStack  402x800\n" +
				"    ├── Text \"Hello\"  100x20\n" +
				"    └── Button  200x44\n",
		},
		{
			name: "siblings",
			nodes: []SwiftUINode{
				{Name: "Text", Size: &Size{Width: 100, Height: 20}},
				{Name: "Button", Size: &Size{Width: 200, Height: 44}},
			},
			prefix: "  ",
			want: "  ├── Text  100x20\n" +
				"  └── Button  200x44\n",
		},
		{
			name: "deep nesting with siblings",
			nodes: []SwiftUINode{
				{
					Name: "VStack",
					Children: []SwiftUINode{
						{
							Name: "HStack",
							Children: []SwiftUINode{
								{Name: "Image"},
							},
						},
						{Name: "Spacer"},
					},
				},
			},
			prefix: "",
			want: "└── VStack\n" +
				"    ├── HStack\n" +
				"    │   └── Image\n" +
				"    └── Spacer\n",
		},
		{
			name:   "empty nodes",
			nodes:  []SwiftUINode{},
			prefix: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderSwiftUIText(tt.nodes, tt.prefix)
			if got != tt.want {
				t.Errorf("renderSwiftUIText() =\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderDetailTextMinimal(t *testing.T) {
	detail := UIKitView{
		Class:   "UIView",
		Address: "0x123",
	}

	text := renderDetailText(detail, nil)

	if !strings.Contains(text, "[yellow]Class:[-]        UIView") {
		t.Errorf("expected Class line, got:\n%s", text)
	}
	if !strings.Contains(text, "[yellow]Address:[-]      0x123") {
		t.Errorf("expected Address line, got:\n%s", text)
	}

	mustNotContain := []string{
		"Inheritance:",
		"Frame:",
		"Bounds:",
		"Position:",
		"Hidden:",
		"LayoutMargins:",
		"AmbiguousLayout:",
		"Layer:",
		"Snapshot:",
		"HostingView:",
	}

	for _, s := range mustNotContain {
		if strings.Contains(text, s) {
			t.Errorf("renderDetailText() output should not contain %q for minimal node\n\nGot:\n%s", s, text)
		}
	}
}

func TestFlattenTreeNodes(t *testing.T) {
	// Build a small tree: Root -> UIWindow (440x956) -> UIView (320x480)
	windowNode := rawViewNode{
		Class:   "UIWindow",
		Address: "0x1001",
		Frame:   []float64{0, 0, 440, 956},
	}
	viewNode := rawViewNode{
		Class:   "UIView",
		Address: "0x1002",
		Frame:   []float64{0, 0, 320, 480},
	}

	childTN := tview.NewTreeNode("UIView [gray]320x480[-]").
		SetReference(&viewNode)
	rootTN := tview.NewTreeNode("Root").SetSelectable(false)
	windowTN := tview.NewTreeNode("UIWindow [gray]440x956[-]").
		SetReference(&windowNode)
	windowTN.AddChild(childTN)
	rootTN.AddChild(windowTN)

	lines := flattenTreeNodes(rootTN, 0)

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}

	// First line: window at depth 0
	if !strings.HasPrefix(lines[0], "0x1001\t") {
		t.Errorf("line[0] should start with address, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "UIWindow") {
		t.Errorf("line[0] should contain UIWindow, got %q", lines[0])
	}

	// Second line: view at depth 1 with indent
	if !strings.HasPrefix(lines[1], "0x1002\t") {
		t.Errorf("line[1] should start with address, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "  UIView") {
		t.Errorf("line[1] should have indented UIView, got %q", lines[1])
	}
}

func TestFindTreeNodeByAddress(t *testing.T) {
	nodeA := rawViewNode{Class: "UIWindow", Address: "0xAAA"}
	nodeB := rawViewNode{Class: "UIView", Address: "0xBBB"}
	nodeC := rawViewNode{Class: "UILabel", Address: "0xCCC"}

	tnA := tview.NewTreeNode("UIWindow").SetReference(&nodeA)
	tnB := tview.NewTreeNode("UIView").SetReference(&nodeB)
	tnC := tview.NewTreeNode("UILabel").SetReference(&nodeC)
	tnB.AddChild(tnC)

	root := tview.NewTreeNode("Root")
	root.AddChild(tnA)
	root.AddChild(tnB)

	// Find existing node
	found := findTreeNodeByAddress(root, "0xCCC")
	if found == nil {
		t.Fatal("expected to find node with address 0xCCC")
	}
	if found.GetText() != "UILabel" {
		t.Errorf("expected UILabel, got %q", found.GetText())
	}

	// Find root-level node
	found = findTreeNodeByAddress(root, "0xAAA")
	if found == nil {
		t.Fatal("expected to find node with address 0xAAA")
	}

	// Not found
	found = findTreeNodeByAddress(root, "0xDDD")
	if found != nil {
		t.Errorf("expected nil for non-existent address, got %v", found)
	}
}

func TestFormatConstraint(t *testing.T) {
	addrMap := map[string]string{
		"0x104b1dd50": "UIView",
		"0x104b2acc0": "UILayoutContainerView",
		"0x104b47830": "UINavigationBar",
	}

	tests := []struct {
		name string
		c    Constraint
		want string
	}{
		{
			name: "constant constraint (secondItem=0x0, notAnAttribute)",
			c: Constraint{
				FirstItem:       "0x104b1dd50",
				FirstAttribute:  "leftMargin",
				Relation:        "==",
				SecondItem:      "0x0",
				SecondAttribute: "notAnAttribute",
				Multiplier:      1.0,
				Constant:        0.0,
				Priority:        1000,
			},
			want: "  [cyan]UIView[-].leftMargin == 0",
		},
		{
			name: "normal constraint with multiplier=1, constant=0, priority=1000 (all omitted)",
			c: Constraint{
				FirstItem:       "0x104b2acc0",
				FirstAttribute:  "width",
				Relation:        "==",
				SecondItem:      "0x104b1dd50",
				SecondAttribute: "leadingMargin",
				Multiplier:      1.0,
				Constant:        0.0,
				Priority:        1000,
			},
			want: "  [cyan]UILayoutContainerView[-].width == [green]UIView[-].leadingMargin",
		},
		{
			name: "constant constraint with non-required priority",
			c: Constraint{
				FirstItem:       "0x104b47830",
				FirstAttribute:  "width",
				Relation:        "==",
				SecondItem:      "0x0",
				SecondAttribute: "notAnAttribute",
				Multiplier:      1.0,
				Constant:        0.0,
				Priority:        250,
			},
			want: "  [cyan]UINavigationBar[-].width == 0  (priority: 250)",
		},
		{
			name: "non-trivial multiplier and constant",
			c: Constraint{
				FirstItem:       "0x104b1dd50",
				FirstAttribute:  "width",
				Relation:        "==",
				SecondItem:      "0x104b2acc0",
				SecondAttribute: "width",
				Multiplier:      2.0,
				Constant:        10.0,
				Priority:        750,
			},
			want: "  [cyan]UIView[-].width == [green]UILayoutContainerView[-].width * 2 + 10  (priority: 750)",
		},
		{
			name: "negative constant",
			c: Constraint{
				FirstItem:       "0x104b1dd50",
				FirstAttribute:  "trailing",
				Relation:        "==",
				SecondItem:      "0x104b2acc0",
				SecondAttribute: "trailing",
				Multiplier:      1.0,
				Constant:        -8.0,
				Priority:        1000,
			},
			want: "  [cyan]UIView[-].trailing == [green]UILayoutContainerView[-].trailing - 8",
		},
		{
			name: "unknown address shortened",
			c: Constraint{
				FirstItem:       "0x104b1dd50",
				FirstAttribute:  "top",
				Relation:        "==",
				SecondItem:      "0x999999999",
				SecondAttribute: "bottom",
				Multiplier:      1.0,
				Constant:        0.0,
				Priority:        1000,
			},
			want: "  [cyan]UIView[-].top == [green]0x…99999[-].bottom",
		},
		{
			name: "secondItem=? treated as constant constraint",
			c: Constraint{
				FirstItem:       "0x104b1dd50",
				FirstAttribute:  "height",
				Relation:        "==",
				SecondItem:      "?",
				SecondAttribute: "notAnAttribute",
				Multiplier:      1.0,
				Constant:        44.0,
				Priority:        1000,
			},
			want: "  [cyan]UIView[-].height == 44",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatConstraint(tt.c, addrMap)
			if got != tt.want {
				t.Errorf("formatConstraint() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestBuildAddressClassMap(t *testing.T) {
	views := []rawViewNode{
		{
			Class:   "UIWindow",
			Address: "0x100",
			Subviews: []rawViewNode{
				{
					Class:   "UIView",
					Address: "0x200",
				},
			},
		},
		{
			Class:   "UILabel",
			Address: "0x300",
		},
	}
	demangled := map[string]string{
		"UIWindow": "MyWindow",
	}
	m := buildAddressClassMap(views, demangled)
	if m["0x100"] != "MyWindow" {
		t.Errorf("expected MyWindow for 0x100, got %q", m["0x100"])
	}
	if m["0x200"] != "UIView" {
		t.Errorf("expected UIView for 0x200, got %q", m["0x200"])
	}
	if m["0x300"] != "UILabel" {
		t.Errorf("expected UILabel for 0x300, got %q", m["0x300"])
	}
}

func TestHighlightYAML(t *testing.T) {
	input := "tree:\n- name: VStack\n  size:\n    width: 402\n    height: 800\n  children:\n  - name: Text\n    value: Hello\n"
	got := highlightYAML(input)

	mustContain := []string{
		"[yellow]tree[-]:",
		"- [yellow]name[-]: VStack",
		"  [yellow]size[-]:",
		"    [yellow]width[-]: 402",
		"  - [yellow]name[-]: Text",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("highlightYAML() missing %q\n\nGot:\n%s", s, got)
		}
	}
}

func TestLoadSnapshotImage_ValidPNG(t *testing.T) {
	// Create a temporary PNG file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	got := loadSnapshotImage(path)
	if got == nil {
		t.Fatal("loadSnapshotImage returned nil for valid PNG")
	}
	bounds := got.Bounds()
	if bounds.Dx() != 2 || bounds.Dy() != 2 {
		t.Errorf("expected 2x2, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestLoadSnapshotImage_InvalidPath(t *testing.T) {
	got := loadSnapshotImage("/nonexistent/path/to/image.png")
	if got != nil {
		t.Error("loadSnapshotImage should return nil for invalid path")
	}
}

func TestLoadSnapshotImage_NotPNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notimage.txt")
	if err := os.WriteFile(path, []byte("not a png"), 0644); err != nil {
		t.Fatal(err)
	}

	got := loadSnapshotImage(path)
	if got != nil {
		t.Error("loadSnapshotImage should return nil for non-PNG file")
	}
}
