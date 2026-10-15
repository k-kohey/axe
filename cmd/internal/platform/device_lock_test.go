package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDeviceLocks_Lock_HoldsFlock(t *testing.T) {
	setPath := t.TempDir()
	locks := NewDeviceLocks(setPath)

	if err := locks.Lock("LOCKED-1"); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	// A non-blocking flock from another descriptor should fail while held.
	f, err := os.OpenFile(filepath.Join(setPath, "LOCKED-1.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening lock file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Error("expected flock to fail while the lock is held, but it succeeded")
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	if !locks.IsHeld("LOCKED-1") {
		t.Error("IsHeld: expected true while the lock is held")
	}
}

func TestDeviceLocks_Lock_AlreadyHeld(t *testing.T) {
	setPath := t.TempDir()

	// Another process already holds the device.
	f, err := os.OpenFile(filepath.Join(setPath, "BUSY-1.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("creating lock file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("acquiring lock: %v", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	locks := NewDeviceLocks(setPath)
	err = locks.Lock("BUSY-1")
	if err == nil {
		t.Fatal("Lock: expected an error while another process holds the lock")
	}
	if !strings.Contains(err.Error(), "in use by another axe process") {
		t.Errorf("Lock: unexpected error: %v", err)
	}
}

func TestDeviceLocks_Unlock_RemovesLockFile(t *testing.T) {
	setPath := t.TempDir()
	locks := NewDeviceLocks(setPath)

	if err := locks.Lock("RELEASED-1"); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	locks.Unlock("RELEASED-1")

	if _, err := os.Stat(filepath.Join(setPath, "RELEASED-1.lock")); !os.IsNotExist(err) {
		t.Errorf("expected lock file to be removed after Unlock, stat err: %v", err)
	}
	if locks.IsHeld("RELEASED-1") {
		t.Error("IsHeld: expected false after Unlock")
	}
	if err := locks.Lock("RELEASED-1"); err != nil {
		t.Errorf("Lock: expected to re-acquire the lock after Unlock: %v", err)
	}
}

func TestDevicePool_CleanupOrphans_SkipsDeviceLockedBySingleSession(t *testing.T) {
	// A single-device session in another process holds the lock without going
	// through the pool; the pool's orphan cleanup must leave the device alone.
	runner := newFakeSimctlRunner()
	setPath := t.TempDir()
	runner.addDevice("SINGLE-1", "axe iPhone 16 Pro (1)", testDeviceType, testRuntime, "Booted")

	session := NewDeviceLocks(setPath)
	if err := session.Lock("SINGLE-1"); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer session.Unlock("SINGLE-1")

	pool := NewDevicePool(runner, setPath)
	if err := pool.CleanupOrphans(context.Background()); err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.shutdownCalls != 0 {
		t.Errorf("expected 0 Shutdown calls (device is locked by a single session), got %d", runner.shutdownCalls)
	}
}

func TestDeviceLocks_XcodeDeviceSet_NoOp(t *testing.T) {
	// An empty set path is Xcode's own device set: nothing is locked
	// and no lock file is created (it would otherwise land in the cwd).
	locks := NewDeviceLocks("")

	if err := locks.Lock("XCODE-1"); err != nil {
		t.Errorf("Lock: expected no error for the Xcode device set: %v", err)
	}
	if !locks.IsHeld("XCODE-1") {
		t.Error("IsHeld: expected true for the Xcode device set (never an orphan)")
	}
	locks.Unlock("XCODE-1")
	if _, err := os.Stat("XCODE-1.lock"); !os.IsNotExist(err) {
		t.Errorf("expected no lock file in the working directory, stat err: %v", err)
	}
}
