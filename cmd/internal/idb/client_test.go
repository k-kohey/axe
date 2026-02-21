package idb

import (
	"context"
	"net"
	"testing"

	pb "github.com/k-kohey/axe/internal/idb/idbproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// mockCompanionServer implements the CompanionService for testing.
type mockCompanionServer struct {
	pb.UnimplementedCompanionServiceServer

	describeResp    *pb.TargetDescriptionResponse
	screenshotResp  *pb.ScreenshotResponse
	hidEvents       []*pb.HIDEvent
	videoStartCalls int
}

func (s *mockCompanionServer) Describe(_ context.Context, _ *pb.TargetDescriptionRequest) (*pb.TargetDescriptionResponse, error) {
	return s.describeResp, nil
}

func (s *mockCompanionServer) Screenshot(_ context.Context, _ *pb.ScreenshotRequest) (*pb.ScreenshotResponse, error) {
	return s.screenshotResp, nil
}

func (s *mockCompanionServer) Hid(stream grpc.ClientStreamingServer[pb.HIDEvent, pb.HIDResponse]) error {
	for {
		evt, err := stream.Recv()
		if err != nil {
			break
		}
		s.hidEvents = append(s.hidEvents, evt)
	}
	return stream.SendAndClose(&pb.HIDResponse{})
}

func (s *mockCompanionServer) VideoStream(stream grpc.BidiStreamingServer[pb.VideoStreamRequest, pb.VideoStreamResponse]) error {
	// Read start request.
	_, err := stream.Recv()
	if err != nil {
		return err
	}
	s.videoStartCalls++

	// Send one frame then close.
	_ = stream.Send(&pb.VideoStreamResponse{
		Output: &pb.VideoStreamResponse_Payload{
			Payload: &pb.Payload{
				Source: &pb.Payload_Data{
					Data: []byte("fake-jpeg-frame"),
				},
			},
		},
	})
	return nil
}

func startMockServer(t *testing.T, srv *mockCompanionServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	pb.RegisterCompanionServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

func TestClient_ScreenSize(t *testing.T) {
	srv := &mockCompanionServer{
		describeResp: &pb.TargetDescriptionResponse{
			TargetDescription: &pb.TargetDescription{
				ScreenDimensions: &pb.ScreenDimensions{
					WidthPoints:  390,
					HeightPoints: 844,
				},
			},
		},
	}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	w, h, err := client.ScreenSize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w != 390 || h != 844 {
		t.Errorf("expected 390x844, got %dx%d", w, h)
	}
}

func TestClient_Tap(t *testing.T) {
	srv := &mockCompanionServer{}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	err = client.Tap(context.Background(), 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	if len(srv.hidEvents) != 2 {
		t.Fatalf("expected 2 HID events (down+up), got %d", len(srv.hidEvents))
	}

	down := srv.hidEvents[0].GetPress()
	if down == nil || down.Direction != pb.HIDEvent_DOWN {
		t.Error("first event should be press DOWN")
	}
	touch := down.GetAction().GetTouch()
	if touch == nil || touch.Point.X != 100 || touch.Point.Y != 200 {
		t.Errorf("unexpected touch point: %v", touch)
	}

	up := srv.hidEvents[1].GetPress()
	if up == nil || up.Direction != pb.HIDEvent_UP {
		t.Error("second event should be press UP")
	}
}

func TestClient_Swipe(t *testing.T) {
	srv := &mockCompanionServer{}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	err = client.Swipe(context.Background(), 100, 200, 100, 600, 0.5)
	if err != nil {
		t.Fatal(err)
	}

	if len(srv.hidEvents) != 1 {
		t.Fatalf("expected 1 HID event (swipe), got %d", len(srv.hidEvents))
	}

	swipe := srv.hidEvents[0].GetSwipe()
	if swipe == nil {
		t.Fatal("expected swipe event")
	}
	if swipe.Start.X != 100 || swipe.Start.Y != 200 {
		t.Errorf("unexpected start: %v", swipe.Start)
	}
	if swipe.End.X != 100 || swipe.End.Y != 600 {
		t.Errorf("unexpected end: %v", swipe.End)
	}
	if swipe.Duration != 0.5 {
		t.Errorf("expected duration 0.5, got %f", swipe.Duration)
	}
}

func TestClient_Text(t *testing.T) {
	srv := &mockCompanionServer{}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	err = client.Text(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}

	// "hi" = 2 chars × 2 events (down+up) = 4 events
	if len(srv.hidEvents) != 4 {
		t.Fatalf("expected 4 HID events for 'hi', got %d", len(srv.hidEvents))
	}
}

func TestClient_VideoStream(t *testing.T) {
	srv := &mockCompanionServer{}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames, err := client.VideoStream(ctx, 15)
	if err != nil {
		t.Fatal(err)
	}

	frame := <-frames
	if string(frame) != "fake-jpeg-frame" {
		t.Errorf("unexpected frame: %q", frame)
	}

	if srv.videoStartCalls != 1 {
		t.Errorf("expected 1 video start call, got %d", srv.videoStartCalls)
	}
}

func TestClient_Screenshot(t *testing.T) {
	srv := &mockCompanionServer{
		screenshotResp: &pb.ScreenshotResponse{
			ImageData:   []byte("fake-png"),
			ImageFormat: "PNG",
		},
	}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	data, err := client.Screenshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-png" {
		t.Errorf("unexpected screenshot data: %q", data)
	}
}

func TestClient_TouchDownMoveUp(t *testing.T) {
	srv := &mockCompanionServer{}
	addr := startMockServer(t, srv)

	client, err := NewClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	stream, err := client.OpenHIDStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := client.TouchDown(stream, 100, 200); err != nil {
		t.Fatalf("TouchDown: %v", err)
	}
	if err := client.TouchMove(stream, 100, 300); err != nil {
		t.Fatalf("TouchMove: %v", err)
	}
	if err := client.TouchMove(stream, 100, 400); err != nil {
		t.Fatalf("TouchMove: %v", err)
	}
	if err := client.TouchUp(stream, 100, 500); err != nil {
		t.Fatalf("TouchUp: %v", err)
	}

	// DOWN (touchDown) + DOWN (move1) + DOWN (move2) + UP (touchUp) = 4 events
	if len(srv.hidEvents) != 4 {
		t.Fatalf("expected 4 HID events, got %d", len(srv.hidEvents))
	}

	// Verify first event is DOWN at (100, 200)
	first := srv.hidEvents[0].GetPress()
	if first == nil || first.Direction != pb.HIDEvent_DOWN {
		t.Error("first event should be DOWN")
	}
	if p := first.GetAction().GetTouch().GetPoint(); p.X != 100 || p.Y != 200 {
		t.Errorf("unexpected first point: %v", p)
	}

	// Verify last event is UP at (100, 500)
	last := srv.hidEvents[3].GetPress()
	if last == nil || last.Direction != pb.HIDEvent_UP {
		t.Error("last event should be UP")
	}
	if p := last.GetAction().GetTouch().GetPoint(); p.X != 100 || p.Y != 500 {
		t.Errorf("unexpected last point: %v", p)
	}
}

func TestNewClient_InvalidAddress(t *testing.T) {
	// NewClient should succeed (lazy connection) but operations should fail.
	client, err := NewClient("localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	_, _, err = client.ScreenSize(context.Background())
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

// TestClientImplementsIDBClient verifies the Client satisfies the IDBClient interface.
func TestClientImplementsIDBClient(t *testing.T) {
	// Compile-time check via package-level var _ IDBClient = (*Client)(nil).
	// This test exists for documentation.
	conn, _ := grpc.NewClient("localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	var _ IDBClient = &Client{conn: conn}
}
