package preview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
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
func collectEvents(t *testing.T, buf *bytes.Buffer) []parsedEvent {
	t.Helper()
	var events []parsedEvent
	scanner := bufio.NewScanner(buf)
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

// newTestStreamManager creates a StreamManager with a fake launcher that acquires
// a device, sends a "booting" status event, and blocks until ctx is cancelled.
func newTestStreamManager(pool DevicePoolInterface, ew *EventWriter) *StreamManager {
	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")
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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)
	defer sm.StopAll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)
	defer sm.StopAll()

	// Command with no payload should not panic.
	sm.HandleCommand(context.Background(), &pb.Command{StreamId: "x"})
}

// TestStreamManager_FullLifecycle verifies the fake launcher sends StreamStarted
// and Frame events, and RemoveStream produces StreamStopped{removed}.
func TestStreamManager_FullLifecycle(t *testing.T) {
	pool := newFakeDevicePool()
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		_ = sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: udid, File: s.file, Data: "frame-" + s.id}},
		})

		<-ctx.Done()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")

	launcherDone := make(chan struct{})
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		udid, _ := sm.pool.Acquire(ctx, s.deviceType, s.runtime)
		s.deviceUDID = udid

		// Simulate an error: send StreamStopped and return.
		s.sendStopped(sm.ew, "build_error", "compilation failed", "error: type 'Foo' not found")
		close(launcherDone)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Give runStream's defer a moment to clean up and self-remove.
	time.Sleep(50 * time.Millisecond)

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")

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
	time.Sleep(100 * time.Millisecond)

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
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	sm := NewStreamManager(pool, ew, ProjectConfig{}, "")

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

// waitForEvents polls until the buffer contains at least n newlines (events).
func waitForEvents(t *testing.T, buf *bytes.Buffer, n int, timeout time.Duration) {
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
