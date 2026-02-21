package platform

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

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
// If the device set already contains an iPhone, it is reused. Otherwise the
// latest available iPhone from the default set is cloned.
func ResolveAxeSimulator() (udid, deviceSetPath string, err error) {
	deviceSetPath, err = AxeDeviceSetPath()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(deviceSetPath, 0o755); err != nil {
		return "", "", fmt.Errorf("creating axe device set directory: %w", err)
	}

	// Check for existing iPhone in the axe device set.
	devices, err := listDevicesInSet(deviceSetPath)
	if err != nil {
		slog.Debug("Failed to list devices in axe set, will clone", "err", err)
	}
	for _, d := range devices {
		if strings.Contains(d.Name, "iPhone") {
			slog.Info("Reusing existing axe simulator", "name", d.Name, "udid", d.UDID)
			return d.UDID, deviceSetPath, nil
		}
	}

	// No iPhone found — create one based on the latest from the default device set.
	source, runtime, err := findLatestIPhone()
	if err != nil {
		return "", "", fmt.Errorf("finding latest iPhone: %w", err)
	}

	slog.Info("Creating simulator in axe device set", "source", source.Name, "deviceType", source.DeviceTypeIdentifier, "runtime", runtime)
	createdUDID, err := createDeviceInSet("axe "+source.Name, source.DeviceTypeIdentifier, runtime, deviceSetPath)
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
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "available", "--json").Output()
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
	out, err := exec.Command("xcrun", "simctl", "--set", deviceSetPath, "list", "devices", "--json").Output()
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
	return all, nil
}

// createDeviceInSet creates a new simulator device in the specified device set.
// Returns the UDID of the newly created device.
func createDeviceInSet(name, deviceType, runtime, setPath string) (string, error) {
	out, err := exec.Command(
		"xcrun", "simctl", "--set", setPath,
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
