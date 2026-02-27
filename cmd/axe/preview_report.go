package main

import (
	"fmt"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview"
	"github.com/spf13/cobra"
)

var (
	reportOutput string
	reportWait   time.Duration
)

var previewReportCmd = &cobra.Command{
	Use:   "report <file.swift> [file.swift...]",
	Short: "Capture screenshots of all #Preview blocks in the given Swift files",
	Long: `Capture screenshots for each #Preview block found in the specified Swift source files.

	When --output is a directory, each screenshot is saved as <basename>--preview-<index>.png.
	When --output is a file path (has extension), exactly one preview across all files is required.

	Examples:
	  axe preview report Sources/FooView.swift --output ./screenshots/
	  axe preview report Sources/FooView.swift --output ./out.png
	  axe preview report Sources/FooView.swift Sources/BarView.swift --output ./screenshots/`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := resolveProjectConfig()
		if err != nil {
			return err
		}

		if reportOutput == "" {
			return fmt.Errorf("--output is required")
		}

		if err := platform.CheckIDBCompanion(); err != nil {
			return err
		}

		return preview.RunReport(preview.ReportOptions{
			Files:       args,
			Output:      reportOutput,
			RenderDelay: reportWait,
			PC:          pc,
			Device:      previewDevice,
		})
	},
}

func init() {
	previewReportCmd.Flags().StringVarP(&reportOutput, "output", "o", "", "output path (directory or file)")
	previewReportCmd.Flags().DurationVar(&reportWait, "wait", 10*time.Second, "rendering delay before screenshot capture")
	previewCmd.AddCommand(previewReportCmd)
}
