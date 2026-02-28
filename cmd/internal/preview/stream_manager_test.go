package preview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	"github.com/k-kohey/axe/internal/preview/build"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
)

// fakeDevicePool implements DevicePoolInterface for testing.
type fakeDevicePool struct {
	mu          sync.Mutex
	nextID      int
	acquired    map[string]bool // UDID → in-use
	released    []string        // UDIDs that were released
	shutdownAll bool

	acquireErr error
	releaseErr error
}

func newFakeDevicePool() *fakeDevicePool {
	return &fakeDevicePool{
		acquired: make(map[string]bool),
	}
}

func (p *fakeDevicePool) Acquire(_ context.Context, _, _ string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.acquireErr != nil {
		return "", p.acquireErr
	}
	p.nextID++
	udid := fmt.Sprintf("FAKE-%d", p.nextID)
	p.acquired[udid] = true
	return udid, nil
}

func (p *fakeDevicePool) Release(_ context.Context, udid string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.releaseErr != nil {
		return p.releaseErr
	}
	delete(p.acquired, udid)
	p.released = append(p.released, udid)
	return nil
}

func (p *fakeDevicePool) ShutdownAll(_ context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shutdownAll = true
}

func (p *fakeDevicePool) CleanupOrphans(_ context.Context) error {
	return nil
}

func (p *fakeDevicePool) GarbageCollect(_ context.Context) {}

// parsedEvent is a loosely-typed event representation for test assertions.
// We parse the JSON Lines output generically because the EventWriter now uses protojson,
// which differs from encoding/json in zero-value omission.
type parsedEvent struct {
	StreamID      string
	Frame         map[string]any
	StreamStarted map[string]any
	StreamStopped map[string]any
	StreamStatus  map[string]any
}

// collectEvents parses all JSON Lines from a buffer into parsedEvents.
func collectEvents(t *testing.T, buf *syncBuffer) []parsedEvent {
	t.Helper()
	var events []parsedEvent
	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for scanner.Scan() {
		var raw map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			t.Errorf("invalid JSON line: %s", scanner.Text())
			continue
		}
		e := parsedEvent{StreamID: fmt.Sprint(raw["streamId"])}
		if v, ok := raw["frame"].(map[string]any); ok {
			e.Frame = v
		}
		if v, ok := raw["streamStarted"].(map[string]any); ok {
			e.StreamStarted = v
		}
		if v, ok := raw["streamStopped"].(map[string]any); ok {
			e.StreamStopped = v
		}
		if v, ok := raw["streamStatus"].(map[string]any); ok {
			e.StreamStatus = v
		}
		events = append(events, e)
	}
	return events
}

// filterEvents returns events matching the given streamID.
func filterEvents(events []parsedEvent, streamID string) []parsedEvent {
	var filtered []parsedEvent
	for _, e := range events {
		if e.StreamID == streamID {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// nopRunners returns no-op fakes for all runner interfaces.
// Tests that override StreamLauncher don't call these, but providing non-nil
// values prevents panics if cleanupStreamResources or other code paths are
// reached unexpectedly.
func nopRunners() (BuildRunner, ToolchainRunner, AppRunner, FileCopier, SourceLister) {
	return &fakeBuildRunner{}, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}
}

// newTestStreamManagerWithRunners creates a StreamManager with nop runners and
// the default stream launcher. Tests that need a custom launcher should set
// sm.StreamLauncher after calling this.
func newTestStreamManagerWithRunners(pool DevicePoolInterface, ew *protocol.EventWriter) *StreamManager {
	br, tc, ar, fc, sl := nopRunners()
	pc := ProjectConfig{}
	preparer := build.NewPreparer(pc, build.ProjectDirs{}, false, br)
	return NewStreamManager(pool, ew, pc, "", preparer, br, tc, ar, fc, sl, false)
}

// newTestStreamManager creates a StreamManager with a fake launcher that acquires
// a device, sends a "booting" status event, and blocks until ctx is cancelled.
func newTestStreamManager(pool DevicePoolInterface, ew *protocol.EventWriter) *StreamManager {
	br, tc, ar, fc, sl := nopRunners()
	pc := ProjectConfig{}
	preparer := build.NewPreparer(pc, build.ProjectDirs{}, false, br)
	sm := NewStreamManager(pool, ew, pc, "", preparer, br, tc, ar, fc, sl, false)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		if err := sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "booting"}},
		}); err != nil {
			return
		}

		udid, err := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		if err != nil {
			s.sendStopped(sm.ew, "resource_error", fmt.Sprintf("acquiring device: %v", err), "")
			return
		}
		s.deviceUDID = udid

		if err := sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
		}); err != nil {
			return
		}

		<-ctx.Done()
	}
	return sm
}

func TestStreamManager_AddStream_Events(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	// Wait for stream goroutine to emit events.
	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.StopAll()

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	if len(events) == 0 {
		t.Fatal("expected events for stream-a, got none")
	}

	// First event should be a StreamStatus.
	first := events[0]
	if first.StreamStatus == nil {
		t.Errorf("expected StreamStatus as first event, got %+v", first)
	}
}

func TestStreamManager_RemoveStream(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	// Wait for stream to start.
	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_RemoveStream{RemoveStream: &pb.RemoveStream{}},
	})

	// Wait for StreamStopped event.
	waitForEvents(t, &buf, 3, 2*time.Second)

	sm.StopAll()

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	// Should have a StreamStopped with reason "removed".
	var foundStopped bool
	for _, e := range events {
		if e.StreamStopped != nil {
			if reason, ok := e.StreamStopped["reason"].(string); ok && reason == "removed" {
				foundStopped = true
				break
			}
		}
	}
	if !foundStopped {
		t.Errorf("expected StreamStopped{reason:removed}, events: %+v", events)
	}

	// Pool.Release should have been called.
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.released) == 0 {
		t.Error("expected pool.Release to be called")
	}
}

func TestStreamManager_NonexistentRemove(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx := context.Background()

	// Should not panic.
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "nonexistent",
		Payload:  &pb.Command_RemoveStream{RemoveStream: &pb.RemoveStream{}},
	})

	sm.StopAll()
}

func TestStreamManager_TwoStreams(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-b",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/FugaView.swift", DeviceType: "iPad-Air", Runtime: "iOS-18-2"}},
	})

	// Wait for both streams to emit events.
	waitForEvents(t, &buf, 2, 2*time.Second)

	sm.StopAll()

	events := collectEvents(t, &buf)
	eventsA := filterEvents(events, "stream-a")
	eventsB := filterEvents(events, "stream-b")

	if len(eventsA) == 0 {
		t.Error("expected events for stream-a, got none")
	}
	if len(eventsB) == 0 {
		t.Error("expected events for stream-b, got none")
	}
}

func TestStreamManager_StopAll_ShutdownsPool(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.StopAll()

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if !pool.shutdownAll {
		t.Error("expected pool.ShutdownAll to be called")
	}
}

func TestStreamManager_AcquireError(t *testing.T) {
	pool := newFakeDevicePool()
	pool.acquireErr = fmt.Errorf("no devices available")
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)
	defer sm.StopAll()

	ctx := context.Background()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	// Wait for StreamStopped event.
	waitForEvents(t, &buf, 1, 2*time.Second)

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	var foundStopped bool
	for _, e := range events {
		if e.StreamStopped != nil {
			if reason, ok := e.StreamStopped["reason"].(string); ok && reason == "resource_error" {
				foundStopped = true
				break
			}
		}
	}
	if !foundStopped {
		t.Errorf("expected StreamStopped{reason:resource_error}, events: %+v", events)
	}
}

func TestStreamManager_DuplicateStreamID(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)
	defer sm.StopAll()

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	// Second AddStream with same ID should be rejected (warning log, no crash).
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/FugaView.swift", DeviceType: "iPad-Air", Runtime: "iOS-18-2"}},
	})

	// Should not panic — that's the test.
}

func TestStreamManager_EmptyCommand(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)
	defer sm.StopAll()

	// Command with no payload should not panic.
	sm.HandleCommand(context.Background(), &pb.Command{StreamId: "x"})
}

// TestStreamManager_FullLifecycle verifies the fake launcher sends StreamStarted
// and Frame events, and RemoveStream produces StreamStopped{removed}.
func TestStreamManager_FullLifecycle(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, err := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		if err != nil {
			s.sendStopped(sm.ew, "resource_error", err.Error(), "")
			return
		}
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: 2}},
		})

		// Simulate frame sending.
		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: udid, File: s.file, Data: "AAAA"}},
		})

		<-ctx.Done()
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 2, 2*time.Second) // StreamStarted + Frame

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_RemoveStream{RemoveStream: &pb.RemoveStream{}},
	})

	waitForEvents(t, &buf, 3, 2*time.Second) // + StreamStopped

	sm.StopAll()

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	var hasStarted, hasFrame, hasStopped bool
	for _, e := range events {
		if e.StreamStarted != nil {
			hasStarted = true
		}
		if e.Frame != nil {
			hasFrame = true
		}
		if e.StreamStopped != nil {
			if reason, ok := e.StreamStopped["reason"].(string); ok && reason == "removed" {
				hasStopped = true
			}
		}
	}
	if !hasStarted {
		t.Error("expected StreamStarted event")
	}
	if !hasFrame {
		t.Error("expected Frame event")
	}
	if !hasStopped {
		t.Error("expected StreamStopped{removed} event")
	}
}

// TestStreamManager_TwoStreamsWithFrames verifies that two streams receive
// independent Frame events with correct streamIds.
func TestStreamManager_TwoStreamsWithFrames(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: udid, File: s.file, Data: "frame-" + s.id}},
		})

		<-ctx.Done()
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-b",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/FugaView.swift", DeviceType: "iPad-Air", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 2, 2*time.Second)

	sm.StopAll()

	events := collectEvents(t, &buf)
	eventsA := filterEvents(events, "stream-a")
	eventsB := filterEvents(events, "stream-b")

	if len(eventsA) == 0 || eventsA[0].Frame == nil {
		t.Error("expected Frame for stream-a")
	}
	if len(eventsB) == 0 || eventsB[0].Frame == nil {
		t.Error("expected Frame for stream-b")
	}
}

// TestStreamManager_LauncherError_NoDoubleStopped verifies that when a launcher
// sends StreamStopped due to error, and RemoveStream is then called, only one
// StreamStopped event is produced.
func TestStreamManager_LauncherError_NoDoubleStopped(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	launcherDone := make(chan struct{})
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		// Simulate an error: send StreamStopped and return.
		s.sendStopped(sm.ew, "build_error", "compilation failed", "error: type 'Foo' not found")
		close(launcherDone)
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	// Wait for the launcher to complete.
	select {
	case <-launcherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("launcher did not complete")
	}

	// Wait for runStream's defer to complete (self-remove from map).
	waitForStreamCount(t, sm, 0, 2*time.Second)

	// RemoveStream should be safe (stream may already be cleaned up).
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_RemoveStream{RemoveStream: &pb.RemoveStream{}},
	})

	sm.StopAll()

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	stoppedCount := 0
	for _, e := range events {
		if e.StreamStopped != nil {
			stoppedCount++
		}
	}
	if stoppedCount != 1 {
		t.Errorf("expected exactly 1 StreamStopped, got %d; events: %+v", stoppedCount, events)
	}
}

// TestStreamManager_SwitchFileRouting verifies that SwitchFile commands are
// delivered to the stream's switchFileCh.
func TestStreamManager_SwitchFileRouting(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	receivedFile := make(chan string, 1)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
		})

		select {
		case file := <-s.switchFileCh:
			receivedFile <- file
		case <-ctx.Done():
		}
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_SwitchFile{SwitchFile: &pb.SwitchFile{File: "/path/to/FugaView.swift"}},
	})

	select {
	case file := <-receivedFile:
		if file != "/path/to/FugaView.swift" {
			t.Errorf("expected /path/to/FugaView.swift, got %s", file)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SwitchFile not received by stream")
	}

	sm.StopAll()
}

// TestStreamManager_InputRouting verifies that Input commands are delivered
// to the stream's inputCh.
func TestStreamManager_InputRouting(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	receivedInput := make(chan *pb.Input, 1)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
		})

		select {
		case input := <-s.inputCh:
			receivedInput <- input
		case <-ctx.Done():
		}
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_Input{Input: &pb.Input{Event: &pb.Input_Text{Text: &pb.TextEvent{Value: "hello"}}}},
	})

	select {
	case input := <-receivedInput:
		if input.GetText().GetValue() != "hello" {
			t.Errorf("expected text 'hello', got %v", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Input not received by stream")
	}

	sm.StopAll()
}

// TestStreamManager_CleanupOnError verifies that when the launcher exits with
// error, the device is released and the stream is removed from the map.
func TestStreamManager_CleanupOnError(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	launcherDone := make(chan struct{})
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid
		s.sendStopped(sm.ew, "build_error", "failed", "")
		close(launcherDone)
	}

	ctx := context.Background()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	select {
	case <-launcherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("launcher did not complete")
	}

	// Wait for runStream's defer to complete cleanup.
	waitForStreamCount(t, sm, 0, 2*time.Second)

	// Device should be released.
	pool.mu.Lock()
	released := len(pool.released)
	pool.mu.Unlock()
	if released == 0 {
		t.Error("expected pool.Release to be called after launcher error")
	}

	// Stream should be self-removed from map.
	sm.mu.Lock()
	count := len(sm.streams)
	sm.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 streams in map after error, got %d", count)
	}

	sm.StopAll()
}

// TestStreamManager_NextPreviewRouting verifies that NextPreview commands are
// delivered to the stream's nextPreviewCh.
func TestStreamManager_NextPreviewRouting(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	received := make(chan struct{}, 1)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
		})

		select {
		case <-s.nextPreviewCh:
			received <- struct{}{}
		case <-ctx.Done():
		}
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_NextPreview{NextPreview: &pb.NextPreview{}},
	})

	select {
	case <-received:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("NextPreview not received by stream")
	}

	sm.StopAll()
}

// TestStreamManager_ForceRebuildRouting verifies that ForceRebuild commands are
// delivered to the stream's forceRebuildCh.
func TestStreamManager_ForceRebuildRouting(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	received := make(chan struct{}, 1)
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
		})

		select {
		case <-s.forceRebuildCh:
			received <- struct{}{}
		case <-ctx.Done():
		}
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/path/to/HogeView.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})

	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_ForceRebuild{ForceRebuild: &pb.ForceRebuild{}},
	})

	select {
	case <-received:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("ForceRebuild not received by stream")
	}

	sm.StopAll()
}

// TestStreamManager_SharedIndexCache verifies that all streams share the same
// sharedIndexCache instance from StreamManager. When one stream updates the
// cache (simulating a rebuild), other streams see the new value.
func TestStreamManager_SharedIndexCache(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManagerWithRunners(pool, ew)

	// Channels for synchronization between test and fake launcher goroutines.
	streamAReady := make(chan struct{})
	streamBReady := make(chan struct{})
	streamAUpdated := make(chan struct{})
	streamBResult := make(chan bool, 1) // true if B sees the updated cache

	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		// Initialize per-stream watchState with the shared cache.
		s.ws = &watchState{
			indexCache: sm.indexCache, // shared reference
		}

		switch s.id {
		case "stream-a":
			close(streamAReady)

			// Wait for stream B to be ready, then update the shared cache.
			select {
			case <-streamBReady:
			case <-ctx.Done():
				return
			}

			// Simulate rebuild: update the shared cache.
			sm.indexCache.Set(makeTestCache("UpdatedType", "/project/Updated.swift"))
			close(streamAUpdated)

		case "stream-b":
			close(streamBReady)

			// Wait for stream A to update the shared cache.
			select {
			case <-streamAUpdated:
			case <-ctx.Done():
				return
			}

			// Verify that reading through ws.indexCache (same shared ref)
			// returns the value set by stream A.
			got := s.ws.indexCache.Get()
			streamBResult <- (got != nil &&
				len(got.DefinedTypes("/project/Updated.swift")) == 1 &&
				got.DefinedTypes("/project/Updated.swift")[0] == "UpdatedType")
		}

		<-ctx.Done()
	}

	ctx := t.Context()

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/a.swift", DeviceType: "iPhone-16-Pro", Runtime: "iOS-18-2"}},
	})
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-b",
		Payload:  &pb.Command_AddStream{AddStream: &pb.AddStream{File: "/b.swift", DeviceType: "iPad-Air", Runtime: "iOS-18-2"}},
	})

	select {
	case ok := <-streamBResult:
		if !ok {
			t.Error("stream B did not see the cache update from stream A")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream B to verify cache")
	}

	sm.StopAll()
}

// syncBuffer is a thread-safe bytes.Buffer wrapper for use as an io.Writer
// shared between goroutines (e.g. EventWriter + test assertions).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *syncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *syncBuffer) Bytes() []byte {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return append([]byte(nil), sb.buf.Bytes()...)
}

// waitForEvents polls until the buffer contains at least n newlines (events).
func waitForEvents(t *testing.T, buf *syncBuffer, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		lines := bytes.Count(buf.Bytes(), []byte("\n"))
		if lines >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d events (got %d)", n, lines)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// waitForStreamCount polls until sm.streams has exactly n entries.
func waitForStreamCount(t *testing.T, sm *StreamManager, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		sm.mu.Lock()
		count := len(sm.streams)
		sm.mu.Unlock()
		if count == n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for stream count %d (got %d)", n, count)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestDegradedStreamLoop_RejectsCommands(t *testing.T) {
	t.Parallel()

	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	pool := newFakeDevicePool()
	sm := newTestStreamManagerWithRunners(pool, ew)

	s := &stream{
		id:             "degraded-1",
		degraded:       true,
		switchFileCh:   make(chan string, 1),
		nextPreviewCh:  make(chan struct{}, 1),
		forceRebuildCh: make(chan struct{}, 1),
		inputCh:        make(chan *pb.Input, 1),
		fileChangeCh:   make(chan string, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	idbErrCh := make(chan error, 1)

	// Start degraded loop in a goroutine.
	loopDone := make(chan error, 1)
	go func() {
		loopDone <- runDegradedStreamLoop(ctx, s, sm, idbErrCh)
	}()

	// Send commands that should be rejected.
	s.switchFileCh <- "/new/file.swift"
	s.nextPreviewCh <- struct{}{}
	s.forceRebuildCh <- struct{}{}

	// Wait for all 3 rejection events.
	waitForEvents(t, &buf, 3, 2*time.Second)

	// Cancel context to stop the loop.
	cancel()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("degraded loop returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for degraded loop to exit")
	}

	events := collectEvents(t, &buf)
	if len(events) != 3 {
		t.Fatalf("expected 3 rejection events, got %d", len(events))
	}
	for i, e := range events {
		if e.StreamStatus == nil {
			t.Errorf("event %d: expected StreamStatus, got %+v", i, e)
			continue
		}
		if phase, _ := e.StreamStatus["phase"].(string); phase != "degraded" {
			t.Errorf("event %d: phase = %q, want \"degraded\"", i, phase)
		}
	}
}

func TestDegradedStreamLoop_ExitsOnBootCrash(t *testing.T) {
	t.Parallel()

	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	pool := newFakeDevicePool()
	sm := newTestStreamManagerWithRunners(pool, ew)

	bootDied := make(chan struct{})
	s := &stream{
		id:       "degraded-boot-crash",
		degraded: true,
		bootCompanion: &fakeCompanion{
			doneCh: bootDied,
			err:    fmt.Errorf("boot process exited with code 1"),
		},
		switchFileCh:   make(chan string, 1),
		nextPreviewCh:  make(chan struct{}, 1),
		forceRebuildCh: make(chan struct{}, 1),
		inputCh:        make(chan *pb.Input, 1),
		fileChangeCh:   make(chan string, 1),
	}

	ctx := t.Context()
	idbErrCh := make(chan error, 1)

	loopDone := make(chan error, 1)
	go func() {
		loopDone <- runDegradedStreamLoop(ctx, s, sm, idbErrCh)
	}()

	// Simulate boot crash.
	close(bootDied)

	select {
	case err := <-loopDone:
		if err == nil {
			t.Fatal("expected error on boot crash, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for degraded loop to exit on boot crash")
	}

	// Verify StreamStopped was sent.
	waitForEvents(t, &buf, 1, 2*time.Second)
	events := collectEvents(t, &buf)
	found := false
	for _, e := range events {
		if e.StreamStopped != nil {
			found = true
			if reason, _ := e.StreamStopped["reason"].(string); reason != "runtime_error" {
				t.Errorf("reason = %q, want \"runtime_error\"", reason)
			}
		}
	}
	if !found {
		t.Error("expected StreamStopped event on boot crash")
	}
}

type cleanupCountingCompanion struct {
	stopCalls atomic.Int32
	doneCh    chan struct{}
}

func (c *cleanupCountingCompanion) Done() <-chan struct{} { return c.doneCh }
func (c *cleanupCountingCompanion) Err() error            { return nil }
func (c *cleanupCountingCompanion) Stop() error {
	c.stopCalls.Add(1)
	select {
	case <-c.doneCh:
	default:
		close(c.doneCh)
	}
	return nil
}

type cleanupCountingIDBClient struct {
	closeCalls atomic.Int32
}

func (c *cleanupCountingIDBClient) ScreenSize(context.Context) (int, int, error) { return 0, 0, nil }
func (c *cleanupCountingIDBClient) VideoStream(context.Context, int) (<-chan []byte, error) {
	return nil, nil
}
func (c *cleanupCountingIDBClient) Tap(context.Context, float64, float64) error { return nil }
func (c *cleanupCountingIDBClient) Swipe(context.Context, float64, float64, float64, float64, float64) error {
	return nil
}
func (c *cleanupCountingIDBClient) Text(context.Context, string) error { return nil }
func (c *cleanupCountingIDBClient) Screenshot(context.Context) ([]byte, error) {
	return nil, nil
}
func (c *cleanupCountingIDBClient) OpenHIDStream(context.Context) (idb.HIDStream, error) {
	return nil, nil
}
func (c *cleanupCountingIDBClient) TouchDown(idb.HIDStream, float64, float64) error { return nil }
func (c *cleanupCountingIDBClient) TouchMove(idb.HIDStream, float64, float64) error { return nil }
func (c *cleanupCountingIDBClient) TouchUp(idb.HIDStream, float64, float64) error   { return nil }
func (c *cleanupCountingIDBClient) Close() error {
	c.closeCalls.Add(1)
	return nil
}

type cleanupCountingAppRunner struct {
	terminateCalls atomic.Int32
}

func (a *cleanupCountingAppRunner) Terminate(context.Context, string, string, string) error {
	a.terminateCalls.Add(1)
	return nil
}
func (a *cleanupCountingAppRunner) Install(context.Context, string, string, string) error {
	return nil
}
func (a *cleanupCountingAppRunner) Launch(context.Context, string, string, string, map[string]string, []string) error {
	return nil
}

func TestStreamManager_CleanupStreamResources_Idempotent(t *testing.T) {
	t.Parallel()

	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	// Create a preparer with a fake runner that returns valid settings,
	// then call Prepare() to populate the cache so that Cached() returns non-nil.
	output := []byte(`    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`)
	br := &fakeBuildRunner{fetchOutput: output}
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	preparer := build.NewPreparer(pc, build.ProjectDirs{Build: t.TempDir()}, false, br)
	if _, err := preparer.Prepare(context.Background()); err != nil {
		t.Fatalf("seeding preparer cache: %v", err)
	}

	_, tc, _, fc, sl := nopRunners()
	sm := NewStreamManager(pool, ew, pc, "", preparer, br, tc, &cleanupCountingAppRunner{}, fc, sl, false)

	app := sm.app.(*cleanupCountingAppRunner)

	socketPath := filepath.Join(t.TempDir(), "loader.sock")
	if err := os.WriteFile(socketPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("creating socket placeholder: %v", err)
	}

	idbClient := &cleanupCountingIDBClient{}
	bootComp := &cleanupCountingCompanion{doneCh: make(chan struct{})}
	idbComp := &cleanupCountingCompanion{doneCh: make(chan struct{})}
	s := &stream{
		id:            "stream-cleanup",
		deviceUDID:    "FAKE-1",
		dirs:          previewDirs{Socket: socketPath},
		idbClient:     idbClient,
		bootCompanion: bootComp,
		idbCompanion:  idbComp,
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			sm.cleanupStreamResources(s)
		})
	}
	wg.Wait()

	if app.terminateCalls.Load() != 1 {
		t.Fatalf("Terminate called %d times, want 1", app.terminateCalls.Load())
	}
	if idbClient.closeCalls.Load() != 1 {
		t.Fatalf("IDB client Close called %d times, want 1", idbClient.closeCalls.Load())
	}
	if idbComp.stopCalls.Load() != 1 {
		t.Fatalf("idb companion Stop called %d times, want 1", idbComp.stopCalls.Load())
	}
	if bootComp.stopCalls.Load() != 1 {
		t.Fatalf("boot companion Stop called %d times, want 1", bootComp.stopCalls.Load())
	}

	pool.mu.Lock()
	releasedCount := len(pool.released)
	pool.mu.Unlock()
	if releasedCount != 1 {
		t.Fatalf("pool.Release called %d times, want 1", releasedCount)
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket file still exists after cleanup: %v", err)
	}
}
