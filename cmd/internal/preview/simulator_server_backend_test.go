package preview

import (
	"context"
	"testing"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

type fakeSimulatorServerClient struct {
	installedPath string
	launchedID    string
	launchEnv     map[string]string
	launchArgs    []string
	terminatedID  string
	input         *simulatorv1.InputEvent
}

func (c *fakeSimulatorServerClient) InstallApp(_ context.Context, _ string, appPath string) error {
	c.installedPath = appPath
	return nil
}

func (c *fakeSimulatorServerClient) LaunchApp(_ context.Context, _ string, bundleID string, env map[string]string, args []string) error {
	c.launchedID = bundleID
	c.launchEnv = env
	c.launchArgs = args
	return nil
}

func (c *fakeSimulatorServerClient) TerminateApp(_ context.Context, _ string, bundleID string) error {
	c.terminatedID = bundleID
	return nil
}

func (c *fakeSimulatorServerClient) SendInput(_ context.Context, _ string, input *simulatorv1.InputEvent) error {
	c.input = input
	return nil
}

func (c *fakeSimulatorServerClient) WatchVideo(context.Context, string, int) (<-chan *simulatorv1.VideoFrame, <-chan error, error) {
	frames := make(chan *simulatorv1.VideoFrame)
	errs := make(chan error)
	close(frames)
	close(errs)
	return frames, errs, nil
}

func TestSimulatorServerAppRunner(t *testing.T) {
	client := &fakeSimulatorServerClient{}
	runner := newSimulatorServerAppRunner(client, "session-1")

	if err := runner.Install(context.Background(), "ignored-device", "/tmp/App.app", "ignored-set"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Launch(context.Background(), "ignored-device", "com.example.App", "ignored-set", map[string]string{"A": "B"}, []string{"--flag"}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Terminate(context.Background(), "ignored-device", "com.example.App", "ignored-set"); err != nil {
		t.Fatal(err)
	}

	if client.installedPath != "/tmp/App.app" {
		t.Fatalf("installed path = %q", client.installedPath)
	}
	if client.launchedID != "com.example.App" || client.launchEnv["A"] != "B" || client.launchArgs[0] != "--flag" {
		t.Fatalf("launch = id:%q env:%v args:%v", client.launchedID, client.launchEnv, client.launchArgs)
	}
	if client.terminatedID != "com.example.App" {
		t.Fatalf("terminated id = %q", client.terminatedID)
	}
}

func TestSimulatorServerInputHandler(t *testing.T) {
	client := &fakeSimulatorServerClient{}
	handler := newSimulatorServerInputHandler(client, "session-1")

	handler.HandleInput(context.Background(), &pb.Input{
		Event: &pb.Input_TouchDown{TouchDown: &pb.TouchEvent{X: 0.25, Y: 0.75}},
	})

	if client.input.GetTouchDown().GetX() != 0.25 || client.input.GetTouchDown().GetY() != 0.75 {
		t.Fatalf("input = %+v", client.input)
	}
}
