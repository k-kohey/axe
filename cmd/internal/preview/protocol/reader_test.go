package protocol

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

func TestReadCommands_ValidCommands(t *testing.T) {
	input := `{"streamId":"a","addStream":{"file":"HogeView.swift","deviceType":"iPhone16,1","runtime":"iOS-18-0"}}
{"streamId":"b","removeStream":{}}
`
	var received []*pb.Command
	ReadCommands(context.Background(), strings.NewReader(input), nil, func(cmd *pb.Command) {
		received = append(received, cmd)
	})

	if len(received) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(received))
	}
	if received[0].GetStreamId() != "a" || received[0].GetAddStream() == nil {
		t.Errorf("first command: got streamId=%s, addStream=%v", received[0].GetStreamId(), received[0].GetAddStream())
	}
	if received[1].GetStreamId() != "b" || received[1].GetRemoveStream() == nil {
		t.Errorf("second command: got streamId=%s, removeStream=%v", received[1].GetStreamId(), received[1].GetRemoveStream())
	}
}

func TestReadCommands_SkipsEmptyLines(t *testing.T) {
	input := `
{"streamId":"a","nextPreview":{}}

`
	var received []*pb.Command
	ReadCommands(context.Background(), strings.NewReader(input), nil, func(cmd *pb.Command) {
		received = append(received, cmd)
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 command (empty lines skipped), got %d", len(received))
	}
}

func TestReadCommands_SkipsInvalidJSON(t *testing.T) {
	input := `not valid json
{"streamId":"a","nextPreview":{}}
{bad json}
`
	var received []*pb.Command
	ReadCommands(context.Background(), strings.NewReader(input), nil, func(cmd *pb.Command) {
		received = append(received, cmd)
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 valid command, got %d", len(received))
	}
	if received[0].GetStreamId() != "a" {
		t.Errorf("expected streamId 'a', got %s", received[0].GetStreamId())
	}
}

func TestReadCommands_RespectsContextCancellation(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	defer func() { _ = pr.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	var received []*pb.Command
	done := make(chan struct{})

	// Start the reader goroutine first.
	go func() {
		defer close(done)
		ReadCommands(ctx, pr, nil, func(cmd *pb.Command) {
			received = append(received, cmd)
		})
	}()

	// Write one command.
	_, _ = pw.Write([]byte(`{"streamId":"a","nextPreview":{}}` + "\n"))

	// Wait a bit for the command to be processed, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Close the pipe to unblock the scanner.
	_ = pw.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReadCommands did not return after context cancellation")
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 command before cancellation, got %d", len(received))
	}
}

func TestReadCommands_SendsProtocolErrorOnInvalidJSON(t *testing.T) {
	input := `not valid json
{"streamId":"a","nextPreview":{}}
{bad json}
`
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	var received []*pb.Command
	ReadCommands(context.Background(), strings.NewReader(input), ew, func(cmd *pb.Command) {
		received = append(received, cmd)
	})

	// Only the valid command should be handled.
	if len(received) != 1 {
		t.Fatalf("expected 1 valid command, got %d", len(received))
	}

	// Two invalid lines -> two ProtocolError events should be written.
	output := strings.TrimSpace(buf.String())
	lines := strings.Split(output, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 ProtocolError events, got %d lines: %q", len(lines), output)
	}

	for i, line := range lines {
		event, err := UnmarshalEvent([]byte(line))
		if err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
		pe := event.GetProtocolError()
		if pe == nil {
			t.Fatalf("line %d: expected ProtocolError payload, got %v", i, event.GetPayload())
		}
		if !strings.Contains(pe.GetMessage(), "invalid command") {
			t.Errorf("line %d: unexpected message: %q", i, pe.GetMessage())
		}
	}
}
