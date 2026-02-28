package main

import (
	"github.com/spf13/cobra"
)

var (
	watchSelector   string
	watchReuseBuild bool
	watchStrict     bool
	watchHeadless bool
)

var previewWatchCmd = &cobra.Command{
	Use:   "watch <source-file.swift>",
	Short: "Watch a source file and hot-reload on changes",
	Long: `Watch the specified Swift source file for changes and hot-reload the preview.

	When the file body changes (computed properties, methods), the view is hot-reloaded
	without a full rebuild. Structural changes (stored properties, type signatures) trigger
	an automatic full rebuild.

	Requires idb_companion (install via: brew install facebook/fb/idb-companion).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWatchLogic(args[0], watchSelector, watchReuseBuild, watchStrict, !watchHeadless)
	},
}

func init() {
	previewWatchCmd.Flags().StringVar(&watchSelector, "preview", "", "select preview by title or index (e.g. --preview \"Dark Mode\" or --preview 1)")
	previewWatchCmd.Flags().BoolVar(&watchReuseBuild, "reuse-build", false, "skip xcodebuild and reuse artifacts from a previous build")
	previewWatchCmd.Flags().BoolVar(&watchStrict, "strict", false, "require full thunk compilation (no degraded fallback)")
	previewWatchCmd.Flags().BoolVar(&watchHeadless, "headless", false, "run simulator headlessly without a display window")
	previewCmd.AddCommand(previewWatchCmd)
}
