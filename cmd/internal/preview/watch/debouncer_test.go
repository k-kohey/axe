package watch

import (
	"testing"
	"time"
)

func TestDebouncer_TrackedFileTriggersTrackedCh(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	select {
	case got := <-db.TrackedCh:
		if got != "/src/HogeView.swift" {
			t.Errorf("expected /src/HogeView.swift, got %s", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("TrackedCh did not fire within timeout")
	}
}

func TestDebouncer_UntrackedFileTriggersDepCh(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}
	db.HandleFileChange("/src/Other.swift", trackedSet)

	select {
	case <-db.DepCh:
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire within timeout")
	}
}

func TestDebouncer_DepChangeCancelsPendingTracked(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Start a tracked timer.
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	// Immediately follow with a dep change — should cancel the tracked timer.
	db.HandleFileChange("/src/Other.swift", trackedSet)

	select {
	case <-db.DepCh:
		// OK — dep fires
	case <-db.TrackedCh:
		t.Fatal("TrackedCh should not fire when dep change supersedes it")
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire within timeout")
	}
}

func TestDebouncer_PendingDepBlocksTrackedFastPath(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Start a dep timer.
	db.HandleFileChange("/src/Other.swift", trackedSet)

	// While dep is pending, tracked change should be skipped (dep rebuild
	// will include it).
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	select {
	case <-db.DepCh:
		// OK — only dep fires
	case <-db.TrackedCh:
		t.Fatal("TrackedCh should not fire while dep timer is pending")
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire within timeout")
	}
}

func TestDebouncer_ClearDepTimerReEnablesFastPath(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Trigger dep change and consume it.
	db.HandleFileChange("/src/Other.swift", trackedSet)
	select {
	case <-db.DepCh:
		db.ClearDepTimer()
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire")
	}

	// Now tracked changes should use the fast path again.
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	select {
	case got := <-db.TrackedCh:
		if got != "/src/HogeView.swift" {
			t.Errorf("expected /src/HogeView.swift, got %s", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("TrackedCh did not fire after ClearDepTimer")
	}
}

func TestDebouncer_TrackedDebounceResetsOnRapidChanges(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{
		"/src/A.swift": true,
		"/src/B.swift": true,
	}

	// Rapid changes: the timer should reset, only the last file fires.
	db.HandleFileChange("/src/A.swift", trackedSet)
	db.HandleFileChange("/src/B.swift", trackedSet)

	select {
	case got := <-db.TrackedCh:
		if got != "/src/B.swift" {
			t.Errorf("expected last changed file /src/B.swift, got %s", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("TrackedCh did not fire within timeout")
	}
}

func TestDebouncer_StopCancelsPendingTimers(t *testing.T) {
	db := NewDebouncer()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	// Stop before the timer fires.
	db.Stop()

	// Channels should not receive anything.
	select {
	case <-db.TrackedCh:
		t.Fatal("TrackedCh should not fire after Stop")
	case <-time.After(500 * time.Millisecond):
		// OK — timer was cancelled
	}
}
