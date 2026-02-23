package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

func TestEventWriter_SendJSONLines(t *testing.T) {
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	events := []*pb.Event{
		{StreamId: "s1", Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "booting"}}},
		{StreamId: "s1", Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "building"}}},
		{StreamId: "s1", Payload: &pb.Event_Frame{Frame: &pb.Frame{Device: "iPhone 16 Pro", File: "HogeView.swift", Data: "base64data"}}},
	}

	for _, e := range events {
		if err := ew.Send(e); err != nil {
			t.Fatalf("Send failed: %v", err)
		}
	}

	// Each line should be valid JSON.
	scanner := bufio.NewScanner(&buf)
	lineNum := 0
	for scanner.Scan() {
		var raw map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			t.Errorf("line %d: invalid JSON: %v (line: %s)", lineNum, err, scanner.Text())
		}
		if raw["streamId"] != "s1" {
			t.Errorf("line %d: streamId = %v, want %q", lineNum, raw["streamId"], "s1")
		}
		lineNum++
	}

	if lineNum != len(events) {
		t.Errorf("expected %d lines, got %d", len(events), lineNum)
	}
}

func TestEventWriter_ConcurrentSend(t *testing.T) {
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	const n = 100
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := ew.Send(&pb.Event{
				StreamId: fmt.Sprintf("s%d", id),
				Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "running"}},
			}); err != nil {
				t.Errorf("Send(%d) failed: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	// Each line should be an independent, valid JSON object.
	scanner := bufio.NewScanner(&buf)
	count := 0
	for scanner.Scan() {
		var raw map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			t.Errorf("line %d: invalid JSON: %v (line: %q)", count, err, scanner.Text())
		}
		count++
	}

	if count != n {
		t.Errorf("expected %d lines, got %d", n, count)
	}
}

func TestEventWriter_FlushesImmediately(t *testing.T) {
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	if err := ew.Send(&pb.Event{StreamId: "s1", Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "booting"}}}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// After Send, the data should be in the buffer immediately.
	if buf.Len() == 0 {
		t.Error("expected buffer to have data after Send, but it is empty")
	}

	// Should be parseable without waiting for more data.
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &raw); err != nil {
		t.Errorf("buffer content is not valid JSON: %v", err)
	}
}

func TestEventWriter_EmptyStreamID(t *testing.T) {
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)

	// Empty stream ID should still work (protojson omits it but the JSON is still valid).
	if err := ew.Send(&pb.Event{StreamId: "", Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "booting"}}}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &raw); err != nil {
		t.Errorf("buffer content is not valid JSON: %v", err)
	}
}
