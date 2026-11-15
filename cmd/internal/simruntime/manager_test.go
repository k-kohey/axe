package simruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/k-kohey/axe/internal/platform"
)

type failingBootSimctl struct {
	platform.SimctlRunner
}

func (failingBootSimctl) Boot(context.Context, string) error {
	return errors.New("boot failed")
}

func newTestManager(t *testing.T) (*RuntimeManager, string) {
	t.Helper()
	setPath := t.TempDir()
	simctl := failingBootSimctl{}
	return &RuntimeManager{
		deviceSetPath: setPath,
		simctl:        simctl,
		pool:          platform.NewDevicePool(simctl, setPath),
		locks:         platform.NewDeviceLocks(setPath),
		sessions:      make(map[string]*Session),
	}, setPath
}

func TestCreateSession_StartFailure_ReleasesDeviceLock(t *testing.T) {
	m, setPath := newTestManager(t)

	_, err := m.CreateSession(context.Background(), CreateSessionRequest{
		DeviceUDID: "FAIL-1",
		DeviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
		Runtime:    "com.apple.CoreSimulator.SimRuntime.iOS-18-0",
		NoHeadless: true,
	})
	if err == nil {
		t.Fatal("CreateSession: expected an error when boot fails")
	}

	if platform.NewDeviceLocks(setPath).IsHeld("FAIL-1") {
		t.Error("expected the device lock to be released after a failed start")
	}
}

func TestCleanupSession_ReleasesDeviceLock_WithTypeAndRuntime(t *testing.T) {
	// DeviceUDID wins over DeviceType/Runtime, so the session holds a plain
	// device lock and cleanup must release that lock, not go through the pool.
	m, setPath := newTestManager(t)
	req := CreateSessionRequest{
		DeviceUDID: "SINGLE-1",
		DeviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
		Runtime:    "com.apple.CoreSimulator.SimRuntime.iOS-18-0",
	}
	s := newSession("session-1", req.DeviceType, req.Runtime, func() {})

	if err := m.acquireDevice(context.Background(), s, req); err != nil {
		t.Fatalf("acquireDevice: %v", err)
	}
	other := platform.NewDeviceLocks(setPath)
	if !other.IsHeld("SINGLE-1") {
		t.Fatal("expected the device lock to be held after acquireDevice")
	}

	m.cleanupSession(context.Background(), s)

	if other.IsHeld("SINGLE-1") {
		t.Error("expected the device lock to be released after cleanupSession")
	}
}
