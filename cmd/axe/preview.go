package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview"
	"github.com/spf13/cobra"
)

// Common flags shared by preview and all its subcommands via PersistentFlags.
var (
	previewProject       string
	previewWorkspace     string
	previewScheme        string
	previewConfiguration string
	previewDevice        string
)

// Oneshot-specific flags.
var (
	previewSelector   string
	previewReuseBuild bool
	previewFullThunk  bool
)

var previewCmd = &cobra.Command{
	Use:   "preview <source-file.swift>",
	Short: "Launch a SwiftUI preview via dynamic replacement",
	Long: `Aiming to reproduce the behavior of Xcode Previews,
	this command builds the project, extracts the View body from the source file, generates a @_dynamicReplacement thunk,
	compiles it into a dylib, and launches the app on a headless simulator with the dylib injected.
	The simulator is managed automatically in axe's dedicated device set and shut down on exit.

	The project is resolved in this order:
	  1. --project / --workspace flags (highest priority)
	  2. Auto-detection: a single .xcworkspace or .xcodeproj in the current directory
	  3. PROJECT / WORKSPACE in .axerc

	By default the command runs in oneshot mode: build, launch, capture a screenshot
	to stdout (PNG), then clean up and exit. Exit 0 on success, exit 1 on failure.

	Subcommands:
	  build     — build the project (xcodebuild phase only)
	  watch     — watch a source file and hot-reload on changes
	  serve     — run as multi-stream IDE backend (JSON Lines protocol)
	  report    — capture screenshots of all #Preview blocks
	  simulator — manage simulators for preview

	Requires idb_companion (install via: brew install facebook/fb/idb-companion).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOneshotLogic(args[0])
	},
}

// resolveSourceFile validates and returns the absolute path for a source file argument.
func resolveSourceFile(sourceArg string) (string, error) {
	sourceFile, err := filepath.Abs(sourceArg)
	if err != nil {
		return "", fmt.Errorf("resolving source path: %w", err)
	}
	if _, err := os.Stat(sourceFile); err != nil {
		return "", fmt.Errorf("source file not found: %s", sourceFile)
	}
	return sourceFile, nil
}

// previewPreamble resolves project config and checks for idb_companion.
// Common setup shared by oneshot, watch, and serve modes.
func previewPreamble() (preview.ProjectConfig, error) {
	pc, err := resolveProjectConfig()
	if err != nil {
		return pc, err
	}
	if err := platform.CheckIDBCompanion(); err != nil {
		return pc, err
	}
	return pc, nil
}

// runOneshotLogic executes a single preview capture (PNG to stdout).
func runOneshotLogic(sourceArg string) error {
	pc, err := previewPreamble()
	if err != nil {
		return err
	}
	sourceFile, err := resolveSourceFile(sourceArg)
	if err != nil {
		return err
	}

	opts := preview.RunOptions{
		SourceFile:      sourceFile,
		PC:              pc,
		PreviewSelector: previewSelector,
		PreferredDevice: previewDevice,
		ReuseBuild:      previewReuseBuild,
		FullThunk:       previewFullThunk,
	}
	opts.OnReady = func(ctx context.Context, device, deviceSetPath string) error {
		data, err := platform.Screenshot(ctx, device, deviceSetPath)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}

	return preview.Run(opts)
}

// validateThunkFlags checks that incremental thunk flags have valid values.
func validateThunkFlags(maxThunkFiles, preThunkDepth int) error {
	if maxThunkFiles < 0 {
		return fmt.Errorf("--max-thunk-files must be >= 0 (0 = unlimited), got %d", maxThunkFiles)
	}
	if preThunkDepth < 0 {
		return fmt.Errorf("--pre-thunk-depth must be >= 0, got %d", preThunkDepth)
	}
	return nil
}

// runWatchLogic starts preview in watch mode with hot-reload.
func runWatchLogic(sourceArg, selector string, reuseBuild, strict, noHeadless bool, maxThunkFiles, preThunkDepth int) error {
	if err := validateThunkFlags(maxThunkFiles, preThunkDepth); err != nil {
		return err
	}

	pc, err := previewPreamble()
	if err != nil {
		return err
	}
	sourceFile, err := resolveSourceFile(sourceArg)
	if err != nil {
		return err
	}

	return preview.Run(preview.RunOptions{
		SourceFile:      sourceFile,
		PC:              pc,
		Watch:           true,
		PreviewSelector: selector,
		PreferredDevice: previewDevice,
		ReuseBuild:      reuseBuild,
		Strict:          strict,
		NoHeadless:      noHeadless,
		MaxThunkFiles:   maxThunkFiles,
		PreThunkDepth:   preThunkDepth,
	})
}

// runServeLogic starts preview in multi-stream serve mode.
func runServeLogic(strict bool, maxThunkFiles, preThunkDepth int) error {
	if err := validateThunkFlags(maxThunkFiles, preThunkDepth); err != nil {
		return err
	}
	pc, err := previewPreamble()
	if err != nil {
		return err
	}
	return preview.RunServe(pc, strict, maxThunkFiles, preThunkDepth)
}

// resolveProjectConfig resolves project settings using the following priority:
//  1. --project / --workspace flags (highest priority)
//  2. Auto-detection: a single .xcworkspace or .xcodeproj in the current directory
//  3. PROJECT / WORKSPACE in .axerc
//
// Shared by the preview command and its subcommands (e.g. report).
func resolveProjectConfig() (preview.ProjectConfig, error) {
	// Use local copies so that global flag variables are not mutated by
	// auto-detection or .axerc fallback. This prevents side effects between
	// successive calls (e.g. in tests) and keeps flag state predictable.
	project := previewProject
	workspace := previewWorkspace
	scheme := previewScheme
	configuration := previewConfiguration
	device := previewDevice

	// Priority 2: auto-detect from current directory when flags are not set.
	if project == "" && workspace == "" {
		project, workspace = platform.DetectXcodeProject()
	}

	// Priority 3: fall back to .axerc for unset flags.
	rc := platform.ReadRC()

	// Reject .axerc that declares both PROJECT and WORKSPACE — regardless of
	// whether CLI flags are set — because the file itself is misconfigured.
	if rc["PROJECT"] != "" && rc["WORKSPACE"] != "" {
		// Allow: a CLI flag already resolved the project/workspace, so the
		// conflicting .axerc values are simply ignored.
		if previewProject == "" && previewWorkspace == "" {
			return preview.ProjectConfig{}, fmt.Errorf(
				"PROJECT and WORKSPACE in .axerc are mutually exclusive; remove one of them")
		}
	}

	if project == "" && workspace == "" {
		if rc["WORKSPACE"] != "" {
			workspace = rc["WORKSPACE"]
		} else if rc["PROJECT"] != "" {
			project = rc["PROJECT"]
		}
	}
	if scheme == "" && rc["SCHEME"] != "" {
		scheme = rc["SCHEME"]
	}
	if configuration == "" && rc["CONFIGURATION"] != "" {
		configuration = rc["CONFIGURATION"]
	}
	if device == "" && rc["DEVICE"] != "" {
		device = rc["DEVICE"]
		// Write back so that subcommand logic can reference previewDevice.
		previewDevice = device
	}
	// Write back scheme so that subcommand logic can reference previewScheme.
	if previewScheme == "" && scheme != "" {
		previewScheme = scheme
	}

	if project != "" && workspace != "" {
		return preview.ProjectConfig{}, fmt.Errorf("--project and --workspace are mutually exclusive")
	}
	if project == "" && workspace == "" {
		return preview.ProjectConfig{}, fmt.Errorf("either --project or --workspace is required. Place a single .xcodeproj or .xcworkspace in the current directory, or set PROJECT/WORKSPACE in .axerc")
	}
	if scheme == "" {
		return preview.ProjectConfig{}, fmt.Errorf("--scheme is required. Use the flag or set SCHEME in .axerc")
	}

	return preview.NewProjectConfig(project, workspace, scheme, configuration)
}

func init() {
	// Common flags inherited by all subcommands.
	previewCmd.PersistentFlags().StringVar(&previewProject, "project", "", "path to .xcodeproj")
	previewCmd.PersistentFlags().StringVar(&previewWorkspace, "workspace", "", "path to .xcworkspace")
	previewCmd.PersistentFlags().StringVar(&previewScheme, "scheme", "", "Xcode scheme to build")
	previewCmd.PersistentFlags().StringVar(&previewConfiguration, "configuration", "", "build configuration (e.g. Debug, Release)")
	previewCmd.PersistentFlags().StringVar(&previewDevice, "device", "", "simulator UDID to use for preview (overrides .axerc DEVICE and global default)")

	// Oneshot-specific flags.
	previewCmd.Flags().StringVar(&previewSelector, "preview", "", "select preview by title or index (e.g. --preview \"Dark Mode\" or --preview 1)")
	previewCmd.Flags().BoolVar(&previewReuseBuild, "reuse-build", false, "skip xcodebuild and reuse artifacts from a previous build")
	previewCmd.Flags().BoolVar(&previewFullThunk, "full-thunk", false, "use full thunk compilation in oneshot mode (per-file dynamic replacement)")

	rootCmd.AddCommand(previewCmd)
}
