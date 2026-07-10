package simulatorclient

import (
	"context"
	"testing"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

type fakeAPI struct {
	simulatorv1.UnimplementedSimulatorServiceServer
	launchReq *simulatorv1.LaunchAppRequest
}

func (f *fakeAPI) Health(context.Context, *simulatorv1.HealthRequest) (*simulatorv1.HealthResponse, error) {
	return &simulatorv1.HealthResponse{Status: "ok", Version: "test"}, nil
}

func (f *fakeAPI) CreateSession(context.Context, *simulatorv1.CreateSessionRequest) (*simulatorv1.CreateSessionResponse, error) {
	return &simulatorv1.CreateSessionResponse{Session: &simulatorv1.Session{SessionId: "session-1"}}, nil
}

func (f *fakeAPI) LaunchApp(_ context.Context, req *simulatorv1.LaunchAppRequest) (*simulatorv1.LaunchAppResponse, error) {
	f.launchReq = req
	return &simulatorv1.LaunchAppResponse{}, nil
}

func TestClientHealth(t *testing.T) {
	client := New(fakeServiceClient{api: &fakeAPI{}})

	resp, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != "ok" || resp.GetVersion() != "test" {
		t.Fatalf("health = %+v", resp)
	}
}

func TestClientCreateSessionReturnsSession(t *testing.T) {
	client := New(fakeServiceClient{api: &fakeAPI{}})

	session, err := client.CreateSession(context.Background(), &simulatorv1.CreateSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if session.GetSessionId() != "session-1" {
		t.Fatalf("session = %+v", session)
	}
}

func TestClientLaunchApp(t *testing.T) {
	api := &fakeAPI{}
	client := New(fakeServiceClient{api: api})

	err := client.LaunchApp(context.Background(), "session-1", "com.example.App", map[string]string{"A": "B"}, []string{"--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if api.launchReq.GetSessionId() != "session-1" || api.launchReq.GetBundleId() != "com.example.App" {
		t.Fatalf("launch req = %+v", api.launchReq)
	}
	if api.launchReq.GetEnv()["A"] != "B" || len(api.launchReq.GetArgs()) != 1 {
		t.Fatalf("launch details = %+v", api.launchReq)
	}
}
