package preview

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/preview/protocol"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

func TestRunCommandLoop_DispatchesToStreamManager(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	input := `{"streamId":"stream-a","addStream":{"file":"HogeView.swift","deviceType":"iPhone16,1","runtime":"iOS-18-0"}}
`
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCommandLoop(ctx, strings.NewReader(input), ew, sm)
	}()

	// Wait for the command to be processed (stream should emit events).
	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.StopAll()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCommandLoop did not return after reader was exhausted")
	}

	// Verify the stream was actually created.
	events := filterEvents(collectEvents(t, &buf), "stream-a")
	if len(events) == 0 {
		t.Error("expected events for stream-a from dispatched AddStream")
	}
}

func TestRunCommandLoop_MultipleCommands(t *testing.T) {
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

	input := `{"streamId":"stream-a","addStream":{"file":"HogeView.swift","deviceType":"iPhone16,1","runtime":"iOS-18-0"}}
{"streamId":"stream-a","switchFile":{"file":"FugaView.swift"}}
`
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCommandLoop(ctx, strings.NewReader(input), ew, sm)
	}()

	// Wait for stream status event.
	waitForEvents(t, &buf, 1, 2*time.Second)

	// The SwitchFile command should be delivered to the stream.
	select {
	case file := <-receivedFile:
		if file != "FugaView.swift" {
			t.Errorf("expected FugaView.swift, got %s", file)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SwitchFile not received by stream")
	}

	sm.StopAll()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCommandLoop did not return")
	}
}

func TestRunCommandLoop_SkipsInvalidJSON(t *testing.T) {
	pool := newFakeDevicePool()
	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)

	sm := newTestStreamManager(pool, ew)

	input := `not valid json
{"streamId":"stream-a","addStream":{"file":"HogeView.swift","deviceType":"iPhone16,1","runtime":"iOS-18-0"}}
`
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCommandLoop(ctx, strings.NewReader(input), ew, sm)
	}()

	// The valid command should still be dispatched.
	waitForEvents(t, &buf, 1, 2*time.Second)

	sm.StopAll()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCommandLoop did not return")
	}

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	if len(events) == 0 {
		t.Error("expected events for stream-a after skipping invalid JSON")
	}
}

func TestRunCommandLoop_SendsProtocolErrorForInvalidJSON(t *testing.T) {
	pool := newFakeDevicePool()
	var eventBuf syncBuffer
	ew := protocol.NewEventWriter(&eventBuf)

	sm := newTestStreamManagerWithRunners(pool, ew)
	sm.StreamLauncher = func(ctx context.Context, _ *StreamManager, _ *stream) {
		<-ctx.Done()
	}

	input := `{bad json}
{"streamId":"stream-a","addStream":{"file":"HogeView.swift","deviceType":"iPhone16,1","runtime":"iOS-18-0"}}
`
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCommandLoop(ctx, strings.NewReader(input), ew, sm)
	}()

	// Wait for events: ProtocolError + StreamStatus.
	waitForEvents(t, &eventBuf, 1, 2*time.Second)

	sm.StopAll()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCommandLoop did not return")
	}

	output := eventBuf.Bytes()
	if !bytes.Contains(output, []byte("protocolError")) {
		t.Errorf("expected ProtocolError event in output, got: %s", output)
	}
}
