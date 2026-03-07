package watch

import (
	"sort"
	"sync"
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
	case files := <-db.DepCh:
		if len(files) != 1 || files[0] != "/src/Other.swift" {
			t.Errorf("expected [/src/Other.swift], got %v", files)
		}
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

func TestDebouncer_ResetClearsPendingDepTimer(t *testing.T) {
	db := NewDebouncer()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Trigger a dep change (untracked file).
	db.HandleFileChange("/src/Other.swift", trackedSet)

	// Simulate file switch: reset the debouncer.
	db.Reset()

	// Old dep timer should NOT fire after reset.
	select {
	case <-db.DepCh:
		t.Fatal("DepCh should not fire after Reset")
	case <-time.After(DepDebounceDelay + 100*time.Millisecond):
		// OK — timer was cancelled by Reset.
	}

	// New tracked change should work normally after reset.
	db.HandleFileChange("/src/HogeView.swift", trackedSet)

	select {
	case got := <-db.TrackedCh:
		if got != "/src/HogeView.swift" {
			t.Errorf("expected /src/HogeView.swift, got %s", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("TrackedCh did not fire after Reset")
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

func TestDebouncer_DepFilesAccumulation(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Multiple untracked changes within the debounce window should accumulate.
	db.HandleFileChange("/src/A.swift", trackedSet)
	db.HandleFileChange("/src/B.swift", trackedSet)
	db.HandleFileChange("/src/C.swift", trackedSet)

	select {
	case files := <-db.DepCh:
		sort.Strings(files)
		expected := []string{"/src/A.swift", "/src/B.swift", "/src/C.swift"}
		if len(files) != len(expected) {
			t.Fatalf("expected %d files, got %d: %v", len(expected), len(files), files)
		}
		for i, f := range files {
			if f != expected[i] {
				t.Errorf("files[%d] = %q, want %q", i, f, expected[i])
			}
		}
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire within timeout")
	}
}

func TestDebouncer_DepFilesDeduplicated(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	// Same file changed multiple times should not duplicate.
	db.HandleFileChange("/src/A.swift", trackedSet)
	db.HandleFileChange("/src/A.swift", trackedSet)
	db.HandleFileChange("/src/B.swift", trackedSet)
	db.HandleFileChange("/src/A.swift", trackedSet)

	select {
	case files := <-db.DepCh:
		sort.Strings(files)
		expected := []string{"/src/A.swift", "/src/B.swift"}
		if len(files) != len(expected) {
			t.Fatalf("expected %d files (deduplicated), got %d: %v", len(expected), len(files), files)
		}
		for i, f := range files {
			if f != expected[i] {
				t.Errorf("files[%d] = %q, want %q", i, f, expected[i])
			}
		}
	case <-time.After(1 * time.Second):
		t.Fatal("DepCh did not fire within timeout")
	}
}

func TestDebouncer_ConcurrentAccess(t *testing.T) {
	db := NewDebouncer()
	defer db.Stop()

	trackedSet := map[string]bool{"/src/HogeView.swift": true}

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				db.HandleFileChange("/src/HogeView.swift", trackedSet)
			} else {
				db.HandleFileChange("/src/Other.swift", trackedSet)
			}
		}(i)
	}
	wg.Wait()
	// No race detector errors means success.
}
