package preview

import (
	"slices"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/preview/protocol"
)

func TestWarmDevice_ParkAndClaim(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)
	defer sm.StopAll()

	comp := newFakeCompanion()
	sm.parkDevice("UDID-1", "iPhone-16-Pro", "iOS-18-2", comp)

	udid, boot := sm.claimWarmDevice("iPhone-16-Pro", "iOS-18-2")
	if udid != "UDID-1" {
		t.Errorf("expected UDID-1, got %q", udid)
	}
	if boot != comp {
		t.Error("expected boot companion to be the parked one")
	}

	// Boot companion should NOT have been stopped (it was claimed for reuse).
	if comp.stopped.Load() {
		t.Error("expected boot companion NOT to be stopped after claim")
	}
}

func TestWarmDevice_ClaimNonExistent(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)
	defer sm.StopAll()

	udid, boot := sm.claimWarmDevice("iPhone-16-Pro", "iOS-18-2")
	if udid != "" || boot != nil {
		t.Errorf("expected empty, got udid=%q boot=%v", udid, boot)
	}
}

func TestWarmDevice_ParkDuplicateKey(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)
	defer sm.StopAll()

	comp1 := newFakeCompanion()
	comp2 := newFakeCompanion()

	sm.parkDevice("UDID-1", "iPhone-16-Pro", "iOS-18-2", comp1)
	sm.parkDevice("UDID-2", "iPhone-16-Pro", "iOS-18-2", comp2)

	// First companion should have been shut down.
	if !comp1.stopped.Load() {
		t.Error("expected first companion to be stopped")
	}

	// Pool should have released the first device.
	pool.mu.Lock()
	found := slices.Contains(pool.released, "UDID-1")
	pool.mu.Unlock()
	if !found {
		t.Error("expected UDID-1 to be released")
	}

	// Claim should return the second device.
	udid, boot := sm.claimWarmDevice("iPhone-16-Pro", "iOS-18-2")
	if udid != "UDID-2" {
		t.Errorf("expected UDID-2, got %q", udid)
	}
	if boot != comp2 {
		t.Error("expected second companion")
	}
}

func TestWarmDevice_ShutdownWarmDevices(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)

	comp1 := newFakeCompanion()
	comp2 := newFakeCompanion()

	sm.parkDevice("UDID-1", "iPhone-16-Pro", "iOS-18-2", comp1)
	sm.parkDevice("UDID-2", "iPad-Air", "iOS-18-2", comp2)

	sm.shutdownWarmDevices()

	if !comp1.stopped.Load() {
		t.Error("expected comp1 to be stopped")
	}
	if !comp2.stopped.Load() {
		t.Error("expected comp2 to be stopped")
	}

	pool.mu.Lock()
	releasedCount := len(pool.released)
	pool.mu.Unlock()
	if releasedCount != 2 {
		t.Errorf("expected 2 releases, got %d", releasedCount)
	}

	sm.StopAll()
}

func TestWarmDevice_BootCompanionCrash(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)
	defer sm.StopAll()

	comp := newFakeCompanion()
	sm.parkDevice("UDID-1", "iPhone-16-Pro", "iOS-18-2", comp)

	// Simulate crash by closing the Done channel.
	close(comp.doneCh)

	// Wait for the goroutine to detect the crash and release the device.
	deadline := time.After(5 * time.Second)
	for {
		pool.mu.Lock()
		released := len(pool.released)
		pool.mu.Unlock()
		if released > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("warm device was not cleaned up after boot companion crash")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Claim should return nothing because the device was already cleaned up.
	udid, _ := sm.claimWarmDevice("iPhone-16-Pro", "iOS-18-2")
	if udid != "" {
		t.Errorf("expected empty after crash, got %q", udid)
	}
}

func TestWarmDevice_StoppingSkipsParking(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := newTestStreamManagerWithRunners(pool, ew)

	// Set stopping flag to simulate StopAll in progress.
	sm.stopping.Store(true)

	comp := newFakeCompanion()
	s := &stream{
		id:            "test-stream",
		deviceUDID:    "UDID-1",
		deviceType:    "iPhone-16-Pro",
		runtime:       "iOS-18-2",
		bootCompanion: comp,
		done:          make(chan struct{}),
		cancel:        func() {},
	}

	sm.cleanupStreamResources(s)

	// No warm devices should have been parked.
	sm.warmMu.Lock()
	warmCount := len(sm.warmDevices)
	sm.warmMu.Unlock()
	if warmCount != 0 {
		t.Errorf("expected 0 warm devices during stopping, got %d", warmCount)
	}

	// Boot companion should be stopped directly.
	if !comp.stopped.Load() {
		t.Error("expected boot companion to be stopped directly")
	}

	// Device should be released directly.
	pool.mu.Lock()
	released := len(pool.released)
	pool.mu.Unlock()
	if released == 0 {
		t.Error("expected pool.Release to be called directly")
	}

	sm.StopAll()
}
