package preview

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// fakeCompanion implements companionProcess for testing.
type fakeCompanion struct {
	doneCh chan struct{}
	err    error
}

func (f *fakeCompanion) Done() <-chan struct{} { return f.doneCh }
func (f *fakeCompanion) Err() error            { return f.err }
func (f *fakeCompanion) Stop() error           { return nil }

// newTestStream creates a stream with initialized channels for testing the event loop.
func newTestStream(id string) *stream {
	return &stream{
		id:            id,
		file:          "/path/to/HogeView.swift",
		switchFileCh:  make(chan string, 1),
		nextPreviewCh: make(chan struct{}, 1),
		inputCh:       make(chan *pb.Input, 1),
		fileChangeCh:  make(chan string, 1),
		ws: &watchState{
			reloadCounter:   1,
			previewSelector: "0",
			previewIndex:    0,
			previewCount:    3,
			skeletonMap:     make(map[string]string),
			trackedFiles:    []string{"/path/to/HogeView.swift"},
		},
	}
}

func TestStreamLoop_Cancellation(t *testing.T) {
	s := newTestStream("test-cancel")
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	sm := NewStreamManager(newFakeDevicePool(), ew, ProjectConfig{}, "")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runStreamLoop(ctx, s, sm, &buildSettings{}, nil)
	}()

	// Cancel should cause clean exit.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error on cancel, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamLoop did not exit on context cancel")
	}
}

func TestStreamLoop_SwitchFile(t *testing.T) {
	s := newTestStream("test-switch")
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	sm := NewStreamManager(newFakeDevicePool(), ew, ProjectConfig{}, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runStreamLoop(ctx, s, sm, &buildSettings{}, nil)
	}()

	// Send a switchFile command. Since we don't have a real project, switchFile
	// will fail, but the channel routing itself should work (no hang/panic).
	s.switchFileCh <- "/path/to/FugaView.swift"

	// Give the loop time to process the command.
	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamLoop did not exit")
	}
}

func TestStreamLoop_NextPreview(t *testing.T) {
	s := newTestStream("test-next")
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	sm := NewStreamManager(newFakeDevicePool(), ew, ProjectConfig{}, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runStreamLoop(ctx, s, sm, &buildSettings{}, nil)
	}()

	// Send nextPreview. The actual reloadMultiFile will fail but the index
	// should be updated.
	s.nextPreviewCh <- struct{}{}

	// Give the loop time to process.
	time.Sleep(100 * time.Millisecond)

	s.ws.mu.Lock()
	idx := s.ws.previewIndex
	sel := s.ws.previewSelector
	s.ws.mu.Unlock()

	if idx != 1 {
		t.Errorf("expected previewIndex 1 after NextPreview, got %d", idx)
	}
	if sel != "1" {
		t.Errorf("expected previewSelector '1', got %s", sel)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamLoop did not exit")
	}
}

func TestStreamLoop_BootCrash(t *testing.T) {
	s := newTestStream("test-crash")
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	sm := NewStreamManager(newFakeDevicePool(), ew, ProjectConfig{}, "")

	// Simulate a boot companion that has already died.
	bootDied := make(chan struct{})
	close(bootDied)
	s.bootCompanion = &fakeCompanion{doneCh: bootDied, err: fmt.Errorf("process exited with code 1")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runStreamLoop(ctx, s, sm, &buildSettings{}, nil)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error from boot crash")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamLoop did not exit on boot crash")
	}

	// Verify StreamStopped was sent.
	events := filterEvents(collectEvents(t, &buf), "test-crash")
	var foundStopped bool
	for _, e := range events {
		if e.StreamStopped != nil {
			if reason, ok := e.StreamStopped["reason"].(string); ok && reason == "runtime_error" {
				foundStopped = true
			}
		}
	}
	if !foundStopped {
		t.Errorf("expected StreamStopped{reason:runtime_error}, events: %+v", events)
	}
}

func TestStreamLoop_IDBError(t *testing.T) {
	s := newTestStream("test-idb-err")
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	sm := NewStreamManager(newFakeDevicePool(), ew, ProjectConfig{}, "")

	idbErrCh := make(chan error, 1)
	idbErrCh <- fmt.Errorf("video stream died")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runStreamLoop(ctx, s, sm, &buildSettings{}, idbErrCh)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error from idb error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runStreamLoop did not exit on idb error")
	}
}
