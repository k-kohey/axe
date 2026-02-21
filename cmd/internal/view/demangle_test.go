package view

import (
	"os/exec"
	"sort"
	"testing"
)

func TestDemangleNames(t *testing.T) {
	// Skip if swift is not available
	if _, err := exec.LookPath("swift"); err != nil {
		t.Skip("swift not found in PATH")
	}

	t.Run("mangles Swift names", func(t *testing.T) {
		names := []string{
			"_TtGC7SwiftUI14_UIHostingViewGVS_15ModifiedContentVS_7AnyViewVS_12RootModifier__",
		}
		result := demangleNames(names)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		mangled := "_TtGC7SwiftUI14_UIHostingViewGVS_15ModifiedContentVS_7AnyViewVS_12RootModifier__"
		demangled, ok := result[mangled]
		if !ok {
			t.Fatalf("expected demangled entry for %s", mangled)
		}
		want := "SwiftUI._UIHostingView<SwiftUI.ModifiedContent<SwiftUI.AnyView, SwiftUI.RootModifier>>"
		if demangled != want {
			t.Errorf("expected %q, got %q", want, demangled)
		}
	})

	t.Run("non-mangled names are not included", func(t *testing.T) {
		names := []string{"UIView", "UILabel", "CALayer"}
		result := demangleNames(names)
		if result != nil {
			t.Errorf("expected nil map for non-mangled names, got %v", result)
		}
	})

	t.Run("mixed names", func(t *testing.T) {
		mangled := "_TtGC7SwiftUI14_UIHostingViewGVS_15ModifiedContentVS_7AnyViewVS_12RootModifier__"
		names := []string{
			"UIView",
			mangled,
			"UILabel",
		}
		result := demangleNames(names)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if _, ok := result["UIView"]; ok {
			t.Error("UIView should not appear in demangled map")
		}
		if _, ok := result["UILabel"]; ok {
			t.Error("UILabel should not appear in demangled map")
		}
		if _, ok := result[mangled]; !ok {
			t.Errorf("expected demangled entry for %s", mangled)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := demangleNames(nil)
		if result != nil {
			t.Errorf("expected nil for empty input, got %v", result)
		}
	})
}

func TestCollectClassNames(t *testing.T) {
	data := &rawBplistData{
		Views: []rawViewNode{
			{
				Class:   "UIWindow",
				Address: "0x100",
				Layer:   map[string]string{"class": "UIWindowLayer", "address": "0x600"},
				Subviews: []rawViewNode{
					{
						Class:   "_TtSomeSwiftClass",
						Address: "0x200",
						Layer:   map[string]string{"class": "CALayer"},
					},
				},
			},
		},
		Classmap: map[string]string{
			"UIWindow":          "UIWindow/UIView/UIResponder",
			"_TtSomeSwiftClass": "_TtSomeSwiftClass/UIView",
		},
	}

	names := collectClassNames(data)
	sort.Strings(names)

	expected := []string{
		"CALayer",
		"UIResponder",
		"UIView",
		"UIWindow",
		"UIWindowLayer",
		"_TtSomeSwiftClass",
	}

	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("names[%d] = %q, want %q", i, names[i], name)
		}
	}
}
