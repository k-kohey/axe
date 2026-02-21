package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/view"
	"github.com/spf13/cobra"
)

var (
	viewDepth       int
	viewFrontmost   bool
	viewSwiftUI     string
	viewInteractive bool
	viewSimulator   string
)

var viewCmd = &cobra.Command{
	Use:   "view [0xADDRESS]",
	Short: "Display view hierarchy tree, or detail for a specific view",
	Long: `Without arguments, displays the UIKit view hierarchy as a tree.
With a 0x address argument, shows detailed info for that specific view.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		device := platform.ResolveSimulator(viewSimulator)
		if viewInteractive {
			return view.RunInteractive(appName, device)
		}
		if len(args) == 1 {
			return runDetail(args[0], device)
		}
		return runTree(device)
	},
}

func runTree(device string) error {
	tree, err := view.RunTree(appName, viewDepth, viewFrontmost, device)
	if err != nil {
		return err
	}
	return view.PresentTreeYAML(os.Stdout, tree)
}

func runDetail(address string, device string) error {
	if !strings.HasPrefix(address, "0x") {
		return fmt.Errorf("address must start with 0x (e.g. 0x10150e5a0)")
	}
	if viewSwiftUI != "none" && viewSwiftUI != "compact" && viewSwiftUI != "full" {
		return fmt.Errorf("--swiftui must be one of: none, compact, full")
	}

	detail, err := view.RunDetail(appName, address, viewSwiftUI, device)
	if err != nil {
		return err
	}
	return view.PresentDetailYAML(os.Stdout, detail)
}

func init() {
	viewCmd.Flags().IntVar(&viewDepth, "depth", 0, "maximum depth to display (tree mode)")
	viewCmd.Flags().BoolVar(&viewFrontmost, "frontmost", false, "show only the frontmost view controller's subtree (tree mode)")
	viewCmd.Flags().StringVar(&viewSwiftUI, "swiftui", "none",
		`SwiftUI tree display mode: none, compact, full (detail mode). Output can be large; use with yq for filtering (e.g. yq '.swiftui')`)
	viewCmd.Flags().BoolVarP(&viewInteractive, "interactive", "i", false,
		"interactive tree navigation mode (TUI)")
	viewCmd.Flags().StringVar(&viewSimulator, "simulator", "", "simulator device UDID or name")
	rootCmd.AddCommand(viewCmd)
}
