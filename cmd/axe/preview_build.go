package main

import (
	"github.com/k-kohey/axe/internal/preview"
	"github.com/spf13/cobra"
)

var previewBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the project for preview (xcodebuild phase only)",
	Long: `Run xcodebuild with the flags required for axe preview
(dynamic replacement and private imports).

This command builds the project without launching a simulator,
compiling thunks, or any other post-build steps. It's useful for
pre-warming the build cache so that subsequent preview commands
start faster.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := resolveProjectConfig()
		if err != nil {
			return err
		}
		return preview.RunBuild(pc)
	},
}

func init() {
	previewCmd.AddCommand(previewBuildCmd)
}
