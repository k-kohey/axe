package view

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"howett.net/plist"
)

// rawViewNode is the plist-decoded structure for a UIKit view.
type rawViewNode struct {
	Class              string            `plist:"class"`
	Address            string            `plist:"address"`
	Frame              []float64         `plist:"frame"`
	Bounds             []float64         `plist:"bounds"`
	Position           []float64         `plist:"position"`
	Hidden             *bool             `plist:"hidden"`
	LayoutMargins      []float64         `plist:"layoutMargins"`
	HasAmbiguousLayout *bool             `plist:"hasAmbiguousLayout"`
	Layer              map[string]string `plist:"layer"`
	Constraints        []rawConstraint   `plist:"constraints"`
	Subviews           []rawViewNode     `plist:"subviews"`
	ImageData          []byte            `plist:"imageData"`
}

// rawConstraint is the plist-decoded structure for an NSLayoutConstraint.
type rawConstraint struct {
	Class           string  `plist:"class"`
	Address         string  `plist:"address"`
	FirstItem       string  `plist:"firstItem"`
	FirstAttribute  int     `plist:"firstAttribute"`
	Relation        int     `plist:"relation"`
	SecondItem      string  `plist:"secondItem"`
	SecondAttribute int     `plist:"secondAttribute"`
	Multiplier      float64 `plist:"multiplier"`
	Constant        float64 `plist:"constant"`
	Priority        float64 `plist:"priority"`
}

// rawBplistData is the top-level plist structure.
type rawBplistData struct {
	Views    []rawViewNode     `plist:"views"`
	Classmap map[string]string `plist:"classmap"`
}

// layoutAttributes maps NSLayoutAttribute enum values to strings.
// https://developer.apple.com/documentation/uikit/nslayoutattribute
var layoutAttributes = map[int]string{
	0:  "notAnAttribute",
	1:  "left",
	2:  "right",
	3:  "top",
	4:  "bottom",
	5:  "leading",
	6:  "trailing",
	7:  "width",
	8:  "height",
	9:  "centerX",
	10: "centerY",
	11: "lastBaseline",
	12: "firstBaseline",
	32: "leftMargin",
	33: "rightMargin",
	34: "topMargin",
	35: "bottomMargin",
	36: "leadingMargin",
	37: "trailingMargin",
	38: "centerXWithinMargins",
	39: "centerYWithinMargins",
}

// layoutRelations maps NSLayoutRelation enum values to strings.
var layoutRelations = map[int]string{
	-1: "<=",
	0:  "==",
	1:  ">=",
}

// parseBplist decodes binary plist bytes into rawBplistData.
func parseBplist(data []byte) (*rawBplistData, error) {
	var result rawBplistData
	if _, err := plist.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to decode bplist: %w", err)
	}
	return &result, nil
}

// parseBplistFile reads and decodes a binary plist file.
func parseBplistFile(path string) (*rawBplistData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read bplist file: %w", err)
	}
	return parseBplist(data)
}

// buildRect converts a [x, y, w, h] slice to a Rect pointer, or nil.
func buildRect(values []float64) *Rect {
	if len(values) < 4 {
		return nil
	}
	return &Rect{X: values[0], Y: values[1], Width: values[2], Height: values[3]}
}

// buildPoint converts a [x, y] slice to a Point pointer, or nil.
func buildPoint(values []float64) *Point {
	if len(values) < 2 {
		return nil
	}
	return &Point{X: values[0], Y: values[1]}
}

// buildInsets converts a [top, left, bottom, right] slice to an Insets pointer, or nil.
func buildInsets(values []float64) *Insets {
	if len(values) < 4 {
		return nil
	}
	return &Insets{Top: values[0], Left: values[1], Bottom: values[2], Right: values[3]}
}

// lookupAttribute returns the string name for an NSLayoutAttribute enum value.
func lookupAttribute(attr int) string {
	if s, ok := layoutAttributes[attr]; ok {
		return s
	}
	return strconv.Itoa(attr)
}

// lookupRelation returns the string name for an NSLayoutRelation enum value.
func lookupRelation(rel int) string {
	if s, ok := layoutRelations[rel]; ok {
		return s
	}
	return "=="
}

// buildConstraint converts a rawConstraint to a Constraint.
func buildConstraint(c rawConstraint) Constraint {
	cls := c.Class
	if cls == "" {
		cls = "NSLayoutConstraint"
	}
	addr := c.Address
	if addr == "" {
		addr = "?"
	}
	firstItem := c.FirstItem
	if firstItem == "" {
		firstItem = "?"
	}
	secondItem := c.SecondItem
	if secondItem == "" {
		secondItem = "?"
	}
	return Constraint{
		Class:           cls,
		Address:         addr,
		FirstItem:       firstItem,
		FirstAttribute:  lookupAttribute(c.FirstAttribute),
		Relation:        lookupRelation(c.Relation),
		SecondItem:      secondItem,
		SecondAttribute: lookupAttribute(c.SecondAttribute),
		Multiplier:      c.Multiplier,
		Constant:        c.Constant,
		Priority:        c.Priority,
	}
}

// isHostingView checks if a node is a _UIHostingView (SwiftUI host).
func isHostingView(node rawViewNode, classmap map[string]string) bool {
	if strings.Contains(node.Class, "HostingView") {
		return true
	}
	if path, ok := classmap[node.Class]; ok {
		return strings.Contains(path, "HostingView")
	}
	return false
}

// findNodeByAddress recursively searches views for a node matching the given address.
func findNodeByAddress(views []rawViewNode, address string) *rawViewNode {
	for i := range views {
		if views[i].Address == address {
			return &views[i]
		}
		if found := findNodeByAddress(views[i].Subviews, address); found != nil {
			return found
		}
	}
	return nil
}

// demangleClass returns the demangled name if available, otherwise the original.
func demangleClass(name string, demangled map[string]string) string {
	if demangled != nil {
		if d, ok := demangled[name]; ok {
			return d
		}
	}
	return name
}

// demangleInheritance demangles each "/" separated component of an inheritance path.
func demangleInheritance(inheritance string, demangled map[string]string) string {
	if demangled == nil || inheritance == "" {
		return inheritance
	}
	parts := strings.Split(inheritance, "/")
	changed := false
	for i, part := range parts {
		if d, ok := demangled[part]; ok {
			parts[i] = d
			changed = true
		}
	}
	if !changed {
		return inheritance
	}
	return strings.Join(parts, "/")
}

// buildTreeNode recursively converts a rawViewNode to a UIKitView for tree mode.
// maxDepth of -1 means unlimited depth.
func buildTreeNode(node rawViewNode, classmap map[string]string, demangled map[string]string, depth, maxDepth int) UIKitView {
	result := UIKitView{
		Class:   demangleClass(node.Class, demangled),
		Address: node.Address,
		Frame:   buildRect(node.Frame),
	}

	if isHostingView(node, classmap) {
		result.IsHostingView = true
	}

	if maxDepth >= 0 && depth >= maxDepth {
		return result
	}

	if len(node.Subviews) > 0 {
		result.Subviews = make([]UIKitView, len(node.Subviews))
		for i, sv := range node.Subviews {
			result.Subviews[i] = buildTreeNode(sv, classmap, demangled, depth+1, maxDepth)
		}
	}

	return result
}

// buildDetailNode converts a rawViewNode to a detailed UIKitView.
func buildDetailNode(node rawViewNode, classmap map[string]string, demangled map[string]string) UIKitView {
	result := UIKitView{
		Class:   demangleClass(node.Class, demangled),
		Address: node.Address,
	}

	if inheritance, ok := classmap[node.Class]; ok && inheritance != "" {
		result.Inheritance = demangleInheritance(inheritance, demangled)
	}

	result.Frame = buildRect(node.Frame)
	result.Bounds = buildRect(node.Bounds)
	result.Position = buildPoint(node.Position)
	result.Hidden = node.Hidden
	result.LayoutMargins = buildInsets(node.LayoutMargins)
	result.HasAmbiguousLayout = node.HasAmbiguousLayout

	if node.Layer != nil {
		cls := node.Layer["class"]
		if cls == "" {
			cls = "CALayer"
		}
		addr := node.Layer["address"]
		if addr == "" {
			addr = "?"
		}
		result.Layer = &LayerInfo{Class: demangleClass(cls, demangled), Address: addr}
	}

	constraints := make([]Constraint, len(node.Constraints))
	for i, c := range node.Constraints {
		constraints[i] = buildConstraint(c)
	}
	result.Constraints = constraints

	subviewCount := len(node.Subviews)
	result.SubviewCount = &subviewCount

	result.IsHostingView = isHostingView(node, classmap)

	return result
}

// buildAddressClassMap recursively walks rawViewNode trees and builds an address → demangled class name map.
func buildAddressClassMap(views []rawViewNode, demangled map[string]string) map[string]string {
	m := make(map[string]string)
	var walk func(nodes []rawViewNode)
	walk = func(nodes []rawViewNode) {
		for _, n := range nodes {
			if n.Address != "" {
				m[n.Address] = demangleClass(n.Class, demangled)
			}
			walk(n.Subviews)
		}
	}
	walk(views)
	return m
}

// buildTree converts rawBplistData to a TreeOutput.
// maxDepth of -1 means unlimited depth.
func buildTree(data *rawBplistData, maxDepth int, demangled map[string]string) TreeOutput {
	views := make([]UIKitView, len(data.Views))
	for i, v := range data.Views {
		views[i] = buildTreeNode(v, data.Classmap, demangled, 0, maxDepth)
	}
	return TreeOutput{Views: views}
}
