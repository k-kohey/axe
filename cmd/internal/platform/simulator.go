package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// simctlContext returns a context with a 30-second timeout for simctl calls.
func simctlContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

type simDevice struct {
	Name                 string `json:"name"`
	UDID                 string `json:"udid"`
	State                string `json:"state"`
	DeviceTypeIdentifier string `json:"deviceTypeIdentifier"`
	RuntimeID            string `json:"-"` // populated by listDevicesInSet, not part of simctl JSON
}

// ResolveSimulator returns the simulator device identifier to use with simctl.
// If flagValue is non-empty, returns it as-is. Otherwise returns "booted".
func ResolveSimulator(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return "booted"
}

// AxeDeviceSetPath returns the path to axe's dedicated simulator device set.
func AxeDeviceSetPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Developer", "axe", "Simulator Devices"), nil
}

// ResolveAxeSimulator finds or creates a simulator in the axe device set.
// It returns the UDID, device set path, and whether the device is external
// (belongs to the standard Xcode simulator set rather than the axe set).
//
// Resolution priority:
//  1. preferredUDID (from --device flag) — search axe set first, then standard set
//  2. config.json defaultSimulator — Shutdown only; skip if Booted or absent
//  3. First Shutdown device in the axe set
//  4. Auto-create from the latest available iPhone
//
// When a device is found in the standard set (isExternal=true), deviceSetPath is
// returned as "" so that downstream simctl commands target the default set.
// External devices are not shut down by axe on exit because the user may be
// using them in other workflows (e.g. Xcode).
//
// NOTE: Race condition with concurrent processes
// This function is not protected by a lock. When multiple axe preview processes
// start simultaneously, the following races may occur:
//   - Two processes both see a Shutdown device and select it before either boots it.
//     The later boot will fail or the app will be overwritten on the same simulator.
//   - Two processes both reach priority 4, finding no Shutdown devices, and each
//     creates a new simulator. This results in duplicate devices but both work correctly.
//
// A proper fix would require either:
//
//	(A) Holding a file lock that spans selection through boot (simctl boot), or
//	(B) A claim-file mechanism (e.g. devices/<udid>/.axe-claimed) checked before selection.
//
// Both add complexity and startup latency; the current behavior is acceptable for typical
// usage since duplicate creation is harmless and same-device collision is unlikely in practice.
func ResolveAxeSimulator(simctl SimctlRunner, preferredUDID string) (udid, deviceSetPath string, isExternal bool, err error) {
	deviceSetPath, err = AxeDeviceSetPath()
	if err != nil {
		return "", "", false, err
	}
	if err := os.MkdirAll(deviceSetPath, 0o755); err != nil {
		return "", "", false, fmt.Errorf("creating axe device set directory: %w", err)
	}

	listCtx, listCancel := simctlContext()
	defer listCancel()
	devices, err := simctl.ListDevices(listCtx, deviceSetPath)
	if err != nil {
		slog.Debug("Failed to list devices in axe set, will clone", "err", err)
	}

	// Priority 1: explicit preferred UDID.
	if preferredUDID != "" {
		for _, d := range devices {
			if d.UDID == preferredUDID {
				slog.Info("Using specified simulator", "name", d.Name, "udid", d.UDID)
				return d.UDID, deviceSetPath, false, nil
			}
		}

		// Fallback: search the standard Xcode simulator set.
		stdCtx, stdCancel := simctlContext()
		defer stdCancel()
		stdJSON, listErr := simctl.ListAllDevices(stdCtx, true)
		if listErr != nil {
			slog.Warn("Failed to list standard Xcode simulator set", "err", listErr)
		} else {
			stdDevices, parseErr := parseDevicesJSON(stdJSON)
			if parseErr != nil {
				slog.Warn("Failed to parse standard Xcode simulator set", "err", parseErr)
			} else {
				for _, d := range stdDevices {
					if d.UDID == preferredUDID {
						slog.Info("Using simulator from standard Xcode set", "name", d.Name, "udid", d.UDID)
						return d.UDID, "", true, nil
					}
				}
			}
		}

		return "", "", false, fmt.Errorf("simulator %s not found in axe device set or standard Xcode simulator set. Run 'axe preview simulator list' or 'xcrun simctl list devices' to see available devices", preferredUDID)
	}

	// Priority 2-3: pick a Shutdown simulator (config default preferred, then any).
	var defaultUDID string
	store, storeErr := NewConfigStore()
	if storeErr == nil {
		defaultUDID, _ = store.GetDefault()
	}

	if selected, ok := selectAvailableSimulator(devices, defaultUDID); ok {
		slog.Info("Using simulator", "udid", selected)
		return selected, deviceSetPath, false, nil
	}

	// Priority 4: auto-create from the latest iPhone.
	source, runtime, err := findLatestIPhone(simctl)
	if err != nil {
		return "", "", false, fmt.Errorf("finding latest iPhone: %w", err)
	}

	slog.Info("Creating simulator in axe device set", "source", source.Name, "deviceType", source.DeviceTypeIdentifier, "runtime", runtime)
	createCtx, createCancel := simctlContext()
	defer createCancel()
	createdUDID, err := simctl.Create(createCtx, "axe "+source.Name+" (1)", source.DeviceTypeIdentifier, runtime, deviceSetPath)
	if err != nil {
		return "", "", false, fmt.Errorf("creating simulator: %w", err)
	}
	return createdUDID, deviceSetPath, false, nil
}

// selectAvailableSimulator picks a Shutdown simulator from devices.
// defaultUDID is tried first; if it is Booted or absent, other Shutdown devices
// are checked. Returns ("", false) if no Shutdown device is available.
func selectAvailableSimulator(devices []simDevice, defaultUDID string) (string, bool) {
	// Prefer the configured default if it is Shutdown.
	if defaultUDID != "" {
		found := false
		for _, d := range devices {
			if d.UDID == defaultUDID {
				found = true
				if d.State == "Shutdown" {
					return d.UDID, true
				}
				slog.Warn("Default simulator is in use, selecting another", "udid", defaultUDID, "state", d.State)
				break
			}
		}
		if !found {
			slog.Warn("Default simulator not found in device set, falling back to auto-select", "udid", defaultUDID)
		}
	}

	// Fall back to the first Shutdown device.
	for _, d := range devices {
		if d.State == "Shutdown" {
			return d.UDID, true
		}
	}
	return "", false
}

// FindDefaultDeviceSpec returns the device type and runtime identifiers
// for the latest available iPhone. Used by DevicePool.Acquire in report mode.
func FindDefaultDeviceSpec(simctl SimctlRunner) (deviceType, runtime string, err error) {
	dev, rt, err := findLatestIPhone(simctl)
	if err != nil {
		return "", "", err
	}
	return dev.DeviceTypeIdentifier, rt, nil
}

// findLatestIPhone selects the latest available iPhone from the default device set
// without booting it. The selection prefers the highest iOS version and, among
// devices on the same version, the lexicographically largest name.
// Returns the device and its runtime key (e.g. "com.apple.CoreSimulator.SimRuntime.iOS-18-2").
func findLatestIPhone(simctl SimctlRunner) (simDevice, string, error) {
	ctx, cancel := simctlContext()
	defer cancel()

	out, err := simctl.ListAllDevices(ctx, true)
	if err != nil {
		return simDevice{}, "", fmt.Errorf("listing available devices: %w", err)
	}

	return selectLatestIPhone(out)
}

// selectLatestIPhone parses simctl JSON output and selects the best iPhone device.
// Exported for testing.
func selectLatestIPhone(jsonData []byte) (simDevice, string, error) {
	var result struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(jsonData, &result); err != nil {
		return simDevice{}, "", fmt.Errorf("parsing simctl output: %w", err)
	}

	var best simDevice
	var bestRuntime string
	var bestVersion [2]int
	for runtime, devices := range result.Devices {
		major, minor := parseIOSVersion(runtime)
		if major < 0 {
			continue
		}
		for _, d := range devices {
			if !strings.Contains(d.Name, "iPhone") {
				continue
			}
			v := [2]int{major, minor}
			if v[0] > bestVersion[0] || (v[0] == bestVersion[0] && v[1] > bestVersion[1]) ||
				(v == bestVersion && d.Name > best.Name) {
				best = d
				bestRuntime = runtime
				bestVersion = v
			}
		}
	}

	if best.UDID == "" {
		return simDevice{}, "", fmt.Errorf("no available iPhone simulator found")
	}
	return best, bestRuntime, nil
}

// parseDevicesJSON parses simctl "list devices --json" output into a flat slice of simDevice.
func parseDevicesJSON(data []byte) ([]simDevice, error) {
	var result struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing simctl output: %w", err)
	}

	var all []simDevice
	for runtime, devices := range result.Devices {
		for i := range devices {
			devices[i].RuntimeID = runtime
		}
		all = append(all, devices...)
	}
	// Sort by name for deterministic, user-friendly ordering (map iteration is random).
	sort.Slice(all, func(i, j int) bool {
		return all[i].Name < all[j].Name
	})
	return all, nil
}

// iosVersionRe extracts major and minor version from a simctl runtime key
// like "com.apple.CoreSimulator.SimRuntime.iOS-18-2".
var iosVersionRe = regexp.MustCompile(`iOS-(\d+)-(\d+)`)

// parseIOSVersion extracts the numeric iOS version from a simctl runtime key.
// Returns (-1, -1) if the key does not represent an iOS runtime.
func parseIOSVersion(runtime string) (major, minor int) {
	m := iosVersionRe.FindStringSubmatch(runtime)
	if m == nil {
		return -1, -1
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor
}
