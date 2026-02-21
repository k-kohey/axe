package view

import (
	"encoding/json"
	"strconv"
	"strings"
)

// rawSwiftUINode is the JSON structure from _viewDebugData() + Mirror extraction.
// All property values are strings; size/position use "(w, h)" tuple format.
type rawSwiftUINode struct {
	Type      string           `json:"type"`
	Value     string           `json:"value"`
	Size      string           `json:"size"`
	Position  string           `json:"position"`
	Transform string           `json:"transform"`
	Children  []rawSwiftUINode `json:"children"`
}

// parseTuple parses a "(a, b)" string into two float64 values.
func parseTuple(s string) (float64, float64, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return 0, 0, false
	}
	inner := s[1 : len(s)-1]
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, errA := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	b, errB := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if errA != nil || errB != nil {
		return 0, 0, false
	}
	return a, b, true
}

// extractShortName extracts the short name from a fully qualified type string.
// e.g. "SwiftUI.Text" -> "Text", "MyApp.ContentView" -> "ContentView"
func extractShortName(fullType string) string {
	if fullType == "" {
		return "Unknown"
	}
	// Find last dot before any generic parameters
	base := fullType
	if idx := strings.Index(fullType, "<"); idx >= 0 {
		base = fullType[:idx]
	}
	if dotIdx := strings.LastIndex(base, "."); dotIdx >= 0 {
		suffix := fullType[dotIdx+1:]
		return suffix
	}
	return fullType
}

func getNodeName(node rawSwiftUINode) string {
	return extractShortName(node.Type)
}

func getNodeSize(node rawSwiftUINode) *Size {
	w, h, ok := parseTuple(node.Size)
	if !ok {
		return nil
	}
	return &Size{Width: w, Height: h}
}

func getNodeType(node rawSwiftUINode) string {
	return node.Type
}

func getNodePosition(node rawSwiftUINode) *Point {
	x, y, ok := parseTuple(node.Position)
	if !ok {
		return nil
	}
	return &Point{X: x, Y: y}
}

func getNodeValue(node rawSwiftUINode) string {
	return node.Value
}

func getNodeTransform(node rawSwiftUINode) string {
	return node.Transform
}

func isIntermediate(node rawSwiftUINode) bool {
	return getNodeSize(node) == nil
}

func collectChildren(node rawSwiftUINode, compact bool) []rawSwiftUINode {
	if !compact {
		return node.Children
	}
	var result []rawSwiftUINode
	for _, child := range node.Children {
		if isIntermediate(child) {
			result = append(result, collectChildren(child, compact)...)
		} else {
			result = append(result, child)
		}
	}
	return result
}

func buildSwiftUINode(node rawSwiftUINode, compact bool) SwiftUINode {
	name := getNodeName(node)
	fullType := getNodeType(node)
	if fullType == name {
		fullType = ""
	}
	out := SwiftUINode{
		Name:      name,
		Type:      fullType,
		Position:  getNodePosition(node),
		Size:      getNodeSize(node),
		Value:     getNodeValue(node),
		Transform: getNodeTransform(node),
	}

	children := collectChildren(node, compact)
	for _, child := range children {
		out.Children = append(out.Children, buildSwiftUINode(child, compact))
	}

	return out
}

// ParseSwiftUIJSON parses _viewDebugData() JSON bytes into SwiftUINode trees.
// compact mode skips intermediate ModifiedContent/_ViewModifier_Content nodes without size.
func ParseSwiftUIJSON(data []byte, compact bool) ([]SwiftUINode, error) {
	var raw []rawSwiftUINode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	var nodes []SwiftUINode
	for _, r := range raw {
		nodes = append(nodes, buildSwiftUINode(r, compact))
	}
	return nodes, nil
}
