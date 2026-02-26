package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

func TestEvent_MarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		event *pb.Event
	}{
		{
			name: "Frame",
			event: &pb.Event{
				StreamId: "abc-123",
				Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: "iPhone 16 Pro", File: "HogeView.swift", Data: "base64data"}},
			},
		},
		{
			name: "StreamStarted",
			event: &pb.Event{
				StreamId: "abc-123",
				Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: 3}},
			},
		},
		{
			name: "StreamStopped",
			event: &pb.Event{
				StreamId: "abc-123",
				Payload:  &pb.Event_StreamStopped{StreamStopped: &pb.StreamStopped{Reason: "build_error", Message: "Build failed", Diagnostic: "error: expected '}'"}},
			},
		},
		{
			name: "StreamStatus",
			event: &pb.Event{
				StreamId: "abc-123",
				Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "building"}},
			},
		},
		{
			name:  "ProtocolError",
			event: &pb.Event{Payload: &pb.Event_ProtocolError{ProtocolError: &pb.ProtocolError{Message: "invalid command: unexpected token"}}},
		},
		{
			name:  "Hello",
			event: &pb.Event{Payload: &pb.Event_Hello{Hello: &pb.Hello{ProtocolVersion: 1}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := MarshalEvent(tt.event)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Verify the output is valid JSON.
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("protojson output is not valid JSON: %v", err)
			}

			// Verify streamId matches the event's value.
			if tt.event.StreamId != "" {
				if raw["streamId"] != tt.event.StreamId {
					t.Errorf("expected streamId=%s, got %v", tt.event.StreamId, raw["streamId"])
				}
			}
		})
	}
}

func TestCommand_UnmarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		checkFn func(t *testing.T, cmd *pb.Command)
	}{
		{
			name: "AddStream",
			json: `{"streamId":"stream-1","addStream":{"file":"/path/to/View.swift","deviceType":"com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro","runtime":"com.apple.CoreSimulator.SimRuntime.iOS-18-2"}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetStreamId() != "stream-1" {
					t.Errorf("StreamId = %q, want %q", cmd.GetStreamId(), "stream-1")
				}
				if cmd.GetAddStream() == nil {
					t.Fatal("expected AddStream to be non-nil")
				}
				if cmd.GetAddStream().GetFile() != "/path/to/View.swift" {
					t.Errorf("File = %q, want %q", cmd.GetAddStream().GetFile(), "/path/to/View.swift")
				}
			},
		},
		{
			name: "RemoveStream",
			json: `{"streamId":"stream-1","removeStream":{}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetRemoveStream() == nil {
					t.Fatal("expected RemoveStream to be non-nil")
				}
			},
		},
		{
			name: "SwitchFile",
			json: `{"streamId":"stream-1","switchFile":{"file":"/path/to/Other.swift"}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetSwitchFile() == nil {
					t.Fatal("expected SwitchFile to be non-nil")
				}
				if cmd.GetSwitchFile().GetFile() != "/path/to/Other.swift" {
					t.Errorf("File = %q, want %q", cmd.GetSwitchFile().GetFile(), "/path/to/Other.swift")
				}
			},
		},
		{
			name: "NextPreview",
			json: `{"streamId":"stream-1","nextPreview":{}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetNextPreview() == nil {
					t.Fatal("expected NextPreview to be non-nil")
				}
			},
		},
		{
			name: "ForceRebuild",
			json: `{"streamId":"stream-1","forceRebuild":{}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetForceRebuild() == nil {
					t.Fatal("expected ForceRebuild to be non-nil")
				}
			},
		},
		{
			name: "Input_TouchDown",
			json: `{"streamId":"stream-1","input":{"touchDown":{"x":0.5,"y":0.3}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput() == nil {
					t.Fatal("expected Input to be non-nil")
				}
				if cmd.GetInput().GetTouchDown() == nil {
					t.Fatal("expected TouchDown to be non-nil")
				}
				if cmd.GetInput().GetTouchDown().GetX() != 0.5 {
					t.Errorf("X = %v, want 0.5", cmd.GetInput().GetTouchDown().GetX())
				}
			},
		},
		{
			name: "Input_Text",
			json: `{"streamId":"stream-1","input":{"text":{"value":"a"}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput() == nil {
					t.Fatal("expected Input to be non-nil")
				}
				if cmd.GetInput().GetText() == nil {
					t.Fatal("expected Text to be non-nil")
				}
				if cmd.GetInput().GetText().GetValue() != "a" {
					t.Errorf("Value = %q, want %q", cmd.GetInput().GetText().GetValue(), "a")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := UnmarshalCommand([]byte(tt.json))
			if err != nil {
				t.Fatalf("UnmarshalCommand failed: %v", err)
			}
			tt.checkFn(t, cmd)
		})
	}
}

func TestEvent_OmitEmpty(t *testing.T) {
	// Only Frame is set; other payload fields should be omitted.
	e := &pb.Event{
		StreamId: "s1",
		Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: "iPhone 16 Pro", File: "View.swift", Data: "abc"}},
	}

	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	s := string(data)
	for _, key := range []string{`"streamStarted"`, `"streamStopped"`, `"streamStatus"`} {
		if strings.Contains(s, key) {
			t.Errorf("expected %s to be omitted, got: %s", key, s)
		}
	}

	// Verify streamId and frame are present.
	if !strings.Contains(s, `"streamId"`) {
		t.Errorf("expected streamId to be present, got: %s", s)
	}
	if !strings.Contains(s, `"frame"`) {
		t.Errorf("expected frame to be present, got: %s", s)
	}
}

func TestCommand_UnknownFieldTolerance(t *testing.T) {
	// Simulate a future extension sending extra fields.
	jsonStr := `{"streamId":"s1","addStream":{"file":"/path","deviceType":"dt","runtime":"rt","unknownField":"value"},"futureField":42}`

	cmd, err := UnmarshalCommand([]byte(jsonStr))
	if err != nil {
		t.Fatalf("Unmarshal with unknown fields should succeed: %v", err)
	}

	if cmd.GetStreamId() != "s1" {
		t.Errorf("StreamId = %q, want %q", cmd.GetStreamId(), "s1")
	}
	if cmd.GetAddStream() == nil {
		t.Fatal("AddStream should not be nil")
	}
	if cmd.GetAddStream().GetFile() != "/path" {
		t.Errorf("AddStream.File = %q, want %q", cmd.GetAddStream().GetFile(), "/path")
	}
}

func TestCommand_InvalidJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"not JSON", "hello"},
		{"truncated", `{"streamId":"s1"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UnmarshalCommand([]byte(tt.input))
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tt.input)
			}
		})
	}
}

func TestEvent_JSONFieldNames(t *testing.T) {
	// Verify that JSON uses camelCase field names (matching the design doc).
	e := &pb.Event{
		StreamId: "s1",
		Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: 2}},
	}

	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	s := string(data)
	// Should use camelCase
	if !strings.Contains(s, `"streamId"`) {
		t.Errorf("expected camelCase streamId, got: %s", s)
	}
	if !strings.Contains(s, `"streamStarted"`) {
		t.Errorf("expected camelCase streamStarted, got: %s", s)
	}
	if !strings.Contains(s, `"previewCount"`) {
		t.Errorf("expected camelCase previewCount, got: %s", s)
	}
	// Should NOT use snake_case
	if strings.Contains(s, `"stream_id"`) {
		t.Errorf("unexpected snake_case stream_id, got: %s", s)
	}
}

func TestCommand_JSONFieldNames(t *testing.T) {
	// Verify AddStream JSON from TS extension is correctly parsed.
	jsonStr := `{"streamId":"s1","addStream":{"file":"/path","deviceType":"dt","runtime":"rt"}}`
	cmd, err := UnmarshalCommand([]byte(jsonStr))
	if err != nil {
		t.Fatalf("UnmarshalCommand failed: %v", err)
	}
	if cmd.GetStreamId() != "s1" {
		t.Errorf("expected streamId = s1, got %s", cmd.GetStreamId())
	}
	if cmd.GetAddStream() == nil {
		t.Fatal("expected addStream to be non-nil")
	}
	if cmd.GetAddStream().GetDeviceType() != "dt" {
		t.Errorf("expected deviceType = dt, got %s", cmd.GetAddStream().GetDeviceType())
	}
}

func TestInput_UnmarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		checkFn func(t *testing.T, cmd *pb.Command)
	}{
		{
			name: "TouchDown",
			json: `{"streamId":"s1","input":{"touchDown":{"x":0.1,"y":0.2}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput().GetTouchDown() == nil {
					t.Fatal("expected TouchDown")
				}
				if cmd.GetInput().GetTouchDown().GetX() != 0.1 {
					t.Errorf("X = %v, want 0.1", cmd.GetInput().GetTouchDown().GetX())
				}
			},
		},
		{
			name: "TouchMove",
			json: `{"streamId":"s1","input":{"touchMove":{"x":0.3,"y":0.4}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput().GetTouchMove() == nil {
					t.Fatal("expected TouchMove")
				}
			},
		},
		{
			name: "TouchUp",
			json: `{"streamId":"s1","input":{"touchUp":{"x":0.5,"y":0.6}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput().GetTouchUp() == nil {
					t.Fatal("expected TouchUp")
				}
			},
		},
		{
			name: "Text",
			json: `{"streamId":"s1","input":{"text":{"value":"x"}}}`,
			checkFn: func(t *testing.T, cmd *pb.Command) {
				if cmd.GetInput().GetText() == nil {
					t.Fatal("expected Text")
				}
				if cmd.GetInput().GetText().GetValue() != "x" {
					t.Errorf("Value = %q, want %q", cmd.GetInput().GetText().GetValue(), "x")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := UnmarshalCommand([]byte(tt.json))
			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			tt.checkFn(t, cmd)
		})
	}
}

func TestStreamStopped_EmptyDiagnostic(t *testing.T) {
	// With EmitDefaultValues, empty strings are emitted. Verify reason is present.
	e := &pb.Event{
		StreamId: "s1",
		Payload:  &pb.Event_StreamStopped{StreamStopped: &pb.StreamStopped{Reason: "removed"}},
	}

	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"reason"`) {
		t.Errorf("expected reason to be present, got: %s", s)
	}

	// Verify round-trip via JSON.parse on TS side would work.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON parse failed: %v", err)
	}
	ss, ok := raw["streamStopped"].(map[string]any)
	if !ok {
		t.Fatal("expected streamStopped to be present")
	}
	if ss["reason"] != "removed" {
		t.Errorf("reason = %v, want %q", ss["reason"], "removed")
	}
}

func TestProtocolError_MarshalFields(t *testing.T) {
	e := &pb.Event{
		Payload: &pb.Event_ProtocolError{ProtocolError: &pb.ProtocolError{Message: "bad input"}},
	}
	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"protocolError"`) {
		t.Errorf("expected protocolError key, got: %s", s)
	}
	if !strings.Contains(s, `"bad input"`) {
		t.Errorf("expected message content, got: %s", s)
	}
}

func TestHello_MarshalFields(t *testing.T) {
	e := &pb.Event{
		Payload: &pb.Event_Hello{Hello: &pb.Hello{ProtocolVersion: 1}},
	}
	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"hello"`) {
		t.Errorf("expected hello key, got: %s", s)
	}
	if !strings.Contains(s, `"protocolVersion"`) {
		t.Errorf("expected protocolVersion key, got: %s", s)
	}

	// Verify round-trip via JSON.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON parse failed: %v", err)
	}
	hello, ok := raw["hello"].(map[string]any)
	if !ok {
		t.Fatal("expected hello to be present")
	}
	if hello["protocolVersion"] != float64(1) {
		t.Errorf("protocolVersion = %v, want 1", hello["protocolVersion"])
	}
}

func TestProtocolVersion_Constant(t *testing.T) {
	if ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", ProtocolVersion)
	}
}

// TestEvent_CrossLanguageWireFormat verifies that protojson output is compatible
// with the TS extension's JSON.parse.
func TestEvent_CrossLanguageWireFormat(t *testing.T) {
	e := &pb.Event{
		StreamId: "s1",
		Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: "iPhone 16 Pro", File: "HogeView.swift", Data: "AAAA"}},
	}

	data, err := MarshalEvent(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Parse as generic JSON (simulating TS JSON.parse).
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON.parse equivalent failed: %v", err)
	}

	if raw["streamId"] != "s1" {
		t.Errorf("streamId = %v, want %q", raw["streamId"], "s1")
	}
	frame, ok := raw["frame"].(map[string]any)
	if !ok {
		t.Fatal("expected frame to be present")
	}
	if frame["device"] != "iPhone 16 Pro" {
		t.Errorf("frame.device = %v, want %q", frame["device"], "iPhone 16 Pro")
	}
	if frame["data"] != "AAAA" {
		t.Errorf("frame.data = %v, want %q", frame["data"], "AAAA")
	}
}
