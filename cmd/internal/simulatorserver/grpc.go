package simulatorserver

import (
	"context"

	"github.com/k-kohey/axe/internal/simruntime"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

type GRPCServer struct {
	simulatorv1.UnimplementedSimulatorServiceServer
	runtime simruntime.Manager
	version string
}

func NewGRPCServer(runtime simruntime.Manager, version string) *GRPCServer {
	return &GRPCServer{runtime: runtime, version: version}
}

func (s *GRPCServer) Health(context.Context, *simulatorv1.HealthRequest) (*simulatorv1.HealthResponse, error) {
	return &simulatorv1.HealthResponse{Status: "ok", Version: s.version}, nil
}

func (s *GRPCServer) ListDevices(ctx context.Context, _ *simulatorv1.ListDevicesRequest) (*simulatorv1.ListDevicesResponse, error) {
	devices, err := s.runtime.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	return &simulatorv1.ListDevicesResponse{DeviceTypes: deviceTypesToProto(devices)}, nil
}

func (s *GRPCServer) ListManagedDevices(ctx context.Context, _ *simulatorv1.ListManagedDevicesRequest) (*simulatorv1.ListManagedDevicesResponse, error) {
	devices, err := s.runtime.ListManagedDevices(ctx)
	if err != nil {
		return nil, err
	}
	return &simulatorv1.ListManagedDevicesResponse{Devices: managedDevicesToProto(devices)}, nil
}

func (s *GRPCServer) AddManagedDevice(ctx context.Context, req *simulatorv1.AddManagedDeviceRequest) (*simulatorv1.AddManagedDeviceResponse, error) {
	device, err := s.runtime.AddManagedDevice(ctx, addManagedDeviceFromProto(req))
	if err != nil {
		return nil, err
	}
	return &simulatorv1.AddManagedDeviceResponse{Device: managedDeviceToProto(*device)}, nil
}

func (s *GRPCServer) RemoveManagedDevice(ctx context.Context, req *simulatorv1.RemoveManagedDeviceRequest) (*simulatorv1.RemoveManagedDeviceResponse, error) {
	if err := s.runtime.RemoveManagedDevice(ctx, req.GetUdid()); err != nil {
		return nil, err
	}
	return &simulatorv1.RemoveManagedDeviceResponse{}, nil
}

func (s *GRPCServer) SetDefaultManagedDevice(ctx context.Context, req *simulatorv1.SetDefaultManagedDeviceRequest) (*simulatorv1.SetDefaultManagedDeviceResponse, error) {
	if err := s.runtime.SetDefaultManagedDevice(ctx, req.GetUdid()); err != nil {
		return nil, err
	}
	return &simulatorv1.SetDefaultManagedDeviceResponse{}, nil
}

func (s *GRPCServer) CreateSession(ctx context.Context, req *simulatorv1.CreateSessionRequest) (*simulatorv1.CreateSessionResponse, error) {
	session, err := s.runtime.CreateSession(ctx, createSessionFromProto(req))
	if err != nil {
		return nil, err
	}
	return &simulatorv1.CreateSessionResponse{Session: sessionToProto(session)}, nil
}

func (s *GRPCServer) ListSessions(context.Context, *simulatorv1.ListSessionsRequest) (*simulatorv1.ListSessionsResponse, error) {
	return &simulatorv1.ListSessionsResponse{Sessions: sessionsToProto(s.runtime.ListSessions())}, nil
}

func (s *GRPCServer) GetSession(_ context.Context, req *simulatorv1.GetSessionRequest) (*simulatorv1.Session, error) {
	session, err := s.runtime.GetSession(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	return sessionToProto(session), nil
}

func (s *GRPCServer) StopSession(ctx context.Context, req *simulatorv1.StopSessionRequest) (*simulatorv1.StopSessionResponse, error) {
	if err := s.runtime.StopSession(ctx, req.GetSessionId()); err != nil {
		return nil, err
	}
	return &simulatorv1.StopSessionResponse{}, nil
}

func (s *GRPCServer) InstallApp(ctx context.Context, req *simulatorv1.InstallAppRequest) (*simulatorv1.InstallAppResponse, error) {
	if err := s.runtime.InstallApp(ctx, req.GetSessionId(), req.GetAppPath()); err != nil {
		return nil, err
	}
	return &simulatorv1.InstallAppResponse{}, nil
}

func (s *GRPCServer) LaunchApp(ctx context.Context, req *simulatorv1.LaunchAppRequest) (*simulatorv1.LaunchAppResponse, error) {
	if err := s.runtime.LaunchApp(ctx, req.GetSessionId(), req.GetBundleId(), req.GetEnv(), req.GetArgs()); err != nil {
		return nil, err
	}
	return &simulatorv1.LaunchAppResponse{}, nil
}

func (s *GRPCServer) TerminateApp(ctx context.Context, req *simulatorv1.TerminateAppRequest) (*simulatorv1.TerminateAppResponse, error) {
	if err := s.runtime.TerminateApp(ctx, req.GetSessionId(), req.GetBundleId()); err != nil {
		return nil, err
	}
	return &simulatorv1.TerminateAppResponse{}, nil
}

func (s *GRPCServer) SendInput(ctx context.Context, req *simulatorv1.SendInputRequest) (*simulatorv1.SendInputResponse, error) {
	if err := s.runtime.SendInput(ctx, req.GetSessionId(), inputFromProto(req.GetInput())); err != nil {
		return nil, err
	}
	return &simulatorv1.SendInputResponse{}, nil
}

func (s *GRPCServer) WatchEvents(req *simulatorv1.WatchEventsRequest, stream simulatorv1.SimulatorService_WatchEventsServer) error {
	events, err := s.runtime.SubscribeEvents(stream.Context(), req.GetSessionId())
	if err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(eventToProto(event)); err != nil {
				return err
			}
		}
	}
}

func (s *GRPCServer) WatchVideo(req *simulatorv1.WatchVideoRequest, stream simulatorv1.SimulatorService_WatchVideoServer) error {
	frames, err := s.runtime.WatchVideo(stream.Context(), req.GetSessionId(), int(req.GetFps()))
	if err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case frame, ok := <-frames:
			if !ok {
				return nil
			}
			if err := stream.Send(videoFrameToProto(frame)); err != nil {
				return err
			}
		}
	}
}
