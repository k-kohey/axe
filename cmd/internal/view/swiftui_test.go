package view

import (
	"testing"
)

func TestParseTuple(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantA  float64
		wantB  float64
		wantOK bool
	}{
		{"normal", "(100.0, 20.0)", 100.0, 20.0, true},
		{"integers", "(402, 874)", 402.0, 874.0, true},
		{"with spaces", "( 50.5 , 30.0 )", 50.5, 30.0, true},
		{"negative", "(-10.0, -20.0)", -10.0, -20.0, true},
		{"empty string", "", 0, 0, false},
		{"no parens", "100.0, 20.0", 0, 0, false},
		{"single value", "(100.0)", 0, 0, false},
		{"non-numeric", "(abc, def)", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b, ok := parseTuple(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("parseTuple(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok {
				if a != tt.wantA || b != tt.wantB {
					t.Errorf("parseTuple(%q) = (%v, %v), want (%v, %v)", tt.input, a, b, tt.wantA, tt.wantB)
				}
			}
		})
	}
}

func TestExtractShortName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"SwiftUI.Text", "Text"},
		{"MyApp.ContentView", "ContentView"},
		{"SwiftUI.ModifiedContent<Text, SomeModifier>", "ModifiedContent<Text, SomeModifier>"},
		{"Text", "Text"},
		{"", "Unknown"},
		{"SwiftUI._ViewModifier_Content<M>", "_ViewModifier_Content<M>"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractShortName(tt.input)
			if got != tt.want {
				t.Errorf("extractShortName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSwiftUIJSON_SingleNode(t *testing.T) {
	input := `[{
		"type": "SwiftUI.Text",
		"size": "(100.0, 20.0)"
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "Text" {
		t.Errorf("expected name Text, got %s", nodes[0].Name)
	}
	if nodes[0].Size == nil {
		t.Fatal("expected size, got nil")
	}
	if nodes[0].Size.Width != 100.0 || nodes[0].Size.Height != 20.0 {
		t.Errorf("expected size 100x20, got %vx%v", nodes[0].Size.Width, nodes[0].Size.Height)
	}
}

func TestParseSwiftUIJSON_Nested(t *testing.T) {
	input := `[{
		"type": "SwiftUI.VStack",
		"children": [{
			"type": "SwiftUI.Text",
			"size": "(50.0, 10.0)"
		}]
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 root node, got %d", len(nodes))
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Name != "Text" {
		t.Errorf("expected child name Text, got %s", nodes[0].Children[0].Name)
	}
}

func TestParseSwiftUIJSON_CompactSkipsNoSize(t *testing.T) {
	// Nodes without size should be skipped in compact mode, their children hoisted
	input := `[{
		"type": "SwiftUI.VStack",
		"size": "(200.0, 100.0)",
		"children": [{
			"type": "SwiftUI._SafeAreaInsetsModifier",
			"children": [{
				"type": "SwiftUI.Text",
				"size": "(50.0, 10.0)"
			}]
		}]
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// _SafeAreaInsetsModifier (no size) should be skipped, Text hoisted to VStack's children
	if len(nodes[0].Children) != 1 {
		t.Fatalf("expected 1 child (hoisted), got %d", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Name != "Text" {
		t.Errorf("expected hoisted child Text, got %s", nodes[0].Children[0].Name)
	}
}

func TestParseSwiftUIJSON_NoCompactKeepsNoSize(t *testing.T) {
	input := `[{
		"type": "SwiftUI.VStack",
		"size": "(200.0, 100.0)",
		"children": [{
			"type": "SwiftUI._SafeAreaInsetsModifier",
			"children": [{
				"type": "SwiftUI.Text",
				"size": "(50.0, 10.0)"
			}]
		}]
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No compact: node without size should remain
	if len(nodes[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Name != "_SafeAreaInsetsModifier" {
		t.Errorf("expected _SafeAreaInsetsModifier, got %s", nodes[0].Children[0].Name)
	}
	if len(nodes[0].Children[0].Children) != 1 {
		t.Fatalf("expected 1 grandchild, got %d", len(nodes[0].Children[0].Children))
	}
}

func TestParseSwiftUIJSON_NodeWithSizeKept(t *testing.T) {
	// Any node WITH size should NOT be skipped even in compact mode
	input := `[{
		"type": "SwiftUI.VStack",
		"size": "(300.0, 200.0)",
		"children": [{
			"type": "SwiftUI._SafeAreaInsetsModifier",
			"size": "(200.0, 100.0)",
			"children": [{
				"type": "SwiftUI.Text",
				"size": "(50.0, 10.0)"
			}]
		}]
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodes[0].Children[0].Name != "_SafeAreaInsetsModifier" {
		t.Errorf("expected _SafeAreaInsetsModifier (has size), got %s", nodes[0].Children[0].Name)
	}
}

func TestParseSwiftUIJSON_NoSize(t *testing.T) {
	input := `[{
		"type": "SwiftUI.Spacer"
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodes[0].Size != nil {
		t.Errorf("expected nil size, got %v", nodes[0].Size)
	}
}

func TestParseSwiftUIJSON_InvalidJSON(t *testing.T) {
	_, err := ParseSwiftUIJSON([]byte("not json"), true)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseSwiftUIJSON_EmptyArray(t *testing.T) {
	nodes, err := ParseSwiftUIJSON([]byte("[]"), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestParseSwiftUIJSON_ValueAndTransform(t *testing.T) {
	input := `[{
		"type": "SwiftUI.Text",
		"value": "Text(storage: anyTextStorage) \"Hello World\"",
		"transform": "CoordinateSpace(base: 1)",
		"size": "(100.0, 20.0)",
		"position": "(50.0, 200.0)"
	}]`

	nodes, err := ParseSwiftUIJSON([]byte(input), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nodes[0].Value != "Text(storage: anyTextStorage) \"Hello World\"" {
		t.Errorf("unexpected value: %s", nodes[0].Value)
	}
	if nodes[0].Transform != "CoordinateSpace(base: 1)" {
		t.Errorf("unexpected transform: %s", nodes[0].Transform)
	}
	if nodes[0].Position == nil || nodes[0].Position.X != 50.0 || nodes[0].Position.Y != 200.0 {
		t.Errorf("unexpected position: %v", nodes[0].Position)
	}
}
