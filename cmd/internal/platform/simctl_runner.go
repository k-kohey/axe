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
}

// RealSimctlRunner executes real xcrun simctl commands.
type RealSimctlRunner struct{}

func (r *RealSimctlRunner) ListDevices(ctx context.Context, setPath string) ([]simDevice, error) {
	return listDevicesInSet(ctx, setPath)
}

func (r *RealSimctlRunner) Clone(ctx context.Context, sourceUDID, name, setPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "--set", setPath, "clone", sourceUDID, name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl clone: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealSimctlRunner) Create(ctx context.Context, name, deviceType, runtime, setPath string) (string, error) {
	return createDeviceInSet(ctx, name, deviceType, runtime, setPath)
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
