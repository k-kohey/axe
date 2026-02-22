package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeSimctlRunner is an in-memory SimctlRunner for testing DevicePool.
type fakeSimctlRunner struct {
	mu      sync.Mutex
	devices map[string]simDevice // UDID → simDevice
	nextID  int

	// Error injection.
	cloneErr    error
	createErr   error
	shutdownErr error
	deleteErr   error

	// Call tracking.
	cloneCalls    int
	createCalls   int
	shutdownCalls int
	deleteCalls   int
}

func newFakeSimctlRunner() *fakeSimctlRunner {
	return &fakeSimctlRunner{
		devices: make(map[string]simDevice),
	}
}

func (f *fakeSimctlRunner) ListDevices(_ context.Context, _ string) ([]simDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []simDevice
	for _, d := range f.devices {
		all = append(all, d)
	}
	return all, nil
}

func (f *fakeSimctlRunner) Clone(_ context.Context, sourceUDID, name, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cloneCalls++
	if f.cloneErr != nil {
		return "", f.cloneErr
	}
	source, ok := f.devices[sourceUDID]
	if !ok {
		return "", fmt.Errorf("source device %s not found", sourceUDID)
	}
	f.nextID++
	udid := fmt.Sprintf("CLONE-%d", f.nextID)
	f.devices[udid] = simDevice{
		Name:                 name,
		UDID:                 udid,
		State:                "Shutdown",
		DeviceTypeIdentifier: source.DeviceTypeIdentifier,
		RuntimeID:            source.RuntimeID,
	}
	return udid, nil
}

func (f *fakeSimctlRunner) Create(_ context.Context, name, deviceType, runtime, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextID++
	udid := fmt.Sprintf("NEW-%d", f.nextID)
	f.devices[udid] = simDevice{
		Name:                 name,
		UDID:                 udid,
		State:                "Shutdown",
		DeviceTypeIdentifier: deviceType,
		RuntimeID:            runtime,
	}
	return udid, nil
}

func (f *fakeSimctlRunner) Shutdown(_ context.Context, udid, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdownCalls++
	if f.shutdownErr != nil {
		return f.shutdownErr
	}
	if d, ok := f.devices[udid]; ok {
		d.State = "Shutdown"
		f.devices[udid] = d
	}
	return nil
}

func (f *fakeSimctlRunner) Delete(_ context.Context, udid, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.devices, udid)
	return nil
}

// addDevice adds a simulated device to the fake runner.
func (f *fakeSimctlRunner) addDevice(udid, name, deviceType, runtime, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices[udid] = simDevice{
		Name:                 name,
		UDID:                 udid,
		State:                state,
		DeviceTypeIdentifier: deviceType,
		RuntimeID:            runtime,
	}
}

const (
	testDeviceType  = "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"
	testRuntime     = "com.apple.CoreSimulator.SimRuntime.iOS-18-2"
	otherDeviceType = "com.apple.CoreSimulator.SimDeviceType.iPad-Air-M2"
	otherRuntime    = "com.apple.CoreSimulator.SimRuntime.iOS-17-0"
)

func newTestPool(t *testing.T, runner *fakeSimctlRunner) *DevicePool {
	t.Helper()
	return NewDevicePool(runner, t.TempDir())
}

func TestDevicePool_Acquire_EmptyPool_NoExisting(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid == "" {
		t.Fatal("Acquire returned empty UDID")
	}

	// Should have called Create (no existing devices to clone from).
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.createCalls != 1 {
		t.Errorf("expected 1 Create call, got %d", runner.createCalls)
	}
	if runner.cloneCalls != 0 {
		t.Errorf("expected 0 Clone calls, got %d", runner.cloneCalls)
	}
}

func TestDevicePool_Acquire_EmptyPool_ExistingDevice(t *testing.T) {
	runner := newFakeSimctlRunner()
	// Add an existing device with matching type+runtime that is currently in use (Booted).
	runner.addDevice("EXISTING-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")

	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid == "" {
		t.Fatal("Acquire returned empty UDID")
	}

	// Should have called Clone (there's an existing device of the same type).
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 1 {
		t.Errorf("expected 1 Clone call, got %d", runner.cloneCalls)
	}
	if runner.createCalls != 0 {
		t.Errorf("expected 0 Create calls, got %d", runner.createCalls)
	}
}

func TestDevicePool_Acquire_AvailableInPool(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	// Acquire and release to put a device in the pool.
	udid1, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := pool.Release(context.Background(), udid1); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Reset call counts.
	runner.mu.Lock()
	runner.cloneCalls = 0
	runner.createCalls = 0
	runner.mu.Unlock()

	// Second Acquire should reuse the pooled device.
	udid2, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid2 != udid1 {
		t.Errorf("expected reuse of %s, got %s", udid1, udid2)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 0 {
		t.Errorf("expected 0 Clone calls, got %d", runner.cloneCalls)
	}
	if runner.createCalls != 0 {
		t.Errorf("expected 0 Create calls, got %d", runner.createCalls)
	}
}

func TestDevicePool_Acquire_DifferentType_NotReused(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	// Acquire and release an iPhone.
	udid1, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := pool.Release(context.Background(), udid1); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Acquire an iPad — should not reuse the iPhone.
	udid2, err := pool.Acquire(context.Background(), otherDeviceType, otherRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid2 == udid1 {
		t.Errorf("different device type should not reuse %s", udid1)
	}
}

func TestDevicePool_Release_ReturnsToPool(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := pool.Release(context.Background(), udid); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Shutdown should have been called.
	runner.mu.Lock()
	if runner.shutdownCalls != 1 {
		t.Errorf("expected 1 Shutdown call, got %d", runner.shutdownCalls)
	}
	runner.mu.Unlock()

	// Device should be available for reuse.
	udid2, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if udid2 != udid {
		t.Errorf("expected %s to be reused, got %s", udid, udid2)
	}
}

func TestDevicePool_Release_ShutdownError(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Simulate the device being Booted (as it would be in real usage).
	runner.mu.Lock()
	if d, ok := runner.devices[udid]; ok {
		d.State = "Booted"
		runner.devices[udid] = d
	}
	runner.mu.Unlock()

	runner.shutdownErr = fmt.Errorf("simctl shutdown failed")
	err = pool.Release(context.Background(), udid)
	if err == nil {
		t.Fatal("expected error from Release, got nil")
	}

	// The device should NOT be in the pool (shutdown failed, device remains Booted).
	// Priority 2 (reuse from set) also skips it because it is not Shutdown.
	runner.shutdownErr = nil
	udid2, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire after failed Release: %v", err)
	}
	if udid2 == udid {
		t.Errorf("device with failed shutdown should not be reused")
	}
}

func TestDevicePool_Release_UnknownUDID(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	err := pool.Release(context.Background(), "UNKNOWN-UDID")
	if err == nil {
		t.Fatal("expected error releasing unknown UDID, got nil")
	}
}

func TestDevicePool_ShutdownAll(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	// Acquire two devices (different types).
	_, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	udid2, err := pool.Acquire(context.Background(), otherDeviceType, otherRuntime)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	// Release one to put it in available pool.
	if err := pool.Release(context.Background(), udid2); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Reset shutdown count after Release.
	runner.mu.Lock()
	runner.shutdownCalls = 0
	runner.mu.Unlock()

	pool.ShutdownAll(context.Background())

	// Should have shutdown both in-use and available devices.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.shutdownCalls != 2 {
		t.Errorf("expected 2 Shutdown calls, got %d", runner.shutdownCalls)
	}
}

func TestDevicePool_ConcurrentAcquire(t *testing.T) {
	runner := newFakeSimctlRunner()
	pool := newTestPool(t, runner)

	const n = 10
	var wg sync.WaitGroup
	udids := make([]string, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			udids[idx], errs[idx] = pool.Acquire(context.Background(), testDeviceType, testRuntime)
		}(i)
	}
	wg.Wait()

	// All should succeed.
	for i, err := range errs {
		if err != nil {
			t.Errorf("Acquire %d: %v", i, err)
		}
	}

	// All UDIDs should be unique.
	seen := make(map[string]bool)
	for i, udid := range udids {
		if seen[udid] {
			t.Errorf("duplicate UDID at index %d: %s", i, udid)
		}
		seen[udid] = true
	}
}

func TestDevicePool_CleanupOrphans(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Booted device (simulates a zombie).
	runner.addDevice("ZOMBIE-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")
	// Add a Shutdown device (should not be touched).
	runner.addDevice("OK-1", "axe iPhone 16 Pro (2)", testDeviceType, testRuntime, "Shutdown")

	// For the zombie, the lock file should be acquirable (no controlling process).
	// We don't create a lock file, so flock will succeed → zombie detected.

	err := pool.CleanupOrphans(context.Background())
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	// Zombie should have been shutdown.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.shutdownCalls != 1 {
		t.Errorf("expected 1 Shutdown call (zombie), got %d", runner.shutdownCalls)
	}
}

func TestDevicePool_CleanupOrphans_LockedDevice(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Booted device.
	runner.addDevice("ACTIVE-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")

	// Create and hold a lock file (simulates an active controlling process).
	lockPath := filepath.Join(setPath, "ACTIVE-1.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("creating lock file: %v", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	err = pool.CleanupOrphans(context.Background())
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	// Should NOT shutdown: device is locked by a controlling process.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.shutdownCalls != 0 {
		t.Errorf("expected 0 Shutdown calls (device is locked), got %d", runner.shutdownCalls)
	}
}

func TestDevicePool_Acquire_HoldsLockFile(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// The lock file should be held by the pool, so a non-blocking flock should fail.
	lockPath := filepath.Join(setPath, udid+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening lock file: %v", err)
	}
	defer func() { _ = f.Close() }()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		t.Error("expected flock to fail (lock should be held by pool), but it succeeded")
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
}

func TestDevicePool_Release_ReleasesLockFile(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := pool.Release(context.Background(), udid); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// After release, the lock file should be acquirable.
	lockPath := filepath.Join(setPath, udid+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Lock file may have been removed, which is also fine.
		return
	}
	defer func() { _ = f.Close() }()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		t.Errorf("expected flock to succeed after Release, but got: %v", err)
	} else {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
}

func TestDevicePool_CleanupOrphans_AcquiredDeviceNotOrphaned(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Acquire a device — this should hold the lock file.
	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Mark the device as Booted in the runner (simulates boot after acquire).
	runner.mu.Lock()
	if d, ok := runner.devices[udid]; ok {
		d.State = "Booted"
		runner.devices[udid] = d
	}
	runner.mu.Unlock()

	err = pool.CleanupOrphans(context.Background())
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	// Should NOT shutdown: device is locked by the pool (not orphaned).
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.shutdownCalls != 0 {
		t.Errorf("expected 0 Shutdown calls (device is held by pool), got %d", runner.shutdownCalls)
	}
}

func TestDevicePool_GC_Expired(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Shutdown device with an old meta file.
	runner.addDevice("OLD-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Shutdown")
	metaPath := filepath.Join(setPath, "OLD-1.meta.json")
	oldTime := time.Now().Add(-15 * 24 * time.Hour) // 15 days ago
	meta := deviceMeta{LastUsed: oldTime}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	pool.GarbageCollect(context.Background())

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.deleteCalls != 1 {
		t.Errorf("expected 1 Delete call (expired device), got %d", runner.deleteCalls)
	}
}

func TestDevicePool_GC_Recent(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Shutdown device with a recent meta file.
	runner.addDevice("RECENT-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Shutdown")
	metaPath := filepath.Join(setPath, "RECENT-1.meta.json")
	meta := deviceMeta{LastUsed: time.Now().Add(-1 * 24 * time.Hour)} // 1 day ago
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	pool.GarbageCollect(context.Background())

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.deleteCalls != 0 {
		t.Errorf("expected 0 Delete calls (recent device), got %d", runner.deleteCalls)
	}
}

func TestDevicePool_GC_NoMeta(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Shutdown device with no meta file — should not be deleted.
	runner.addDevice("NOMETA-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Shutdown")

	pool.GarbageCollect(context.Background())

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.deleteCalls != 0 {
		t.Errorf("expected 0 Delete calls (no meta file), got %d", runner.deleteCalls)
	}
}

func TestDevicePool_GC_InUseNotDeleted(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Acquire a device then write an expired meta.
	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	metaPath := filepath.Join(setPath, udid+".meta.json")
	oldTime := time.Now().Add(-15 * 24 * time.Hour)
	meta := deviceMeta{LastUsed: oldTime}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	pool.GarbageCollect(context.Background())

	// In-use devices should NOT be garbage-collected even with expired meta.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.deleteCalls != 0 {
		t.Errorf("expected 0 Delete calls (device is in-use), got %d", runner.deleteCalls)
	}
}

func TestDevicePool_Acquire_CloneError_FallbackToCreate(t *testing.T) {
	runner := newFakeSimctlRunner()
	// Add an existing device to enable clone path.
	runner.addDevice("EXISTING-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")
	runner.cloneErr = fmt.Errorf("simctl clone failed")

	pool := newTestPool(t, runner)

	// Clone fails → should fall back to Create.
	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid == "" {
		t.Fatal("Acquire returned empty UDID")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 1 {
		t.Errorf("expected 1 Clone call, got %d", runner.cloneCalls)
	}
	if runner.createCalls != 1 {
		t.Errorf("expected 1 Create call (fallback), got %d", runner.createCalls)
	}
}

func TestDevicePool_Acquire_CreateError(t *testing.T) {
	runner := newFakeSimctlRunner()
	runner.createErr = fmt.Errorf("simctl create failed")

	pool := newTestPool(t, runner)

	_, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err == nil {
		t.Fatal("expected error from Acquire, got nil")
	}
}

func TestDevicePool_Acquire_WritesMetaFile(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Meta file should exist.
	metaPath := filepath.Join(setPath, udid+".meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta file: %v", err)
	}

	var meta deviceMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parsing meta: %v", err)
	}

	// LastUsed should be recent.
	if time.Since(meta.LastUsed) > 5*time.Second {
		t.Errorf("LastUsed too old: %v", meta.LastUsed)
	}
}

func TestDevicePool_Acquire_ReusesShutdownDeviceFromSet(t *testing.T) {
	runner := newFakeSimctlRunner()
	// Add a Shutdown device with matching type+runtime in the device set.
	runner.addDevice("SHUTDOWN-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Shutdown")

	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Should reuse the existing Shutdown device without clone or create.
	if udid != "SHUTDOWN-1" {
		t.Errorf("expected SHUTDOWN-1, got %s", udid)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 0 {
		t.Errorf("expected 0 Clone calls, got %d", runner.cloneCalls)
	}
	if runner.createCalls != 0 {
		t.Errorf("expected 0 Create calls, got %d", runner.createCalls)
	}
}

func TestDevicePool_Acquire_SkipsBootedDevice_FallsBackToClone(t *testing.T) {
	runner := newFakeSimctlRunner()
	// Only a Booted device exists — cannot be reused, should fall back to clone.
	runner.addDevice("BOOTED-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")

	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid == "BOOTED-1" {
		t.Error("should not reuse a Booted device")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 1 {
		t.Errorf("expected 1 Clone call, got %d", runner.cloneCalls)
	}
}

func TestDevicePool_Acquire_SkipsLockedShutdownDevice(t *testing.T) {
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	pool := NewDevicePool(runner, setPath)

	// Add a Shutdown device with matching type+runtime.
	runner.addDevice("LOCKED-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Shutdown")

	// Hold a lock on this device (simulates another process using it).
	lockPath := filepath.Join(setPath, "LOCKED-1.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("creating lock file: %v", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Should NOT reuse the locked device; should fall back to clone.
	if udid == "LOCKED-1" {
		t.Error("should not reuse a locked device")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 1 {
		t.Errorf("expected 1 Clone call (fallback from locked device), got %d", runner.cloneCalls)
	}
}

func TestDevicePool_Acquire_SkipsShutdownDeviceWithDifferentType(t *testing.T) {
	runner := newFakeSimctlRunner()
	// Add a Shutdown device with a DIFFERENT type — should not be reused.
	runner.addDevice("IPAD-1", "axe iPad Air (1)", otherDeviceType, otherRuntime, "Shutdown")

	pool := newTestPool(t, runner)

	udid, err := pool.Acquire(context.Background(), testDeviceType, testRuntime)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if udid == "IPAD-1" {
		t.Error("should not reuse a device with different type/runtime")
	}

	// No matching device exists, so should fall through to Create.
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.cloneCalls != 0 {
		t.Errorf("expected 0 Clone calls, got %d", runner.cloneCalls)
	}
	if runner.createCalls != 1 {
		t.Errorf("expected 1 Create call, got %d", runner.createCalls)
	}
}
