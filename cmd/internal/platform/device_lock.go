package platform

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// DeviceLocks tracks which simulators in a device set are in use by a live
// process. A session holds an exclusive flock on <set>/<udid>.lock for as long
// as it uses the device. The OS releases the flock on process exit (including
// SIGKILL), so a Booted device whose lock is not held has no controlling
// process and can be cleaned up as an orphan. Pool sessions and single-device
// sessions take the same lock.
//
// An empty setPath denotes Xcode's own device set, which axe never cleans up;
// locks there are no-ops.
type DeviceLocks struct {
	setPath string
	mu      sync.Mutex
	files   map[string]*os.File // UDID → held flock file handle
}

// NewDeviceLocks creates a DeviceLocks for the given device set path.
func NewDeviceLocks(setPath string) *DeviceLocks {
	return &DeviceLocks{setPath: setPath, files: make(map[string]*os.File)}
}

// isXcodeDeviceSet reports whether this is Xcode's own device set rather than
// the axe one (no --set path). axe never cleans up orphans there, so there is
// nothing to lock.
func (l *DeviceLocks) isXcodeDeviceSet() bool {
	return l.setPath == ""
}

func (l *DeviceLocks) path(udid string) string {
	return filepath.Join(l.setPath, udid+".lock")
}

// Lock creates and holds an exclusive flock for the device. It fails if
// another process already holds it.
func (l *DeviceLocks) Lock(udid string) error {
	if l.isXcodeDeviceSet() {
		return nil
	}
	lockPath := l.path(udid)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return fmt.Errorf("simulator %s is in use by another axe process", udid)
	}

	l.mu.Lock()
	l.files[udid] = f
	l.mu.Unlock()
	return nil
}

// Unlock releases the flock taken by Lock. The lock file stays on disk: if it
// were unlinked, a process that already opened the old file could lock that
// inode while another locks a newly created file for the same UDID. The file
// is removed only when the device itself is deleted.
func (l *DeviceLocks) Unlock(udid string) {
	if l.isXcodeDeviceSet() {
		return
	}
	l.mu.Lock()
	f, ok := l.files[udid]
	if ok {
		delete(l.files, udid)
	}
	l.mu.Unlock()

	if ok {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// IsHeld reports whether some process currently holds the device's lock.
// It tries to acquire the lock in non-blocking mode; if that succeeds, the
// controlling process is gone.
func (l *DeviceLocks) IsHeld(udid string) bool {
	if l.isXcodeDeviceSet() {
		return true
	}
	f, err := os.OpenFile(l.path(udid), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Can't open lock file → treat as held (safe default).
		return true
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// remove deletes the lock file on disk (best-effort).
func (l *DeviceLocks) remove(udid string) {
	lockPath := l.path(udid)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		slog.Debug("Failed to remove lock file", "path", lockPath, "err", err)
	}
}
