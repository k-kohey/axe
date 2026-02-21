package view

// Rect represents a rectangle with origin and size.
type Rect struct {
	X      float64 `json:"x" yaml:"x"`
	Y      float64 `json:"y" yaml:"y"`
	Width  float64 `json:"width" yaml:"width"`
	Height float64 `json:"height" yaml:"height"`
}

// Point represents a 2D point.
type Point struct {
	X float64 `json:"x" yaml:"x"`
	Y float64 `json:"y" yaml:"y"`
}

// Insets represents edge insets.
type Insets struct {
	Top    float64 `json:"top" yaml:"top"`
	Left   float64 `json:"left" yaml:"left"`
	Bottom float64 `json:"bottom" yaml:"bottom"`
	Right  float64 `json:"right" yaml:"right"`
}

// LayerInfo represents a CALayer summary.
type LayerInfo struct {
	Class   string `json:"class" yaml:"class"`
	Address string `json:"address" yaml:"address"`
}

// Constraint represents a single NSLayoutConstraint.
type Constraint struct {
	Class           string  `json:"class,omitempty" yaml:"class,omitempty"`
	Address         string  `json:"address,omitempty" yaml:"address,omitempty"`
	FirstItem       string  `json:"firstItem" yaml:"firstItem"`
	FirstAttribute  string  `json:"firstAttribute" yaml:"firstAttribute"`
	Relation        string  `json:"relation" yaml:"relation"`
	SecondItem      string  `json:"secondItem" yaml:"secondItem"`
	SecondAttribute string  `json:"secondAttribute" yaml:"secondAttribute"`
	Multiplier      float64 `json:"multiplier" yaml:"multiplier"`
	Constant        float64 `json:"constant" yaml:"constant"`
	Priority        float64 `json:"priority" yaml:"priority"`
}

// UIKitView represents a UIKit view node (used for both tree and detail).
type UIKitView struct {
	Class              string       `json:"class" yaml:"class"`
	Address            string       `json:"address" yaml:"address"`
	Inheritance        string       `json:"inheritance,omitempty" yaml:"inheritance,omitempty"`
	Frame              *Rect        `json:"frame,omitempty" yaml:"frame,omitempty"`
	Bounds             *Rect        `json:"bounds,omitempty" yaml:"bounds,omitempty"`
	Position           *Point       `json:"position,omitempty" yaml:"position,omitempty"`
	Hidden             *bool        `json:"hidden,omitempty" yaml:"hidden,omitempty"`
	LayoutMargins      *Insets      `json:"layoutMargins,omitempty" yaml:"layoutMargins,omitempty"`
	HasAmbiguousLayout *bool        `json:"hasAmbiguousLayout,omitempty" yaml:"hasAmbiguousLayout,omitempty"`
	Layer              *LayerInfo   `json:"layer,omitempty" yaml:"layer,omitempty"`
	Constraints        []Constraint `json:"constraints,omitempty" yaml:"constraints,omitempty"`
	SubviewCount       *int         `json:"subviewCount,omitempty" yaml:"subviewCount,omitempty"`
	IsHostingView      bool         `json:"isHostingView,omitempty" yaml:"isHostingView,omitempty"`
	Snapshot           string       `json:"snapshot,omitempty" yaml:"snapshot,omitempty"`
	Subviews           []UIKitView  `json:"subviews,omitempty" yaml:"subviews,omitempty"`
}

// TreeOutput is the top-level output for tree mode.
type TreeOutput struct {
	Views []UIKitView `json:"views" yaml:"views"`
}

// SwiftUINode represents a node in the SwiftUI view debug tree.
type SwiftUINode struct {
	Name      string        `yaml:"name"`
	Type      string        `yaml:"type,omitempty"`
	Position  *Point        `yaml:"position,omitempty"`
	Size      *Size         `yaml:"size,omitempty"`
	Value     string        `yaml:"value,omitempty"`
	Transform string        `yaml:"transform,omitempty"`
	Children  []SwiftUINode `yaml:"children,omitempty"`
}

// Size represents a 2D size (width x height).
type Size struct {
	Width  float64 `yaml:"width"`
	Height float64 `yaml:"height"`
}

// SwiftUIOutput wraps the SwiftUI tree for YAML output.
type SwiftUIOutput struct {
	Tree []SwiftUINode `yaml:"tree"`
}

// DetailOutput is the YAML output for detail mode.
type DetailOutput struct {
	UIKit   UIKitView      `yaml:"uikit"`
	SwiftUI *SwiftUIOutput `yaml:"swiftui,omitempty"`
}
