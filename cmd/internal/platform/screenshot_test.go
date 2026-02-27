package platform

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestScreenshotRequiresSimctl(t *testing.T) {
	// Verify that xcrun is available (basic sanity check).
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not found")
	}

	// Use a bogus UDID so the command fails predictably.
	// This validates the argument construction without needing a running simulator.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Screenshot(ctx, "00000000-0000-0000-0000-000000000000", "/nonexistent/device-set")
	if err == nil {
		t.Fatal("expected error for invalid device, got nil")
	}
}
