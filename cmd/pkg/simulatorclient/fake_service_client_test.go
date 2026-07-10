package simulatorclient

import (
	"context"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/grpc"
)

type fakeServiceClient struct {
	api simulatorv1.SimulatorServiceServer
}

func (c fakeServiceClient) Health(ctx context.Context, in *simulatorv1.HealthRequest, _ ...grpc.CallOption) (*simulatorv1.HealthResponse, error) {
	return c.api.Health(ctx, in)
}

func (c fakeServiceClient) ListDevices(ctx context.Context, in *simulatorv1.ListDevicesRequest, _ ...grpc.CallOption) (*simulatorv1.ListDevicesResponse, error) {
	return c.api.ListDevices(ctx, in)
}

func (c fakeServiceClient) CreateSession(ctx context.Context, in *simulatorv1.CreateSessionRequest, _ ...grpc.CallOption) (*simulatorv1.CreateSessionResponse, error) {
	return c.api.CreateSession(ctx, in)
}

func (c fakeServiceClient) GetSession(ctx context.Context, in *simulatorv1.GetSessionRequest, _ ...grpc.CallOption) (*simulatorv1.Session, error) {
	return c.api.GetSession(ctx, in)
}

func (c fakeServiceClient) StopSession(ctx context.Context, in *simulatorv1.StopSessionRequest, _ ...grpc.CallOption) (*simulatorv1.StopSessionResponse, error) {
	return c.api.StopSession(ctx, in)
}

func (c fakeServiceClient) InstallApp(ctx context.Context, in *simulatorv1.InstallAppRequest, _ ...grpc.CallOption) (*simulatorv1.InstallAppResponse, error) {
	return c.api.InstallApp(ctx, in)
}

func (c fakeServiceClient) LaunchApp(ctx context.Context, in *simulatorv1.LaunchAppRequest, _ ...grpc.CallOption) (*simulatorv1.LaunchAppResponse, error) {
	return c.api.LaunchApp(ctx, in)
}

func (c fakeServiceClient) TerminateApp(ctx context.Context, in *simulatorv1.TerminateAppRequest, _ ...grpc.CallOption) (*simulatorv1.TerminateAppResponse, error) {
	return c.api.TerminateApp(ctx, in)
}

func (c fakeServiceClient) SendInput(ctx context.Context, in *simulatorv1.SendInputRequest, _ ...grpc.CallOption) (*simulatorv1.SendInputResponse, error) {
	return c.api.SendInput(ctx, in)
}

func (c fakeServiceClient) WatchEvents(context.Context, *simulatorv1.WatchEventsRequest, ...grpc.CallOption) (simulatorv1.SimulatorService_WatchEventsClient, error) {
	panic("not implemented")
}

func (c fakeServiceClient) WatchVideo(context.Context, *simulatorv1.WatchVideoRequest, ...grpc.CallOption) (simulatorv1.SimulatorService_WatchVideoClient, error) {
	panic("not implemented")
}
