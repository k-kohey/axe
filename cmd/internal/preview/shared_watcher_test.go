package preview

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// newTestSharedWatcher creates a sharedWatcher that watches a single directory.
// It bypasses the ProjectConfig-based discovery used in production.
// Cleanup is registered via t.Cleanup.
func newTestSharedWatcher(t *testing.T, dir string) *sharedWatcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		t.Fatalf("watching dir: %v", err)
	}

	stopCh := make(chan struct{})
	loopDone := make(chan struct{})

	var closeOnce sync.Once
	sw := &sharedWatcher{
		watcher:   watcher,
		listeners: make(map[string]chan<- string),
		cancel:    func() { closeOnce.Do(func() { close(stopCh) }) },
		done:      loopDone,
	}

	go func() {
		defer close(loopDone)
		for {
			select {
			case <-stopCh:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Ext(event.Name) != ".swift" {
					continue
				}
				if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
					continue
				}
				sw.broadcast(filepath.Clean(event.Name))
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	t.Cleanup(func() {
		sw.close()
	})
	return sw
}

func TestSharedWatcher_Broadcast(t *testing.T) {
	dir := t.TempDir()

	sw := newTestSharedWatcher(t, dir)

	chA := make(chan string, 4)
	chB := make(chan string, 4)
	sw.addListener("a", chA)
	sw.addListener("b", chB)

	// Create a .swift file to trigger an event.
	path := filepath.Join(dir, "TestView.swift")
	if err := os.WriteFile(path, []byte("struct TestView {}"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	// Both listeners should receive the event.
	expectPath := filepath.Clean(path)

	select {
	case got := <-chA:
		if got != expectPath {
			t.Errorf("listener a: got %s, want %s", got, expectPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener a: timed out waiting for event")
	}

	select {
	case got := <-chB:
		if got != expectPath {
			t.Errorf("listener b: got %s, want %s", got, expectPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener b: timed out waiting for event")
	}
}

func TestSharedWatcher_RemoveListener(t *testing.T) {
	dir := t.TempDir()

	sw := newTestSharedWatcher(t, dir)

	chA := make(chan string, 4)
	chB := make(chan string, 4)
	sw.addListener("a", chA)
	sw.addListener("b", chB)

	// Remove listener B.
	sw.removeListener("b")

	// Trigger a file change.
	path := filepath.Join(dir, "AnotherView.swift")
	if err := os.WriteFile(path, []byte("struct AnotherView {}"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	// Listener A should get the event.
	select {
	case <-chA:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("listener a: timed out waiting for event")
	}

	// Listener B should NOT get the event (removed).
	select {
	case got := <-chB:
		t.Errorf("listener b should not have received event, got %s", got)
	case <-time.After(300 * time.Millisecond):
		// Expected: no event received.
	}
}

func TestSharedWatcher_NonSwiftIgnored(t *testing.T) {
	dir := t.TempDir()

	sw := newTestSharedWatcher(t, dir)

	ch := make(chan string, 4)
	sw.addListener("a", ch)

	// Write a non-swift file.
	path := filepath.Join(dir, "README.md")
	if err := os.WriteFile(path, []byte("# README"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	// Should NOT trigger an event.
	select {
	case got := <-ch:
		t.Errorf("should not have received event for non-swift file, got %s", got)
	case <-time.After(300 * time.Millisecond):
		// Expected: no event.
	}
}
