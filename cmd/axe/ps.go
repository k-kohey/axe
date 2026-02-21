package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running app processes on iOS simulators",
	Long:  `Lists app processes running on booted iOS simulators with their PID, app name, device UDID, and device name.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		procs, err := platform.ListSimulatorProcesses()
		if err != nil {
			return err
		}

		if len(procs) == 0 {
			fmt.Println("No app processes found on booted simulators.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "PID\tAPP\tBUNDLE ID\tDEVICE\tUDID")
		for _, p := range procs {
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", p.PID, p.App, p.BundleID, p.DeviceName, p.DeviceUDID)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(psCmd)
}
