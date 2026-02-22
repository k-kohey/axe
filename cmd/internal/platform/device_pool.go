package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const gcMaxAge = 14 * 24 * time.Hour // 2 weeks

const simctlTimeout = 30 * time.Second

// deviceKey groups pool entries by device type and runtime.
type deviceKey struct {
	DeviceType string
	Runtime    string
}

// poolEntry represents a single simulator device in the pool.
type poolEntry struct {
	UDID       string
	DeviceType string
	Runtime    string
}

// deviceMeta is persisted as <deviceSetPath>/<udid>.meta.json.
type deviceMeta struct {
	LastUsed time.Time `json:"lastUsed"`
}

// DevicePool manages simulator allocation and reuse.
// It assigns simulators by deviceType + runtime, pooling released devices for later reuse.
type DevicePool struct {
	mu            sync.Mutex
	deviceSetPath string
	available     map[deviceKey][]poolEntry // Shutdown devices ready for reuse
	inUse         map[string]poolEntry      // UDID → in-use entry
	lockFiles     map[string]*os.File       // UDID → held flock file handle
	simctl        SimctlRunner
}

// NewDevicePool creates a DevicePool using the given SimctlRunner and device set path.
func NewDevicePool(simctl SimctlRunner, deviceSetPath string) *DevicePool {
	return &DevicePool{
		deviceSetPath: deviceSetPath,
		available:     make(map[deviceKey][]poolEntry),
		inUse:         make(map[string]poolEntry),
		lockFiles:     make(map[string]*os.File),
		simctl:        simctl,
	}
}

// Acquire obtains a simulator matching deviceType + runtime.
// Priority: 1. Shutdown device in pool → 2. Reuse Shutdown device from device set
// → 3. Clone existing → 4. Create new.
func (p *DevicePool) Acquire(ctx context.Context, deviceType, runtime string) (string, error) {
	p.mu.Lock()

	key := deviceKey{DeviceType: deviceType, Runtime: runtime}

	// Priority 1: reuse a pooled device.
	if entries := p.available[key]; len(entries) > 0 {
		entry := entries[len(entries)-1]
		p.available[key] = entries[:len(entries)-1]
		p.inUse[entry.UDID] = entry
		p.mu.Unlock()
		p.acquireLockFile(entry.UDID)
		p.writeMetaFile(entry.UDID)
		slog.Info("Reusing pooled device", "udid", entry.UDID, "deviceType", deviceType, "runtime", runtime)
		return entry.UDID, nil
	}

	p.mu.Unlock()

	// List existing devices (outside of lock to avoid holding mutex during I/O).
	listCtx, listCancel := context.WithTimeout(ctx, simctlTimeout)
	defer listCancel()
	devices, err := p.simctl.ListDevices(listCtx, p.deviceSetPath)
	if err != nil {
		slog.Debug("Failed to list devices", "err", err)
		devices = nil
	}

	// Priority 2: reuse a Shutdown device from the device set.
	// Also find a clone source (any device with matching type+runtime) in the same pass.
	var cloneSource string
	for _, d := range devices {
		if d.DeviceTypeIdentifier != deviceType || d.RuntimeID != runtime {
			continue
		}
		if cloneSource == "" {
			cloneSource = d.UDID
		}
		if d.State != "Shutdown" {
			continue
		}
		p.mu.Lock()
		if _, alreadyInUse := p.inUse[d.UDID]; alreadyInUse {
			p.mu.Unlock()
			continue
		}
		p.inUse[d.UDID] = poolEntry{UDID: d.UDID, DeviceType: deviceType, Runtime: runtime}
		p.mu.Unlock()

		if p.acquireLockFile(d.UDID) {
			p.writeMetaFile(d.UDID)
			slog.Info("Reusing existing device from set", "udid", d.UDID, "deviceType", deviceType, "runtime", runtime)
			return d.UDID, nil
		}
		// Lock acquisition failed — another process is using this device.
		p.mu.Lock()
		delete(p.inUse, d.UDID)
		p.mu.Unlock()
	}

	// Priority 3: clone an existing device.
	if cloneSource != "" {
		name := p.nextDeviceName(devices, deviceType)
		cloneCtx, cloneCancel := context.WithTimeout(ctx, simctlTimeout)
		udid, err := p.simctl.Clone(cloneCtx, cloneSource, name, p.deviceSetPath)
		cloneCancel()
		if err != nil {
			slog.Warn("Clone failed, falling back to create", "source", cloneSource, "err", err)
		} else {
			entry := poolEntry{UDID: udid, DeviceType: deviceType, Runtime: runtime}
			p.mu.Lock()
			if _, alreadyClaimed := p.inUse[udid]; alreadyClaimed {
				// A concurrent Acquire (Priority 2) grabbed this device between
				// Clone and here. The cloned device is orphaned; GC will clean it up.
				p.mu.Unlock()
				slog.Debug("Cloned device already claimed by concurrent acquire", "udid", udid)
			} else {
				p.inUse[udid] = entry
				p.mu.Unlock()
				p.acquireLockFile(udid)
				p.writeMetaFile(udid)
				slog.Info("Cloned device", "udid", udid, "source", cloneSource)
				return udid, nil
			}
		}
	}

	// Priority 4: create a new device.
	// Loop to retry if a concurrent Acquire (Priority 2) claims the device
	// between Create and the inUse registration.
	const maxCreateRetries = 3
	for range maxCreateRetries {
		name := p.nextDeviceName(devices, deviceType)
		createCtx, createCancel := context.WithTimeout(ctx, simctlTimeout)
		udid, err := p.simctl.Create(createCtx, name, deviceType, runtime, p.deviceSetPath)
		createCancel()
		if err != nil {
			return "", fmt.Errorf("creating device: %w", err)
		}

		entry := poolEntry{UDID: udid, DeviceType: deviceType, Runtime: runtime}
		p.mu.Lock()
		if _, alreadyClaimed := p.inUse[udid]; alreadyClaimed {
			// A concurrent Acquire (Priority 2) grabbed this device between
			// Create and here. The created device is orphaned; GC will clean it up.
			p.mu.Unlock()
			slog.Debug("Created device already claimed by concurrent acquire", "udid", udid)
			continue
		}
		p.inUse[udid] = entry
		p.mu.Unlock()
		p.acquireLockFile(udid)
		p.writeMetaFile(udid)
		slog.Info("Created new device", "udid", udid, "deviceType", deviceType, "runtime", runtime)
		return udid, nil
	}
	return "", fmt.Errorf("failed to create device after %d attempts (concurrent claim conflicts)", maxCreateRetries)
}

// Release shuts down a device and returns it to the pool for reuse.
func (p *DevicePool) Release(ctx context.Context, udid string) error {
	p.mu.Lock()
	entry, ok := p.inUse[udid]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("device %s not found in active pool", udid)
	}
	delete(p.inUse, udid)
	p.mu.Unlock()

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, simctlTimeout)
	defer shutdownCancel()
	if err := p.simctl.Shutdown(shutdownCtx, udid, p.deviceSetPath); err != nil {
		// Shutdown failed — device state is unknown, don't return to pool.
		// Still release the lock file to avoid leaking the flock.
		p.closeLockFile(udid)
		p.releaseLockFile(udid)
		return fmt.Errorf("shutting down device %s: %w", udid, err)
	}

	p.closeLockFile(udid)
	p.releaseLockFile(udid)

	p.mu.Lock()
	key := deviceKey{DeviceType: entry.DeviceType, Runtime: entry.Runtime}
	p.available[key] = append(p.available[key], entry)
	p.mu.Unlock()

	slog.Info("Released device to pool", "udid", udid)
	return nil
}

// ShutdownAll shuts down all devices (both in-use and available).
// Called at process exit for cleanup.
func (p *DevicePool) ShutdownAll(ctx context.Context) {
	p.mu.Lock()
	var all []poolEntry
	for _, entry := range p.inUse {
		all = append(all, entry)
	}
	for _, entries := range p.available {
		all = append(all, entries...)
	}
	p.inUse = make(map[string]poolEntry)
	p.available = make(map[deviceKey][]poolEntry)
	p.mu.Unlock()

	for _, entry := range all {
		sdCtx, sdCancel := context.WithTimeout(ctx, simctlTimeout)
		if err := p.simctl.Shutdown(sdCtx, entry.UDID, p.deviceSetPath); err != nil {
			slog.Debug("Failed to shutdown device during ShutdownAll", "udid", entry.UDID, "err", err)
		}
		sdCancel()
		p.closeLockFile(entry.UDID)
		p.releaseLockFile(entry.UDID)
	}
}

// CleanupOrphans finds Booted devices whose controlling process is gone
// (lock file is acquirable) and shuts them down.
// Called at process startup before accepting streams.
func (p *DevicePool) CleanupOrphans(ctx context.Context) error {
	listCtx, listCancel := context.WithTimeout(ctx, simctlTimeout)
	defer listCancel()
	devices, err := p.simctl.ListDevices(listCtx, p.deviceSetPath)
	if err != nil {
		return fmt.Errorf("listing devices for orphan cleanup: %w", err)
	}

	for _, d := range devices {
		if d.State != "Booted" {
			continue
		}
		if p.isOrphaned(d.UDID) {
			slog.Info("Cleaning up orphaned device", "udid", d.UDID, "name", d.Name)
			sdCtx, sdCancel := context.WithTimeout(ctx, simctlTimeout)
			if err := p.simctl.Shutdown(sdCtx, d.UDID, p.deviceSetPath); err != nil {
				slog.Warn("Failed to shutdown orphaned device", "udid", d.UDID, "err", err)
			}
			sdCancel()
		}
	}
	return nil
}

// GarbageCollect deletes devices that haven't been used in gcMaxAge.
// Called at process exit to prevent disk bloat.
func (p *DevicePool) GarbageCollect(ctx context.Context) {
	listCtx, listCancel := context.WithTimeout(ctx, simctlTimeout)
	defer listCancel()
	devices, err := p.simctl.ListDevices(listCtx, p.deviceSetPath)
	if err != nil {
		slog.Debug("Failed to list devices for GC", "err", err)
		return
	}

	p.mu.Lock()
	inUseUDIDs := make(map[string]bool, len(p.inUse))
	for udid := range p.inUse {
		inUseUDIDs[udid] = true
	}
	p.mu.Unlock()

	now := time.Now()
	for _, d := range devices {
		if inUseUDIDs[d.UDID] {
			continue
		}

		meta, err := p.readMetaFile(d.UDID)
		if err != nil {
			continue // No meta file → skip (can't determine age)
		}

		if now.Sub(meta.LastUsed) > gcMaxAge {
			slog.Info("Garbage collecting expired device", "udid", d.UDID, "name", d.Name, "lastUsed", meta.LastUsed)
			delCtx, delCancel := context.WithTimeout(ctx, simctlTimeout)
			if err := p.simctl.Delete(delCtx, d.UDID, p.deviceSetPath); err != nil {
				slog.Warn("Failed to delete expired device", "udid", d.UDID, "err", err)
			} else {
				// Remove from available map so stale UDIDs are not reused.
				p.mu.Lock()
				for key, entries := range p.available {
					for i, e := range entries {
						if e.UDID == d.UDID {
							p.available[key] = append(entries[:i], entries[i+1:]...)
							break
						}
					}
				}
				p.mu.Unlock()
			}
			delCancel()
			// Clean up meta and lock files (best-effort, errors are non-fatal).
			if err := os.Remove(filepath.Join(p.deviceSetPath, d.UDID+".meta.json")); err != nil && !os.IsNotExist(err) {
				slog.Debug("Failed to remove meta file during GC", "udid", d.UDID, "err", err)
			}
			if err := os.Remove(filepath.Join(p.deviceSetPath, d.UDID+".lock")); err != nil && !os.IsNotExist(err) {
				slog.Debug("Failed to remove lock file during GC", "udid", d.UDID, "err", err)
			}
		}
	}
}

// acquireLockFile creates and holds an exclusive flock for the device.
// The OS automatically releases the flock on process exit (including SIGKILL),
// allowing CleanupOrphans to detect zombie devices.
// Returns true if the lock was successfully acquired.
func (p *DevicePool) acquireLockFile(udid string) bool {
	lockPath := filepath.Join(p.deviceSetPath, udid+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		slog.Debug("Failed to open lock file", "path", lockPath, "err", err)
		return false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		slog.Debug("Failed to acquire flock", "path", lockPath, "err", err)
		_ = f.Close()
		return false
	}

	p.mu.Lock()
	p.lockFiles[udid] = f
	p.mu.Unlock()
	return true
}

// closeLockFile releases the flock and closes the file handle for a device.
// The lock file on disk is not removed so that it can be reused.
func (p *DevicePool) closeLockFile(udid string) {
	p.mu.Lock()
	f, ok := p.lockFiles[udid]
	if ok {
		delete(p.lockFiles, udid)
	}
	p.mu.Unlock()

	if ok {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// isOrphaned checks whether a Booted device has no controlling process.
// It tries to acquire the lock file in non-blocking mode. If the lock succeeds,
// the controlling process is gone (OS releases flock on process exit).
func (p *DevicePool) isOrphaned(udid string) bool {
	lockPath := filepath.Join(p.deviceSetPath, udid+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Can't open lock file → treat as non-orphaned (safe default).
		return false
	}
	defer func() { _ = f.Close() }()

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Lock held by another process → device is actively managed.
		return false
	}
	// Lock acquired → controlling process is gone → orphaned.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true
}

// releaseLockFile removes the lock file for a device.
func (p *DevicePool) releaseLockFile(udid string) {
	lockPath := filepath.Join(p.deviceSetPath, udid+".lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		slog.Debug("Failed to remove lock file", "path", lockPath, "err", err)
	}
}

// writeMetaFile writes or updates the meta file for a device.
func (p *DevicePool) writeMetaFile(udid string) {
	meta := deviceMeta{LastUsed: time.Now()}
	data, err := json.Marshal(meta)
	if err != nil {
		slog.Debug("Failed to marshal meta", "udid", udid, "err", err)
		return
	}
	metaPath := filepath.Join(p.deviceSetPath, udid+".meta.json")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		slog.Debug("Failed to create meta dir", "err", err)
		return
	}
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		slog.Debug("Failed to write meta file", "path", metaPath, "err", err)
	}
}

// readMetaFile reads the meta file for a device.
func (p *DevicePool) readMetaFile(udid string) (deviceMeta, error) {
	metaPath := filepath.Join(p.deviceSetPath, udid+".meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return deviceMeta{}, err
	}
	var meta deviceMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return deviceMeta{}, err
	}
	return meta, nil
}

// nextDeviceName generates a sequential name like "axe iPhone 16 Pro (2)"
// based on existing devices.
func (p *DevicePool) nextDeviceName(devices []simDevice, deviceType string) string {
	baseName := deviceTypeBaseNameFromID(deviceType)
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	seq := nextSequenceFromNames(names, baseName)
	return fmt.Sprintf("axe %s (%d)", baseName, seq)
}

// deviceTypeBaseNameFromID extracts a human-readable name from a device type identifier.
// e.g. "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro" → "iPhone 16 Pro"
func deviceTypeBaseNameFromID(identifier string) string {
	last := identifier
	if idx := strings.LastIndex(identifier, "."); idx >= 0 {
		last = identifier[idx+1:]
	}
	return strings.ReplaceAll(last, "-", " ")
}
