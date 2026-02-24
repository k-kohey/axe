package preview

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const warmDeviceTimeout = 10 * time.Minute

type warmDeviceKey struct {
	deviceType string
	runtime    string
}

type warmDevice struct {
	udid          string
	deviceType    string
	runtime       string
	bootCompanion companionProcess
	cancel        context.CancelFunc
	done          chan struct{} // closed when goroutine exits
	claimed       atomic.Bool  // true if consumed by a new stream
}

// parkDevice stores a booted device for warm reuse. The device's boot companion
// remains running so the next stream with the same device type and runtime can
// skip the ~11s boot time. If a warm device with the same key already exists,
// the previous one is shut down immediately.
func (sm *StreamManager) parkDevice(udid, deviceType, runtime string, bootCompanion companionProcess) {
	key := warmDeviceKey{deviceType: deviceType, runtime: runtime}

	ctx, cancel := context.WithCancel(context.Background())
	wd := &warmDevice{
		udid:          udid,
		deviceType:    deviceType,
		runtime:       runtime,
		bootCompanion: bootCompanion,
		cancel:        cancel,
		done:          make(chan struct{}),
	}

	sm.warmMu.Lock()
	old := sm.warmDevices[key]
	sm.warmDevices[key] = wd
	sm.warmMu.Unlock()

	// Shut down the previous warm device with the same key.
	if old != nil {
		old.cancel()
		<-old.done
	}

	slog.Info("Parking warm device", "udid", udid, "deviceType", deviceType, "runtime", runtime)
	go sm.warmShutdownLoop(ctx, wd, key)
}

// claimWarmDevice retrieves a warm device matching the given type and runtime.
// Returns ("", nil) if no matching warm device is available.
func (sm *StreamManager) claimWarmDevice(deviceType, runtime string) (string, companionProcess) {
	key := warmDeviceKey{deviceType: deviceType, runtime: runtime}

	sm.warmMu.Lock()
	wd, ok := sm.warmDevices[key]
	if ok {
		delete(sm.warmDevices, key)
		// Set claimed under the lock to prevent the goroutine from racing
		// on shutdown (e.g. when the boot companion crashes concurrently).
		wd.claimed.Store(true)
	}
	sm.warmMu.Unlock()

	if !ok {
		return "", nil
	}

	wd.cancel()
	<-wd.done

	return wd.udid, wd.bootCompanion
}

// shutdownWarmDevices cancels all warm device goroutines and waits for them
// to complete their shutdown. Called from StopAll.
func (sm *StreamManager) shutdownWarmDevices() {
	sm.warmMu.Lock()
	devices := make([]*warmDevice, 0, len(sm.warmDevices))
	for _, wd := range sm.warmDevices {
		devices = append(devices, wd)
	}
	sm.warmDevices = make(map[warmDeviceKey]*warmDevice)
	sm.warmMu.Unlock()

	for _, wd := range devices {
		wd.cancel()
	}
	for _, wd := range devices {
		select {
		case <-wd.done:
		case <-time.After(30 * time.Second):
			slog.Error("Warm device shutdown timed out", "udid", wd.udid)
		}
	}
}

// warmShutdownLoop runs in a goroutine for each parked warm device.
// It waits for the timeout to expire, context cancellation (from claim or
// process exit), or boot companion crash, then performs cleanup if not claimed.
func (sm *StreamManager) warmShutdownLoop(ctx context.Context, wd *warmDevice, key warmDeviceKey) {
	defer close(wd.done)

	select {
	case <-time.After(warmDeviceTimeout):
		slog.Info("Warm device timeout expired, shutting down", "udid", wd.udid)
	case <-ctx.Done():
		// Fast path: if claimed, the new stream takes ownership.
		// This is safe without the lock because claimed is set before cancel(),
		// and cancel() happens-before ctx.Done() fires.
		if wd.claimed.Load() {
			slog.Debug("Warm device claimed by new stream", "udid", wd.udid)
			return
		}
		slog.Info("Warm device cancelled, shutting down", "udid", wd.udid)
	case <-wd.bootCompanion.Done():
		slog.Warn("Warm device boot companion exited unexpectedly",
			"udid", wd.udid, "err", wd.bootCompanion.Err())
	}

	// Recheck claimed under the lock to handle the race between a concurrent
	// claimWarmDevice and boot companion crash detection.
	sm.warmMu.Lock()
	if wd.claimed.Load() {
		sm.warmMu.Unlock()
		return
	}
	if sm.warmDevices[key] == wd {
		delete(sm.warmDevices, key)
	}
	sm.warmMu.Unlock()

	if err := wd.bootCompanion.Stop(); err != nil {
		slog.Debug("Failed to stop warm device boot companion", "udid", wd.udid, "err", err)
	}
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := sm.pool.Release(releaseCtx, wd.udid); err != nil {
		slog.Warn("Failed to release warm device", "udid", wd.udid, "err", err)
	}
	releaseCancel()
}
