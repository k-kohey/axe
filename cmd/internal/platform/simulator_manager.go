package platform

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

// ManagedSimulator represents a simulator in the axe device set.
type ManagedSimulator struct {
	UDID      string `json:"udid"`
	Name      string `json:"name"`
	Runtime   string `json:"runtime"`   // human-readable, e.g. "iOS 18.2"
	RuntimeID string `json:"runtimeId"` // e.g. "com.apple.CoreSimulator.SimRuntime.iOS-18-2"
	State     string `json:"state"`     // "Shutdown" | "Booted"
	IsDefault bool   `json:"isDefault"`
}

// AvailableDeviceType represents a device type that can be added.
type AvailableDeviceType struct {
	Identifier string             `json:"identifier"`
	Name       string             `json:"name"`
	Runtimes   []AvailableRuntime `json:"runtimes"`
}

// AvailableRuntime represents an available runtime for a device type.
type AvailableRuntime struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

// ListManaged returns all simulators in the axe device set.
func ListManaged(simctl SimctlRunner, store *ConfigStore) ([]ManagedSimulator, error) {
	deviceSetPath, err := AxeDeviceSetPath()
	if err != nil {
		return nil, err
	}

	ctx, cancel := simctlContext()
	defer cancel()

	devices, err := simctl.ListDevices(ctx, deviceSetPath)
	if err != nil {
		// Device set may not exist yet — return empty list rather than error.
		slog.Debug("Failed to list devices in axe set", "err", err)
		return nil, nil
	}

	defaultUDID, _ := store.GetDefault()

	var managed []ManagedSimulator
	for _, d := range devices {
		managed = append(managed, ManagedSimulator{
			UDID:      d.UDID,
			Name:      d.Name,
			Runtime:   humanReadableRuntime(d.RuntimeID),
			RuntimeID: d.RuntimeID,
			State:     d.State,
			IsDefault: d.UDID == defaultUDID,
		})
	}
	return managed, nil
}

// humanReadableRuntime converts a runtime identifier like
// "com.apple.CoreSimulator.SimRuntime.iOS-18-2" to "iOS 18.2".
func humanReadableRuntime(runtime string) string {
	// Pattern: com.apple.CoreSimulator.SimRuntime.<Platform>-<Major>-<Minor>
	parts := strings.Split(runtime, ".")
	if len(parts) == 0 {
		return runtime
	}
	last := parts[len(parts)-1]
	// "iOS-18-2" → "iOS 18.2"
	segments := strings.SplitN(last, "-", 2)
	if len(segments) < 2 {
		return runtime
	}
	platform := segments[0]
	version := strings.ReplaceAll(segments[1], "-", ".")
	return platform + " " + version
}

// ListAvailable returns device types with their compatible runtimes.
func ListAvailable(simctl SimctlRunner) ([]AvailableDeviceType, error) {
	ctx, cancel := simctlContext()
	defer cancel()

	runtimesOut, err := simctl.ListRuntimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing runtimes: %w", err)
	}

	deviceTypesOut, err := simctl.ListDeviceTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing devicetypes: %w", err)
	}

	return parseAvailable(runtimesOut, deviceTypesOut)
}

// parseAvailable builds the AvailableDeviceType list from simctl JSON outputs.
// Exported for testing.
func parseAvailable(runtimesJSON, deviceTypesJSON []byte) ([]AvailableDeviceType, error) {
	var runtimesResult struct {
		Runtimes []struct {
			Identifier           string `json:"identifier"`
			Name                 string `json:"name"`
			SupportedDeviceTypes []struct {
				Identifier string `json:"identifier"`
			} `json:"supportedDeviceTypes"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(runtimesJSON, &runtimesResult); err != nil {
		return nil, fmt.Errorf("parsing runtimes JSON: %w", err)
	}

	// Build reverse map: deviceType identifier → []AvailableRuntime
	dtRuntimes := make(map[string][]AvailableRuntime)
	for _, rt := range runtimesResult.Runtimes {
		ar := AvailableRuntime{Identifier: rt.Identifier, Name: rt.Name}
		for _, sdt := range rt.SupportedDeviceTypes {
			dtRuntimes[sdt.Identifier] = append(dtRuntimes[sdt.Identifier], ar)
		}
	}

	var deviceTypesResult struct {
		DeviceTypes []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal(deviceTypesJSON, &deviceTypesResult); err != nil {
		return nil, fmt.Errorf("parsing devicetypes JSON: %w", err)
	}

	var result []AvailableDeviceType
	for _, dt := range deviceTypesResult.DeviceTypes {
		runtimes := dtRuntimes[dt.Identifier]
		if len(runtimes) == 0 {
			continue // skip device types with no available runtimes
		}
		result = append(result, AvailableDeviceType{
			Identifier: dt.Identifier,
			Name:       dt.Name,
			Runtimes:   runtimes,
		})
	}
	return result, nil
}

// Add creates a new simulator in the axe device set.
// It generates a sequential name like "axe iPhone 16 Pro (1)".
func Add(simctl SimctlRunner, deviceType, runtime string, setDefault bool, store *ConfigStore) (ManagedSimulator, error) {
	deviceSetPath, err := AxeDeviceSetPath()
	if err != nil {
		return ManagedSimulator{}, err
	}

	// Look up the human-readable name for this device type.
	baseName := deviceTypeBaseName(simctl, deviceType)

	// Determine the next sequence number.
	existing, _ := ListManaged(simctl, store)
	seq := nextSequenceNumber(existing, baseName)
	name := fmt.Sprintf("axe %s (%d)", baseName, seq)

	createCtx, createCancel := simctlContext()
	defer createCancel()
	udid, err := simctl.Create(createCtx, name, deviceType, runtime, deviceSetPath)
	if err != nil {
		return ManagedSimulator{}, fmt.Errorf("creating simulator: %w", err)
	}

	runtimeName := humanReadableRuntime(runtime)

	if setDefault {
		if err := store.SetDefault(udid); err != nil {
			slog.Warn("Failed to set default simulator", "err", err)
		}
	}

	isDefault, _ := store.GetDefault()

	return ManagedSimulator{
		UDID:      udid,
		Name:      name,
		Runtime:   runtimeName,
		RuntimeID: runtime,
		State:     "Shutdown",
		IsDefault: udid == isDefault,
	}, nil
}

// Remove deletes a simulator from the axe device set.
// Returns an error if the simulator is currently booted.
func Remove(simctl SimctlRunner, udid string, store *ConfigStore) error {
	deviceSetPath, err := AxeDeviceSetPath()
	if err != nil {
		return err
	}

	// Check if the device exists and its state.
	listCtx, listCancel := simctlContext()
	defer listCancel()
	devices, err := simctl.ListDevices(listCtx, deviceSetPath)
	if err != nil {
		return fmt.Errorf("listing devices: %w", err)
	}

	var found *simDevice
	for _, d := range devices {
		if d.UDID == udid {
			found = &d
			break
		}
	}
	if found == nil {
		return fmt.Errorf("simulator %s not found in axe device set", udid)
	}
	if found.State == "Booted" {
		return fmt.Errorf("simulator %s (%s) is currently booted; shut it down first", udid, found.Name)
	}

	deleteCtx, deleteCancel := simctlContext()
	defer deleteCancel()
	if err := simctl.Delete(deleteCtx, udid, deviceSetPath); err != nil {
		return err
	}

	// Clear default if this was the default.
	if defaultUDID, _ := store.GetDefault(); defaultUDID == udid {
		if err := store.ClearDefault(); err != nil {
			slog.Warn("Failed to clear default after removing simulator", "err", err)
		}
	}

	return nil
}

// sequenceRe matches the "(N)" suffix in device names like "axe iPhone 16 Pro (2)".
var sequenceRe = regexp.MustCompile(`\((\d+)\)\s*$`)

// nextSequenceFromNames finds the highest (N) among names that start with
// "axe <baseName> (" and returns max+1. Returns 1 if none match.
func nextSequenceFromNames(names []string, baseName string) int {
	prefix := "axe " + baseName + " ("
	maxN := 0
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		m := sequenceRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	return maxN + 1
}

// nextSequenceNumber finds the highest (N) among managed devices whose name
// starts with "axe <baseName>" and returns max+1. Returns 1 if none exist.
func nextSequenceNumber(devices []ManagedSimulator, baseName string) int {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	return nextSequenceFromNames(names, baseName)
}

// deviceTypeBaseName extracts the human-readable device name from a device type
// identifier like "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro".
// It queries simctl list devicetypes --json for the name. Falls back to parsing
// the identifier suffix if the query fails.
func deviceTypeBaseName(simctl SimctlRunner, identifier string) string {
	ctx, cancel := simctlContext()
	defer cancel()

	out, err := simctl.ListDeviceTypes(ctx)
	if err == nil {
		var result struct {
			DeviceTypes []struct {
				Identifier string `json:"identifier"`
				Name       string `json:"name"`
			} `json:"devicetypes"`
		}
		if json.Unmarshal(out, &result) == nil {
			for _, dt := range result.DeviceTypes {
				if dt.Identifier == identifier {
					return dt.Name
				}
			}
		}
	}

	// Fallback: parse the identifier suffix.
	// "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro" → "iPhone 16 Pro"
	parts := strings.Split(identifier, ".")
	last := parts[len(parts)-1]
	return strings.ReplaceAll(last, "-", " ")
}
