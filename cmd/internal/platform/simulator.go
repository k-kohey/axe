package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
// It returns the UDID and device set path for the resolved simulator.
//
// Resolution priority:
//  1. preferredUDID (from --device flag or .axerc DEVICE) — must exist, error if not found
//  2. config.json defaultSimulator — warn and fall through if not found
//  3. First existing device in the axe set
//  4. Auto-create from the latest available iPhone
func ResolveAxeSimulator(preferredUDID string) (udid, deviceSetPath string, err error) {
	deviceSetPath, err = AxeDeviceSetPath()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(deviceSetPath, 0o755); err != nil {
		return "", "", fmt.Errorf("creating axe device set directory: %w", err)
	}

	devices, err := listDevicesInSet(deviceSetPath)
	if err != nil {
		slog.Debug("Failed to list devices in axe set, will clone", "err", err)
	}

	// Priority 1: explicit preferred UDID.
	if preferredUDID != "" {
		for _, d := range devices {
			if d.UDID == preferredUDID {
				slog.Info("Using specified simulator", "name", d.Name, "udid", d.UDID)
				return d.UDID, deviceSetPath, nil
			}
		}
		return "", "", fmt.Errorf("simulator %s not found in axe device set. Run 'axe preview simulator list' to see available devices", preferredUDID)
	}

	// Priority 2: config.json default.
	store, storeErr := NewConfigStore()
	if storeErr == nil {
		if defaultUDID, _ := store.GetDefault(); defaultUDID != "" {
			for _, d := range devices {
				if d.UDID == defaultUDID {
					slog.Info("Using default simulator", "name", d.Name, "udid", d.UDID)
					return d.UDID, deviceSetPath, nil
				}
			}
			slog.Warn("Default simulator not found, falling back to auto-select", "udid", defaultUDID)
		}
	}

	// Priority 3: reuse first existing device.
	for _, d := range devices {
		slog.Info("Reusing existing axe simulator", "name", d.Name, "udid", d.UDID)
		return d.UDID, deviceSetPath, nil
	}

	// Priority 4: auto-create from the latest iPhone.
	source, runtime, err := findLatestIPhone()
	if err != nil {
		return "", "", fmt.Errorf("finding latest iPhone: %w", err)
	}

	slog.Info("Creating simulator in axe device set", "source", source.Name, "deviceType", source.DeviceTypeIdentifier, "runtime", runtime)
	createdUDID, err := createDeviceInSet("axe "+source.Name+" (1)", source.DeviceTypeIdentifier, runtime, deviceSetPath)
	if err != nil {
		return "", "", fmt.Errorf("creating simulator: %w", err)
	}
	return createdUDID, deviceSetPath, nil
}

// findLatestIPhone selects the latest available iPhone from the default device set
// without booting it. The selection prefers the highest iOS version and, among
// devices on the same version, the lexicographically largest name.
// Returns the device and its runtime key (e.g. "com.apple.CoreSimulator.SimRuntime.iOS-18-2").
func findLatestIPhone() (simDevice, string, error) {
	ctx, cancel := simctlContext()
	defer cancel()

	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "available", "--json").Output()
	if err != nil {
		return simDevice{}, "", fmt.Errorf("simctl list devices: %w", err)
	}

	var result struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
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

// listDevicesInSet returns all devices in the given custom device set.
func listDevicesInSet(deviceSetPath string) ([]simDevice, error) {
	ctx, cancel := simctlContext()
	defer cancel()

	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", deviceSetPath, "list", "devices", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list devices in set: %w", err)
	}

	var result struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing simctl output: %w", err)
	}

	var all []simDevice
	for _, devices := range result.Devices {
		all = append(all, devices...)
	}
	// Sort by name for deterministic, user-friendly ordering (map iteration is random).
	sort.Slice(all, func(i, j int) bool {
		return all[i].Name < all[j].Name
	})
	return all, nil
}

// createDeviceInSet creates a new simulator device in the specified device set.
// Returns the UDID of the newly created device.
func createDeviceInSet(name, deviceType, runtime, setPath string) (string, error) {
	ctx, cancel := simctlContext()
	defer cancel()

	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath,
		"create", name, deviceType, runtime,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl create: %w\n%s", err, out)
	}
	// simctl create prints the new UDID on stdout.
	return strings.TrimSpace(string(out)), nil
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
