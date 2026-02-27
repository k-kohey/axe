package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/k-kohey/axe/internal/procgroup"
)

// Screenshot captures a PNG screenshot from the given simulator device.
// It uses "xcrun simctl io screenshot" with the specified device set path.
// The caller is responsible for providing a valid booted device UDID.
func Screenshot(ctx context.Context, udid, deviceSetPath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "axe-screenshot-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpFile := filepath.Join(tmpDir, "screenshot.png")

	args := []string{"simctl"}
	if deviceSetPath != "" {
		args = append(args, "--set", deviceSetPath)
	}
	args = append(args, "io", udid, "screenshot", "--type=png", tmpFile)

	out, err := procgroup.Command(ctx, "xcrun", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("simctl screenshot: %w\n%s", err, out)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("reading screenshot: %w", err)
	}

	return data, nil
}
