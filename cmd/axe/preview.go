package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview"
	"github.com/spf13/cobra"
)

var (
	previewProject       string
	previewWorkspace     string
	previewScheme        string
	previewConfiguration string
	previewWatch         bool
	previewSelector      string
	previewServe         bool
	previewDevice        string
	previewReuseBuild    bool
)

var previewCmd = &cobra.Command{
	Use:   "preview <source-file.swift>",
	Short: "Launch a SwiftUI preview via dynamic replacement",
	Long: `Aiming to reproduce the behavior of Xcode Previews,
	this command builds the project, extracts the View body from the source file, generates a @_dynamicReplacement thunk,
	compiles it into a dylib, and launches the app on a headless simulator with the dylib injected.
	The simulator is managed automatically in axe's dedicated device set and shut down on exit.

	With --serve, the command runs as a multi-stream IDE backend. No source file argument is needed;
	streams are managed via JSON Lines commands on stdin (AddStream/RemoveStream), and events
	(Frame/StreamStarted/StreamStopped/StreamStatus) are emitted on stdout.

	Requires idb_companion (install via: brew install facebook/fb/idb-companion).`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Fall back to .axerc for unset flags.
		rc := platform.ReadRC()
		if previewProject == "" && rc["PROJECT"] != "" {
			previewProject = rc["PROJECT"]
		}
		if previewWorkspace == "" && rc["WORKSPACE"] != "" {
			previewWorkspace = rc["WORKSPACE"]
		}
		if previewScheme == "" && rc["SCHEME"] != "" {
			previewScheme = rc["SCHEME"]
		}
		if previewConfiguration == "" && rc["CONFIGURATION"] != "" {
			previewConfiguration = rc["CONFIGURATION"]
		}
		if previewDevice == "" && rc["DEVICE"] != "" {
			previewDevice = rc["DEVICE"]
		}

		if previewProject != "" && previewWorkspace != "" {
			return fmt.Errorf("--project and --workspace are mutually exclusive")
		}
		if previewProject == "" && previewWorkspace == "" {
			return fmt.Errorf("either --project or --workspace is required. Use flags or set PROJECT/WORKSPACE in .axerc")
		}
		if previewScheme == "" {
			return fmt.Errorf("--scheme is required. Use the flag or set SCHEME in .axerc")
		}

		pc, err := preview.NewProjectConfig(previewProject, previewWorkspace, previewScheme, previewConfiguration)
		if err != nil {
			return err
		}

		// idb_companion is always required (headless boot + serve mode).
		if err := platform.CheckIDBCompanion(); err != nil {
			return err
		}

		// Multi-stream serve mode: source file comes via AddStream commands on stdin.
		if previewServe {
			return preview.RunServe(pc)
		}

		// Single-stream mode requires a source file argument.
		if len(args) == 0 {
			return fmt.Errorf("source file argument is required (or use --serve for multi-stream mode)")
		}
		sourceFile, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("resolving source path: %w", err)
		}
		if _, err := os.Stat(sourceFile); err != nil {
			return fmt.Errorf("source file not found: %s", sourceFile)
		}

		return preview.Run(sourceFile, pc, previewWatch, previewSelector, previewServe, previewDevice, previewReuseBuild)
	},
}

func init() {
	previewCmd.Flags().StringVar(&previewProject, "project", "", "path to .xcodeproj")
	previewCmd.Flags().StringVar(&previewWorkspace, "workspace", "", "path to .xcworkspace")
	previewCmd.Flags().StringVar(&previewScheme, "scheme", "", "Xcode scheme to build")
	previewCmd.Flags().BoolVar(&previewWatch, "watch", false, "watch source file for changes and reload")
	previewCmd.Flags().StringVar(&previewConfiguration, "configuration", "", "build configuration (e.g. Debug, Release)")
	previewCmd.Flags().StringVar(&previewSelector, "preview", "", "select preview by title or index (e.g. --preview \"Dark Mode\" or --preview 1)")
	previewCmd.Flags().BoolVar(&previewServe, "serve", false, "run as IDE backend: stream video via idb, accept JSON commands on stdin (requires idb_companion)")
	previewCmd.Flags().StringVar(&previewDevice, "device", "", "simulator UDID to use for preview (overrides .axerc DEVICE and global default)")
	previewCmd.Flags().BoolVar(&previewReuseBuild, "reuse-build", false, "skip xcodebuild and reuse artifacts from a previous build")
	rootCmd.AddCommand(previewCmd)
}
