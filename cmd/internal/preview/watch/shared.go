package watch

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// SharedWatcher runs a single fsnotify.Watcher and fans out .swift file
// change events to all registered stream listeners.
// Debounce is the stream's responsibility; the watcher delivers raw events.
type SharedWatcher struct {
	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	listeners map[string]chan<- string // streamID → fileChangeCh
	cancel    context.CancelFunc
	done      chan struct{} // closed when the event loop exits
}

// NewSharedWatcher creates a SharedWatcher that monitors directories containing
// .swift files under watchRoot. It uses dl for fast discovery,
// falling back to WalkSwiftDirs for non-git projects.
func NewSharedWatcher(ctx context.Context, watchRoot string, dl DirLister) (*SharedWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	watchDirs, err := dl.SwiftDirs(ctx, watchRoot)
	if err != nil {
		slog.Debug("git ls-files unavailable, falling back to WalkDir", "err", err)
		watchDirs, err = WalkSwiftDirs(watchRoot)
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
	sw := &SharedWatcher{
		watcher:   watcher,
		listeners: make(map[string]chan<- string),
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go sw.loop(loopCtx)
	return sw, nil
}

// AddListener registers a stream to receive file change paths.
func (sw *SharedWatcher) AddListener(streamID string, ch chan<- string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.listeners[streamID] = ch
}

// RemoveListener unregisters a stream.
func (sw *SharedWatcher) RemoveListener(streamID string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	delete(sw.listeners, streamID)
}

// Close stops the event loop and releases the underlying fsnotify.Watcher.
func (sw *SharedWatcher) Close() {
	sw.cancel()
	<-sw.done
	_ = sw.watcher.Close()
}

// loop reads fsnotify events, filters for .swift Write/Create, and broadcasts
// the cleaned file path to all listeners with non-blocking sends.
func (sw *SharedWatcher) loop(ctx context.Context) {
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
func (sw *SharedWatcher) broadcast(path string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	for _, ch := range sw.listeners {
		select {
		case ch <- path:
		default:
		}
	}
}
