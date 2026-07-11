package simulatorserver

import (
	"time"

	"github.com/k-kohey/axe/internal/simruntime"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

func deviceTypesToProto(devices []simruntime.DeviceType) []*simulatorv1.DeviceType {
	out := make([]*simulatorv1.DeviceType, 0, len(devices))
	for _, d := range devices {
		runtimes := make([]*simulatorv1.Runtime, 0, len(d.Runtimes))
		for _, r := range d.Runtimes {
			runtimes = append(runtimes, &simulatorv1.Runtime{
				Identifier: r.Identifier,
				Name:       r.Name,
			})
		}
		out = append(out, &simulatorv1.DeviceType{
			Identifier: d.Identifier,
			Name:       d.Name,
			Runtimes:   runtimes,
		})
	}
	return out
}

func createSessionFromProto(req *simulatorv1.CreateSessionRequest) simruntime.CreateSessionRequest {
	out := simruntime.CreateSessionRequest{
		DeviceType: req.GetDeviceType(),
		Runtime:    req.GetRuntime(),
	}
	if app := req.GetAppBundle(); app != nil {
		out.Target.AppBundle = &simruntime.AppBundle{Path: app.GetPath(), BundleID: app.GetBundleId()}
	}
	if app := req.GetInstalledApp(); app != nil {
		out.Target.InstalledApp = &simruntime.InstalledApp{BundleID: app.GetBundleId()}
	}
	return out
}

func sessionToProto(s *simruntime.SessionInfo) *simulatorv1.Session {
	if s == nil {
		return nil
	}
	return &simulatorv1.Session{
		SessionId:    s.ID,
		DeviceUdid:   s.DeviceUDID,
		DeviceType:   s.DeviceType,
		Runtime:      s.Runtime,
		State:        stateToProto(s.State),
		BundleId:     s.BundleID,
		ScreenWidth:  int32(s.ScreenWidth),
		ScreenHeight: int32(s.ScreenHeight),
	}
}

func stateToProto(state simruntime.SessionState) simulatorv1.SessionState {
	switch state {
	case simruntime.SessionStarting:
		return simulatorv1.SessionState_SESSION_STATE_STARTING
	case simruntime.SessionRunning:
		return simulatorv1.SessionState_SESSION_STATE_RUNNING
	case simruntime.SessionStopped:
		return simulatorv1.SessionState_SESSION_STATE_STOPPED
	case simruntime.SessionFailed:
		return simulatorv1.SessionState_SESSION_STATE_FAILED
	default:
		return simulatorv1.SessionState_SESSION_STATE_UNSPECIFIED
	}
}

func inputFromProto(input *simulatorv1.InputEvent) simruntime.InputEvent {
	if input == nil {
		return simruntime.InputEvent{}
	}
	switch {
	case input.GetTouchDown() != nil:
		p := input.GetTouchDown()
		return simruntime.InputEvent{TouchDown: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetTouchMove() != nil:
		p := input.GetTouchMove()
		return simruntime.InputEvent{TouchMove: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetTouchUp() != nil:
		p := input.GetTouchUp()
		return simruntime.InputEvent{TouchUp: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetText() != nil:
		return simruntime.InputEvent{Text: input.GetText().GetValue()}
	case input.GetTap() != nil:
		p := input.GetTap()
		return simruntime.InputEvent{Tap: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetSwipe() != nil:
		s := input.GetSwipe()
		return simruntime.InputEvent{Swipe: &simruntime.Swipe{
			StartX:          s.GetStartX(),
			StartY:          s.GetStartY(),
			EndX:            s.GetEndX(),
			EndY:            s.GetEndY(),
			DurationSeconds: s.GetDurationSeconds(),
		}}
	default:
		return simruntime.InputEvent{}
	}
}

func eventToProto(event simruntime.Event) *simulatorv1.Event {
	out := &simulatorv1.Event{
		SessionId:       event.SessionID,
		TimestampUnixMs: unixMilli(event.Time),
	}
	switch {
	case event.Status != nil:
		out.Payload = &simulatorv1.Event_Status{Status: &simulatorv1.StatusEvent{Phase: event.Status.Phase}}
	case event.Stopped != nil:
		out.Payload = &simulatorv1.Event_Stopped{Stopped: &simulatorv1.StoppedEvent{
			Reason:  event.Stopped.Reason,
			Message: event.Stopped.Message,
		}}
	case event.Error != nil:
		out.Payload = &simulatorv1.Event_Error{Error: &simulatorv1.ErrorEvent{Message: event.Error.Message}}
	}
	return out
}

func videoFrameToProto(frame simruntime.VideoFrame) *simulatorv1.VideoFrame {
	return &simulatorv1.VideoFrame{
		SessionId:       frame.SessionID,
		Jpeg:            frame.JPEG,
		Width:           int32(frame.Width),
		Height:          int32(frame.Height),
		TimestampUnixMs: unixMilli(frame.Time),
	}
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UnixNano() / int64(time.Millisecond)
}
