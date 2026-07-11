package simulatorserver

import (
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/simruntime"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

func TestCreateSessionFromProto_AppBundle(t *testing.T) {
	req := &simulatorv1.CreateSessionRequest{
		DeviceType: "device",
		Runtime:    "runtime",
		LaunchTarget: &simulatorv1.CreateSessionRequest_AppBundle{
			AppBundle: &simulatorv1.AppBundle{Path: "/tmp/App.app", BundleId: "com.example.App"},
		},
	}

	got := createSessionFromProto(req)
	if got.DeviceType != "device" || got.Runtime != "runtime" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if got.Target.AppBundle == nil {
		t.Fatal("expected app bundle target")
	}
	if got.Target.AppBundle.Path != "/tmp/App.app" || got.Target.AppBundle.BundleID != "com.example.App" {
		t.Fatalf("unexpected app bundle: %+v", got.Target.AppBundle)
	}
}

func TestInputFromProto_Swipe(t *testing.T) {
	got := inputFromProto(&simulatorv1.InputEvent{
		Event: &simulatorv1.InputEvent_Swipe{
			Swipe: &simulatorv1.SwipeEvent{
				StartX: 0.1, StartY: 0.2, EndX: 0.3, EndY: 0.4, DurationSeconds: 0.5,
			},
		},
	})
	if got.Swipe == nil {
		t.Fatal("expected swipe")
	}
	if got.Swipe.StartX != 0.1 || got.Swipe.EndY != 0.4 || got.Swipe.DurationSeconds != 0.5 {
		t.Fatalf("unexpected swipe: %+v", got.Swipe)
	}
}

func TestEventToProto(t *testing.T) {
	at := time.Unix(10, int64(250*time.Millisecond))
	got := eventToProto(simruntime.Event{
		SessionID: "session-1",
		Time:      at,
		Status:    &simruntime.StatusEvent{Phase: "running"},
	})

	if got.GetSessionId() != "session-1" {
		t.Fatalf("session id = %q", got.GetSessionId())
	}
	if got.GetTimestampUnixMs() != 10250 {
		t.Fatalf("timestamp = %d", got.GetTimestampUnixMs())
	}
	if got.GetStatus().GetPhase() != "running" {
		t.Fatalf("status = %+v", got.GetStatus())
	}
}
