package preview

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// sharedWatcher runs a single fsnotify.Watcher and fans out .swift file
// change events to all registered stream listeners.
// Debounce is the stream's responsibility; the watcher delivers raw events.
type sharedWatcher struct {
	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	listeners map[string]chan<- string // streamID → fileChangeCh
	cancel    context.CancelFunc
	done      chan struct{} // closed when the event loop exits
}

// newSharedWatcher creates a sharedWatcher that monitors directories containing
// .swift files under the project root. It uses git ls-files for fast discovery,
// falling back to WalkDir for non-git projects.
func newSharedWatcher(ctx context.Context, pc ProjectConfig) (*sharedWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	watchRoot := filepath.Dir(pc.primaryPath())
	watchDirs, err := gitSwiftDirs(watchRoot)
	if err != nil {
		slog.Debug("git ls-files unavailable, falling back to WalkDir", "err", err)
		watchDirs, err = walkSwiftDirs(watchRoot)
		if err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	for _, d := range watchDirs {
		if err := watcher.Add(d); err != nil {
			slog.Debug("Cannot watch directory", "path", d, "err", err)
		}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	sw := &sharedWatcher{
		watcher:   watcher,
		listeners: make(map[string]chan<- string),
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go sw.loop(loopCtx)
	return sw, nil
}

// addListener registers a stream to receive file change paths.
func (sw *sharedWatcher) addListener(streamID string, ch chan<- string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.listeners[streamID] = ch
}

// removeListener unregisters a stream.
func (sw *sharedWatcher) removeListener(streamID string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	delete(sw.listeners, streamID)
}

// close stops the event loop and releases the underlying fsnotify.Watcher.
func (sw *sharedWatcher) close() {
	sw.cancel()
	<-sw.done
	_ = sw.watcher.Close()
}

// loop reads fsnotify events, filters for .swift Write/Create, and broadcasts
// the cleaned file path to all listeners with non-blocking sends.
func (sw *sharedWatcher) loop(ctx context.Context) {
	defer close(sw.done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sw.watcher.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(event.Name, ".swift") {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			cleanPath := filepath.Clean(event.Name)
			sw.broadcast(cleanPath)
		case err, ok := <-sw.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("Shared watcher error", "err", err)
		}
	}
}

// broadcast sends a file path to all registered listeners.
// Non-blocking: if a listener's channel is full, the event is dropped
// (the stream will pick up the change on the next event).
func (sw *sharedWatcher) broadcast(path string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	for _, ch := range sw.listeners {
		select {
		case ch <- path:
		default:
		}
	}
}
