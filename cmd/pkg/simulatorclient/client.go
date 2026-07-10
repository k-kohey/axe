package simulatorclient

import (
	"context"
	"fmt"
	"io"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn *grpc.ClientConn
	api  simulatorv1.SimulatorServiceClient
}

func Dial(ctx context.Context, addr string, opts ...grpc.DialOption) (*Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("simulator server address is required")
	}
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	dialOpts = append(dialOpts, opts...)
	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, api: simulatorv1.NewSimulatorServiceClient(conn)}, nil
}

func New(api simulatorv1.SimulatorServiceClient) *Client {
	return &Client{api: api}
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Health(ctx context.Context) (*simulatorv1.HealthResponse, error) {
	return c.api.Health(ctx, &simulatorv1.HealthRequest{})
}

func (c *Client) ListDevices(ctx context.Context) (*simulatorv1.ListDevicesResponse, error) {
	return c.api.ListDevices(ctx, &simulatorv1.ListDevicesRequest{})
}

func (c *Client) CreateSession(ctx context.Context, req *simulatorv1.CreateSessionRequest) (*simulatorv1.Session, error) {
	resp, err := c.api.CreateSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.GetSession(), nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (*simulatorv1.Session, error) {
	return c.api.GetSession(ctx, &simulatorv1.GetSessionRequest{SessionId: sessionID})
}

func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	_, err := c.api.StopSession(ctx, &simulatorv1.StopSessionRequest{SessionId: sessionID})
	return err
}

func (c *Client) InstallApp(ctx context.Context, sessionID, appPath string) error {
	_, err := c.api.InstallApp(ctx, &simulatorv1.InstallAppRequest{SessionId: sessionID, AppPath: appPath})
	return err
}

func (c *Client) LaunchApp(ctx context.Context, sessionID, bundleID string, env map[string]string, args []string) error {
	_, err := c.api.LaunchApp(ctx, &simulatorv1.LaunchAppRequest{
		SessionId: sessionID,
		BundleId:  bundleID,
		Env:       env,
		Args:      args,
	})
	return err
}

func (c *Client) TerminateApp(ctx context.Context, sessionID, bundleID string) error {
	_, err := c.api.TerminateApp(ctx, &simulatorv1.TerminateAppRequest{SessionId: sessionID, BundleId: bundleID})
	return err
}

func (c *Client) SendInput(ctx context.Context, sessionID string, input *simulatorv1.InputEvent) error {
	_, err := c.api.SendInput(ctx, &simulatorv1.SendInputRequest{SessionId: sessionID, Input: input})
	return err
}

func (c *Client) WatchEvents(ctx context.Context, sessionID string) (<-chan *simulatorv1.Event, <-chan error, error) {
	stream, err := c.api.WatchEvents(ctx, &simulatorv1.WatchEventsRequest{SessionId: sessionID})
	if err != nil {
		return nil, nil, err
	}
	out := make(chan *simulatorv1.Event, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for {
			event, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					errCh <- err
				}
				return
			}
			select {
			case out <- event:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
	}()
	return out, errCh, nil
}

func (c *Client) WatchVideo(ctx context.Context, sessionID string, fps int) (<-chan *simulatorv1.VideoFrame, <-chan error, error) {
	stream, err := c.api.WatchVideo(ctx, &simulatorv1.WatchVideoRequest{SessionId: sessionID, Fps: int32(fps)})
	if err != nil {
		return nil, nil, err
	}
	out := make(chan *simulatorv1.VideoFrame, 2)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		for {
			frame, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					errCh <- err
				}
				return
			}
			select {
			case out <- frame:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
	}()
	return out, errCh, nil
}
