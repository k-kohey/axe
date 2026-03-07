package watch

import (
	"slices"
	"sync"
	"time"
)

const (
	// TrackedDebounceDelay is the debounce delay for tracked file changes
	// (target file or 1-level dependencies). Short because hot-reload is fast.
	TrackedDebounceDelay = 200 * time.Millisecond

	// DepDebounceDelay is the debounce delay for untracked dependency changes.
	// Longer because these trigger a full project rebuild.
	DepDebounceDelay = 500 * time.Millisecond
)

// Debouncer manages debounce timers for file change events.
// It classifies changes as tracked (hot-reload) or dependency (full rebuild)
// and fires signals on the appropriate output channel after the debounce delay.
//
// Usage: call HandleFileChange for each file change event, then select on
// TrackedCh/DepCh in the caller's event loop. Call Stop to release timers.
type Debouncer struct {
	// TrackedCh receives the changed file path after the tracked debounce delay.
	TrackedCh <-chan string
	// DepCh sends the accumulated untracked file paths after the dependency debounce delay.
	DepCh <-chan []string

	trackedCh chan string
	depCh     chan []string

	mu           sync.Mutex // protects depFiles, trackedTimer, depTimer, trackedSeq, depSeq
	depFiles     []string   // accumulated untracked file paths within debounce window
	trackedTimer *time.Timer
	depTimer     *time.Timer
	trackedSeq   uint64 // generation counter; incremented on each tracked timer reset
	depSeq       uint64 // generation counter; incremented on each dep timer reset
}

// NewDebouncer creates a Debouncer with buffered output channels.
func NewDebouncer() *Debouncer {
	tracked := make(chan string, 1)
	dep := make(chan []string, 1)
	return &Debouncer{
		TrackedCh: tracked,
		DepCh:     dep,
		trackedCh: tracked,
		depCh:     dep,
	}
}

// HandleFileChange classifies a file change and starts/resets the
// appropriate debounce timer. trackedSet contains the set of tracked
// file paths (cleaned) for efficient lookup.
func (d *Debouncer) HandleFileChange(cleanPath string, trackedSet map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if trackedSet[cleanPath] {
		// Tracked file changed (target or 1-level dependency).
		// If a dependency rebuild is already pending, it will include
		// this change too, so skip the fast path.
		if d.depTimer != nil {
			return
		}
		if d.trackedTimer != nil {
			d.trackedTimer.Stop()
		}
		changedFile := cleanPath
		d.trackedSeq++
		seq := d.trackedSeq
		d.trackedTimer = time.AfterFunc(TrackedDebounceDelay, func() {
			d.mu.Lock()
			if d.trackedSeq != seq {
				d.mu.Unlock()
				return // stale timer, a newer one supersedes this
			}
			d.mu.Unlock()
			select {
			case d.trackedCh <- changedFile:
			default:
			}
		})
	} else {
		// Untracked .swift file changed → dependency rebuild path.
		if d.trackedTimer != nil {
			d.trackedTimer.Stop()
			d.trackedTimer = nil
		}
		if d.depTimer != nil {
			d.depTimer.Stop()
		}

		// Accumulate untracked files with deduplication.
		if !slices.Contains(d.depFiles, cleanPath) {
			d.depFiles = append(d.depFiles, cleanPath)
		}

		// Capture snapshot and generation for the timer callback.
		snapshot := make([]string, len(d.depFiles))
		copy(snapshot, d.depFiles)
		d.depSeq++
		seq := d.depSeq

		d.depTimer = time.AfterFunc(DepDebounceDelay, func() {
			d.mu.Lock()
			if d.depSeq != seq {
				d.mu.Unlock()
				return // stale timer, a newer one supersedes this
			}
			d.mu.Unlock()
			select {
			case d.depCh <- snapshot:
			default:
			}
		})
	}
}

// ClearDepTimer resets the dependency timer reference and accumulated files
// after the dep signal has been consumed. This allows subsequent tracked
// changes to use the fast path again.
func (d *Debouncer) ClearDepTimer() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.depTimer = nil
	d.depFiles = nil
}

// Reset cancels all pending timers and drains the output channels,
// restoring the debouncer to a clean state. Use this on file switch
// to prevent stale timers from firing against the new file.
func (d *Debouncer) Reset() {
	d.mu.Lock()
	if d.trackedTimer != nil {
		d.trackedTimer.Stop()
		d.trackedTimer = nil
	}
	if d.depTimer != nil {
		d.depTimer.Stop()
		d.depTimer = nil
	}
	d.depFiles = nil
	// Invalidate any already-fired callbacks sitting in the goroutine queue.
	// Timer.Stop() cannot prevent callbacks that have already been scheduled.
	d.trackedSeq++
	d.depSeq++
	d.mu.Unlock()

	// Drain any buffered signals that may have fired between Stop and now.
	select {
	case <-d.trackedCh:
	default:
	}
	select {
	case <-d.depCh:
	default:
	}
}

// Stop cancels all pending timers. Call this when the event loop exits.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.trackedTimer != nil {
		d.trackedTimer.Stop()
	}
	if d.depTimer != nil {
		d.depTimer.Stop()
	}
}
