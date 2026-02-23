package watch

import "time"

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
	// DepCh fires after the dependency debounce delay.
	DepCh <-chan struct{}

	trackedCh chan string
	depCh     chan struct{}

	trackedTimer *time.Timer
	depTimer     *time.Timer
}

// NewDebouncer creates a Debouncer with buffered output channels.
func NewDebouncer() *Debouncer {
	tracked := make(chan string, 1)
	dep := make(chan struct{}, 1)
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
		d.trackedTimer = time.AfterFunc(TrackedDebounceDelay, func() {
			select {
			case d.trackedCh <- changedFile:
			default:
			}
		})
	} else {
		// Untracked .swift file changed → full rebuild path.
		if d.trackedTimer != nil {
			d.trackedTimer.Stop()
			d.trackedTimer = nil
		}
		if d.depTimer != nil {
			d.depTimer.Stop()
		}
		d.depTimer = time.AfterFunc(DepDebounceDelay, func() {
			select {
			case d.depCh <- struct{}{}:
			default:
			}
		})
	}
}

// ClearDepTimer resets the dependency timer reference after the dep signal
// has been consumed. This allows subsequent tracked changes to use the
// fast path again.
func (d *Debouncer) ClearDepTimer() {
	d.depTimer = nil
}

// Stop cancels all pending timers. Call this when the event loop exits.
func (d *Debouncer) Stop() {
	if d.trackedTimer != nil {
		d.trackedTimer.Stop()
	}
	if d.depTimer != nil {
		d.depTimer.Stop()
	}
}
