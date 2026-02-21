package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/spf13/cobra"
)

var simulatorCmd = &cobra.Command{
	Use:   "simulator",
	Short: "Manage simulators for preview",
	Long:  `Add, remove, and configure simulators used by axe preview.`,
}

// --- list ---

var (
	simulatorListAvailable bool
	simulatorListJSON      bool
)

var simulatorListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed simulators or available device types",
	RunE:  runSimulatorList,
}

func runSimulatorList(cmd *cobra.Command, args []string) error {
	if simulatorListAvailable {
		return runSimulatorListAvailable()
	}
	return runSimulatorListManaged()
}

func runSimulatorListManaged() error {
	store, err := platform.NewConfigStore()
	if err != nil {
		return err
	}

	managed, err := platform.ListManaged(store)
	if err != nil {
		return err
	}

	if simulatorListJSON {
		// Ensure empty list is [] not null.
		if managed == nil {
			managed = []platform.ManagedSimulator{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(managed)
	}

	if len(managed) == 0 {
		fmt.Println("No managed simulators. Use 'axe preview simulator add' to create one.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "  \tUDID\tNAME\tRUNTIME\tSTATE")
	for _, s := range managed {
		marker := " "
		if s.IsDefault {
			marker = "*"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", marker, s.UDID, s.Name, s.Runtime, s.State)
	}
	return w.Flush()
}

func runSimulatorListAvailable() error {
	available, err := platform.ListAvailable()
	if err != nil {
		return err
	}

	if simulatorListJSON {
		if available == nil {
			available = []platform.AvailableDeviceType{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(available)
	}

	if len(available) == 0 {
		fmt.Println("No available device types found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "DEVICE TYPE\tIDENTIFIER\tRUNTIMES")
	for _, dt := range available {
		runtimes := make([]string, len(dt.Runtimes))
		for i, r := range dt.Runtimes {
			runtimes[i] = r.Name
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", dt.Name, dt.Identifier, joinShort(runtimes, 3))
	}
	return w.Flush()
}

// joinShort joins up to maxN strings with ", " and appends "..." if truncated.
func joinShort(ss []string, maxN int) string {
	if len(ss) <= maxN {
		result := ""
		for i, s := range ss {
			if i > 0 {
				result += ", "
			}
			result += s
		}
		return result
	}
	result := ""
	for i := 0; i < maxN; i++ {
		if i > 0 {
			result += ", "
		}
		result += ss[i]
	}
	return result + fmt.Sprintf(", ... (+%d)", len(ss)-maxN)
}

// --- add ---

var (
	simulatorAddDeviceType string
	simulatorAddRuntime    string
	simulatorAddSetDefault bool
	simulatorAddJSON       bool
)

var simulatorAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new simulator for preview",
	Long: `Create a new simulator in axe's device set.

Use 'axe preview simulator list --available' to find device type and runtime identifiers.

Example:
  axe preview simulator add \
    --device-type com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro \
    --runtime com.apple.CoreSimulator.SimRuntime.iOS-18-2`,
	RunE: runSimulatorAdd,
}

func runSimulatorAdd(cmd *cobra.Command, args []string) error {
	store, err := platform.NewConfigStore()
	if err != nil {
		return err
	}

	sim, err := platform.Add(simulatorAddDeviceType, simulatorAddRuntime, simulatorAddSetDefault, store)
	if err != nil {
		return err
	}

	if simulatorAddJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sim)
	}

	fmt.Printf("Created simulator: %s (%s)\n", sim.Name, sim.UDID)
	if sim.IsDefault {
		fmt.Println("Set as default simulator.")
	}
	return nil
}

// --- remove ---

var simulatorRemoveCmd = &cobra.Command{
	Use:   "remove <udid>",
	Short: "Remove a managed simulator",
	Args:  cobra.ExactArgs(1),
	RunE:  runSimulatorRemove,
}

func runSimulatorRemove(cmd *cobra.Command, args []string) error {
	store, err := platform.NewConfigStore()
	if err != nil {
		return err
	}

	udid := args[0]
	if err := platform.Remove(udid, store); err != nil {
		return err
	}

	fmt.Printf("Removed simulator: %s\n", udid)
	return nil
}

// --- default ---

var (
	simulatorDefaultClear bool
	simulatorDefaultJSON  bool
)

var simulatorDefaultCmd = &cobra.Command{
	Use:   "default [udid]",
	Short: "Get or set the default simulator",
	Long: `Without arguments, shows the current default simulator.
With a UDID argument, sets it as the default.
Use --clear to remove the default setting.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSimulatorDefault,
}

func runSimulatorDefault(cmd *cobra.Command, args []string) error {
	store, err := platform.NewConfigStore()
	if err != nil {
		return err
	}

	if simulatorDefaultClear {
		if err := store.ClearDefault(); err != nil {
			return err
		}
		fmt.Println("Default simulator cleared.")
		return nil
	}

	if len(args) == 1 {
		// Set default.
		udid := args[0]

		// Verify the UDID exists in the axe device set.
		managed, err := platform.ListManaged(store)
		if err != nil {
			return err
		}
		found := false
		var name string
		for _, s := range managed {
			if s.UDID == udid {
				found = true
				name = s.Name
				break
			}
		}
		if !found {
			return fmt.Errorf("simulator %s not found. Run 'axe preview simulator list' to see managed simulators", udid)
		}

		if err := store.SetDefault(udid); err != nil {
			return err
		}
		fmt.Printf("Default simulator set to: %s (%s)\n", name, udid)
		return nil
	}

	// Get current default.
	defaultUDID, err := store.GetDefault()
	if err != nil {
		return err
	}
	if defaultUDID == "" {
		fmt.Println("No default simulator set.")
		return nil
	}

	if simulatorDefaultJSON {
		managed, _ := platform.ListManaged(store)
		for _, s := range managed {
			if s.UDID == defaultUDID {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
		}
		// Default points to missing device.
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"udid": defaultUDID, "name": "(not found)"})
	}

	// Try to find the name.
	managed, _ := platform.ListManaged(store)
	for _, s := range managed {
		if s.UDID == defaultUDID {
			fmt.Printf("%s (%s)\n", s.Name, s.UDID)
			return nil
		}
	}
	fmt.Printf("%s (not found in device set)\n", defaultUDID)
	return nil
}

func init() {
	simulatorListCmd.Flags().BoolVar(&simulatorListAvailable, "available", false, "list available device types instead of managed simulators")
	simulatorListCmd.Flags().BoolVar(&simulatorListJSON, "json", false, "output as JSON")

	simulatorAddCmd.Flags().StringVar(&simulatorAddDeviceType, "device-type", "", "device type identifier (required)")
	simulatorAddCmd.Flags().StringVar(&simulatorAddRuntime, "runtime", "", "runtime identifier (required)")
	simulatorAddCmd.Flags().BoolVar(&simulatorAddSetDefault, "set-default", false, "set as default after creation")
	simulatorAddCmd.Flags().BoolVar(&simulatorAddJSON, "json", false, "output as JSON")
	_ = simulatorAddCmd.MarkFlagRequired("device-type")
	_ = simulatorAddCmd.MarkFlagRequired("runtime")

	simulatorDefaultCmd.Flags().BoolVar(&simulatorDefaultClear, "clear", false, "clear the default simulator")
	simulatorDefaultCmd.Flags().BoolVar(&simulatorDefaultJSON, "json", false, "output as JSON")

	simulatorCmd.AddCommand(simulatorListCmd, simulatorAddCmd, simulatorRemoveCmd, simulatorDefaultCmd)
	previewCmd.AddCommand(simulatorCmd)
}
