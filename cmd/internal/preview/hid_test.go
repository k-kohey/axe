package preview

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	pb "github.com/k-kohey/axe/internal/idb/idbproto"
	"google.golang.org/grpc/metadata"
)

// mockHIDClient records method calls for testing hidHandler.
// Set error fields to inject failures into specific methods.
type mockHIDClient struct {
	mu    sync.Mutex
	calls []hidCall

	// Error injection (checked under mu).
	openStreamErr error
	touchDownErr  error

	// If set, OpenHIDStream returns this stream instead of creating a new one.
	returnStream *mockHIDStream
}

type hidCall struct {
	method string
	args   []float64
	text   string
}

func (m *mockHIDClient) record(method string, args []float64, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, hidCall{method: method, args: args, text: text})
}

func (m *mockHIDClient) getCalls() []hidCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]hidCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockHIDClient) Tap(_ context.Context, x, y float64) error {
	m.record("Tap", []float64{x, y}, "")
	return nil
}
func (m *mockHIDClient) Swipe(_ context.Context, sx, sy, ex, ey, dur float64) error {
	m.record("Swipe", []float64{sx, sy, ex, ey, dur}, "")
	return nil
}
func (m *mockHIDClient) Text(_ context.Context, text string) error {
	m.record("Text", nil, text)
	return nil
}
func (m *mockHIDClient) OpenHIDStream(_ context.Context) (idb.HIDStream, error) {
	m.mu.Lock()
	err := m.openStreamErr
	s := m.returnStream
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.record("OpenHIDStream", nil, "")
	if s != nil {
		return s, nil
	}
	return &mockHIDStream{}, nil
}
func (m *mockHIDClient) TouchDown(_ idb.HIDStream, x, y float64) error {
	m.mu.Lock()
	err := m.touchDownErr
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.record("TouchDown", []float64{x, y}, "")
	return nil
}
func (m *mockHIDClient) TouchMove(_ idb.HIDStream, x, y float64) error {
	m.record("TouchMove", []float64{x, y}, "")
	return nil
}
func (m *mockHIDClient) TouchUp(_ idb.HIDStream, x, y float64) error {
	m.record("TouchUp", []float64{x, y}, "")
	return nil
}

// mockHIDStream satisfies idb.HIDStream (pb.CompanionService_HidClient).
type mockHIDStream struct {
	mu              sync.Mutex
	closeRecvCalled bool
}

func (s *mockHIDStream) Send(_ *pb.HIDEvent) error { return nil }
func (s *mockHIDStream) CloseAndRecv() (*pb.HIDResponse, error) {
	s.mu.Lock()
	s.closeRecvCalled = true
	s.mu.Unlock()
	return &pb.HIDResponse{}, nil
}
func (s *mockHIDStream) Header() (metadata.MD, error) { return nil, nil }
func (s *mockHIDStream) Trailer() metadata.MD         { return nil }
func (s *mockHIDStream) CloseSend() error             { return nil }
func (s *mockHIDStream) Context() context.Context     { return context.Background() }
func (s *mockHIDStream) SendMsg(_ interface{}) error  { return nil }
func (s *mockHIDStream) RecvMsg(_ interface{}) error  { return nil }

// --- Coordinate conversion ---

func TestHIDHandler_CoordinateConversion(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "tap", X: 0.5, Y: 0.3})
	// Tap runs in a goroutine; wait for it.
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].method != "Tap" {
		t.Fatalf("expected Tap, got %s", calls[0].method)
	}
	wantX, wantY := 195.0, 253.2
	if calls[0].args[0] != wantX || calls[0].args[1] != wantY {
		t.Errorf("expected (%v, %v), got (%v, %v)", wantX, wantY, calls[0].args[0], calls[0].args[1])
	}
}

// --- Swipe ---

func TestHIDHandler_SwipeCoordinateConversionAndDefaultDuration(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "swipe", StartX: 0.1, StartY: 0.2, EndX: 0.8, EndY: 0.9})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args := calls[0].args
	// startX=0.1*390=39, startY=0.2*844=168.8, endX=0.8*390=312, endY=0.9*844=759.6
	if args[0] != 39.0 {
		t.Errorf("startX: expected 39.0, got %v", args[0])
	}
	if args[1] != 168.8 {
		t.Errorf("startY: expected 168.8, got %v", args[1])
	}
	if args[2] != 312.0 {
		t.Errorf("endX: expected 312.0, got %v", args[2])
	}
	if args[3] != 759.6 {
		t.Errorf("endY: expected 759.6, got %v", args[3])
	}
	// Duration should default to 0.5 when not specified.
	if args[4] != 0.5 {
		t.Errorf("duration: expected default 0.5, got %v", args[4])
	}
}

func TestHIDHandler_SwipeExplicitDuration(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "swipe", StartX: 0.1, StartY: 0.2, EndX: 0.8, EndY: 0.9, Duration: 1.5})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].args[4] != 1.5 {
		t.Errorf("duration: expected 1.5, got %v", calls[0].args[4])
	}
}

// --- Text ---

func TestHIDHandler_TextInput(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "text", Value: "hello"})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].text != "hello" {
		t.Errorf("expected text 'hello', got %q", calls[0].text)
	}
}

func TestHIDHandler_TextEmpty(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "text", Value: ""})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected no calls for empty text, got %d", len(calls))
	}
}

func TestHIDHandler_TextWorksWithZeroScreenSize(t *testing.T) {
	mock := &mockHIDClient{}
	// Screen size is zero, but text input should still work (no coordinate conversion needed).
	h := &hidHandler{client: mock, screenWidth: 0, screenHeight: 0}

	h.Handle(stdinCommand{Type: "text", Value: "hello"})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected text to work without screen size, got %d calls", len(calls))
	}
	if calls[0].text != "hello" {
		t.Errorf("expected text 'hello', got %q", calls[0].text)
	}
}

// --- Touch lifecycle ---

func TestHIDHandler_TouchLifecycle(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "touchDown", X: 0.5, Y: 0.5})

	// Wait enough time to avoid throttle.
	time.Sleep(20 * time.Millisecond)
	h.Handle(stdinCommand{Type: "touchMove", X: 0.6, Y: 0.6})

	h.Handle(stdinCommand{Type: "touchUp", X: 0.7, Y: 0.7})

	calls := mock.getCalls()
	// OpenHIDStream + TouchDown + TouchMove + TouchUp = 4
	if len(calls) != 4 {
		t.Fatalf("expected 4 calls, got %d: %+v", len(calls), calls)
	}
	if calls[0].method != "OpenHIDStream" {
		t.Errorf("call 0: expected OpenHIDStream, got %s", calls[0].method)
	}
	if calls[1].method != "TouchDown" {
		t.Errorf("call 1: expected TouchDown, got %s", calls[1].method)
	}
	if calls[2].method != "TouchMove" {
		t.Errorf("call 2: expected TouchMove, got %s", calls[2].method)
	}
	if calls[3].method != "TouchUp" {
		t.Errorf("call 3: expected TouchUp, got %s", calls[3].method)
	}
}

func TestHIDHandler_TouchMoveThrottle(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	// Start a gesture.
	h.Handle(stdinCommand{Type: "touchDown", X: 0.5, Y: 0.5})

	// Send two touchMoves rapidly (< 16ms apart); second should be throttled.
	h.Handle(stdinCommand{Type: "touchMove", X: 0.6, Y: 0.6})
	h.Handle(stdinCommand{Type: "touchMove", X: 0.7, Y: 0.7})

	h.Handle(stdinCommand{Type: "touchUp", X: 0.8, Y: 0.8})

	calls := mock.getCalls()
	moveCount := 0
	for _, c := range calls {
		if c.method == "TouchMove" {
			moveCount++
		}
	}
	if moveCount != 1 {
		t.Errorf("expected 1 TouchMove (second throttled), got %d", moveCount)
	}
}

func TestHIDHandler_TouchMoveWithoutTouchDown(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	// touchMove without a prior touchDown — stream is nil, should be a no-op.
	h.Handle(stdinCommand{Type: "touchMove", X: 0.5, Y: 0.5})

	calls := mock.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected no calls for touchMove without touchDown, got %d", len(calls))
	}
}

func TestHIDHandler_TouchUpWithoutTouchDown(t *testing.T) {
	mock := &mockHIDClient{}
	h := newHIDHandler(mock, 390, 844)

	// touchUp without a prior touchDown — stream is nil, should be a no-op.
	h.Handle(stdinCommand{Type: "touchUp", X: 0.5, Y: 0.5})

	calls := mock.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected no calls for touchUp without touchDown, got %d", len(calls))
	}
}

// --- Error paths ---

func TestHIDHandler_OpenHIDStreamError(t *testing.T) {
	mock := &mockHIDClient{openStreamErr: fmt.Errorf("connection refused")}
	h := newHIDHandler(mock, 390, 844)

	// Should not panic; stream should remain nil.
	h.Handle(stdinCommand{Type: "touchDown", X: 0.5, Y: 0.5})

	h.mu.Lock()
	stream := h.activeHIDStream
	h.mu.Unlock()
	if stream != nil {
		t.Error("expected activeHIDStream to remain nil after OpenHIDStream error")
	}
}

func TestHIDHandler_TouchDownError_ClosesStream(t *testing.T) {
	stream := &mockHIDStream{}
	mock := &mockHIDClient{
		touchDownErr: fmt.Errorf("send failed"),
		returnStream: stream,
	}
	h := newHIDHandler(mock, 390, 844)

	h.Handle(stdinCommand{Type: "touchDown", X: 0.5, Y: 0.5})

	// activeHIDStream should NOT be stored after TouchDown error.
	h.mu.Lock()
	storedStream := h.activeHIDStream
	h.mu.Unlock()
	if storedStream != nil {
		t.Error("expected activeHIDStream to remain nil after TouchDown error")
	}

	// CloseAndRecv should have been called to prevent stream leak.
	stream.mu.Lock()
	closed := stream.closeRecvCalled
	stream.mu.Unlock()
	if !closed {
		t.Error("expected CloseAndRecv to be called on stream after TouchDown error")
	}
}

// --- Guard conditions ---

func TestHIDHandler_NilReceiver(t *testing.T) {
	// Should not panic on nil receiver.
	var h *hidHandler
	h.Handle(stdinCommand{Type: "tap", X: 0.5, Y: 0.5})
}

func TestHIDHandler_NilClient(t *testing.T) {
	// newHIDHandler returns nil when client is nil.
	h := newHIDHandler(nil, 390, 844)
	if h != nil {
		t.Fatal("expected nil hidHandler for nil client")
	}
}

func TestHIDHandler_ZeroScreenSize(t *testing.T) {
	mock := &mockHIDClient{}
	h := &hidHandler{client: mock, screenWidth: 0, screenHeight: 0}

	// Coordinate-based commands should be skipped.
	h.Handle(stdinCommand{Type: "tap", X: 0.5, Y: 0.5})
	time.Sleep(50 * time.Millisecond)

	calls := mock.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected no calls with zero screen size, got %d", len(calls))
	}
}
