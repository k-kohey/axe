package preview

import (
	"context"
	"encoding/base64"
	"fmt"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"github.com/k-kohey/axe/pkg/simulatorclient"
)

type simulatorServerClient interface {
	InstallApp(ctx context.Context, sessionID, appPath string) error
	LaunchApp(ctx context.Context, sessionID, bundleID string, env map[string]string, args []string) error
	TerminateApp(ctx context.Context, sessionID, bundleID string) error
	SendInput(ctx context.Context, sessionID string, input *simulatorv1.InputEvent) error
	WatchVideo(ctx context.Context, sessionID string, fps int) (<-chan *simulatorv1.VideoFrame, <-chan error, error)
}

var _ simulatorServerClient = (*simulatorclient.Client)(nil)

type simulatorServerAppRunner struct {
	client    simulatorServerClient
	sessionID string
}

func newSimulatorServerAppRunner(client simulatorServerClient, sessionID string) *simulatorServerAppRunner {
	return &simulatorServerAppRunner{client: client, sessionID: sessionID}
}

func (r *simulatorServerAppRunner) Terminate(ctx context.Context, _ string, bundleID, _ string) error {
	return r.client.TerminateApp(ctx, r.sessionID, bundleID)
}

func (r *simulatorServerAppRunner) Install(ctx context.Context, _ string, appPath, _ string) error {
	return r.client.InstallApp(ctx, r.sessionID, appPath)
}

func (r *simulatorServerAppRunner) Launch(ctx context.Context, _ string, bundleID, _ string, env map[string]string, args []string) error {
	return r.client.LaunchApp(ctx, r.sessionID, bundleID, env, args)
}

type simulatorServerInputHandler struct {
	client    simulatorServerClient
	sessionID string
}

func newSimulatorServerInputHandler(client simulatorServerClient, sessionID string) *simulatorServerInputHandler {
	return &simulatorServerInputHandler{client: client, sessionID: sessionID}
}

func (h *simulatorServerInputHandler) HandleInput(ctx context.Context, input *pb.Input) {
	if h == nil || input == nil {
		return
	}
	_ = h.client.SendInput(ctx, h.sessionID, inputToSimulatorEvent(input))
}

func inputToSimulatorEvent(input *pb.Input) *simulatorv1.InputEvent {
	switch {
	case input.GetTouchDown() != nil:
		p := input.GetTouchDown()
		return &simulatorv1.InputEvent{Event: &simulatorv1.InputEvent_TouchDown{TouchDown: &simulatorv1.TouchEvent{X: p.GetX(), Y: p.GetY()}}}
	case input.GetTouchMove() != nil:
		p := input.GetTouchMove()
		return &simulatorv1.InputEvent{Event: &simulatorv1.InputEvent_TouchMove{TouchMove: &simulatorv1.TouchEvent{X: p.GetX(), Y: p.GetY()}}}
	case input.GetTouchUp() != nil:
		p := input.GetTouchUp()
		return &simulatorv1.InputEvent{Event: &simulatorv1.InputEvent_TouchUp{TouchUp: &simulatorv1.TouchEvent{X: p.GetX(), Y: p.GetY()}}}
	case input.GetText() != nil:
		return &simulatorv1.InputEvent{Event: &simulatorv1.InputEvent_Text{Text: &simulatorv1.TextEvent{Value: input.GetText().GetValue()}}}
	default:
		return &simulatorv1.InputEvent{}
	}
}

func relaySimulatorServerVideo(ctx context.Context, client simulatorServerClient, sessionID string, ew *protocol.EventWriter, streamID, device, file string, errCh chan<- error) {
	frames, frameErrCh, err := client.WatchVideo(ctx, sessionID, 30)
	if err != nil {
		errCh <- err
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-frameErrCh:
			if ok && err != nil {
				errCh <- err
			}
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if ew == nil {
				continue
			}
			encoded := base64.StdEncoding.EncodeToString(frame.GetJpeg())
			if err := ew.Send(&pb.Event{
				StreamId: streamID,
				Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: device, File: file, Data: encoded}},
			}); err != nil {
				errCh <- fmt.Errorf("frame send: %w", err)
				return
			}
		}
	}
}
