package main

import (
	"github.com/spf13/cobra"
)

var serveStrict bool

var previewServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run as multi-stream IDE backend (JSON Lines protocol)",
	Long: `Run as a multi-stream IDE backend. No source file argument is needed;
	streams are managed via JSON Lines commands on stdin (AddStream/RemoveStream),
	and events (Frame/StreamStarted/StreamStopped/StreamStatus) are emitted on stdout.

	This mode is used by the VS Code / Cursor extension for real-time preview.

	Requires idb_companion (install via: brew install facebook/fb/idb-companion).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServeLogic(serveStrict)
	},
}

func init() {
	previewServeCmd.Flags().BoolVar(&serveStrict, "strict", false, "require full thunk compilation (no degraded fallback)")
	previewCmd.AddCommand(previewServeCmd)
}
