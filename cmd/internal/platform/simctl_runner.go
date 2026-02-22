package platform

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// SimctlRunner abstracts xcrun simctl operations for testability.
type SimctlRunner interface {
	ListDevices(ctx context.Context, setPath string) ([]simDevice, error)
	Clone(ctx context.Context, sourceUDID, name, setPath string) (string, error)
	Create(ctx context.Context, name, deviceType, runtime, setPath string) (string, error)
	Shutdown(ctx context.Context, udid, setPath string) error
	Delete(ctx context.Context, udid, setPath string) error

	// ListAllDevices returns raw JSON for all simulator devices (no --set filter).
	// If onlyAvailable is true, only available devices are listed.
	ListAllDevices(ctx context.Context, onlyAvailable bool) ([]byte, error)
	// ListRuntimes returns raw JSON for available runtimes.
	ListRuntimes(ctx context.Context) ([]byte, error)
	// ListDeviceTypes returns raw JSON for device types.
	ListDeviceTypes(ctx context.Context) ([]byte, error)
}

// RealSimctlRunner executes real xcrun simctl commands.
type RealSimctlRunner struct{}

func (r *RealSimctlRunner) ListDevices(ctx context.Context, setPath string) ([]simDevice, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath, "list", "devices", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list devices in set: %w", err)
	}
	return parseDevicesJSON(out)
}

func (r *RealSimctlRunner) Clone(ctx context.Context, sourceUDID, name, setPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath, "clone", sourceUDID, name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl clone: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealSimctlRunner) Create(ctx context.Context, name, deviceType, runtime, setPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath,
		"create", name, deviceType, runtime,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl create: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealSimctlRunner) Shutdown(ctx context.Context, udid, setPath string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath, "shutdown", udid).CombinedOutput()
	if err != nil {
		// "Unable to shutdown device in current state: Shutdown" means the device
		// is already shut down — treat as success.
		if strings.Contains(string(out), "current state: Shutdown") {
			return nil
		}
		return fmt.Errorf("simctl shutdown: %w\n%s", err, out)
	}
	return nil
}

func (r *RealSimctlRunner) Delete(ctx context.Context, udid, setPath string) error {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath, "delete", udid).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl delete: %w\n%s", err, out)
	}
	return nil
}

func (r *RealSimctlRunner) ListAllDevices(ctx context.Context, onlyAvailable bool) ([]byte, error) {
	args := []string{"simctl", "list", "devices"}
	if onlyAvailable {
		args = append(args, "available")
	}
	args = append(args, "--json")
	out, err := exec.CommandContext(ctx, "xcrun", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list devices: %w", err)
	}
	return out, nil
}

func (r *RealSimctlRunner) ListRuntimes(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "runtimes", "available", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list runtimes: %w", err)
	}
	return out, nil
}

func (r *RealSimctlRunner) ListDeviceTypes(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devicetypes", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list devicetypes: %w", err)
	}
	return out, nil
}
