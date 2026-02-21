package idb

import (
	"context"
	"fmt"

	pb "github.com/k-kohey/axe/internal/idb/idbproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps the idb_companion gRPC connection.
type Client struct {
	conn   *grpc.ClientConn
	client pb.CompanionServiceClient
}

// NewClient connects to idb_companion at the given address (host:port).
func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connecting to idb_companion at %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: pb.NewCompanionServiceClient(conn),
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// ScreenSize returns the device screen dimensions in points.
func (c *Client) ScreenSize(ctx context.Context) (width, height int, err error) {
	resp, err := c.client.Describe(ctx, &pb.TargetDescriptionRequest{})
	if err != nil {
		return 0, 0, fmt.Errorf("describe: %w", err)
	}
	td := resp.GetTargetDescription()
	if td == nil || td.GetScreenDimensions() == nil {
		return 0, 0, fmt.Errorf("no screen dimensions in target description")
	}
	sd := td.GetScreenDimensions()
	return int(sd.GetWidthPoints()), int(sd.GetHeightPoints()), nil
}

// VideoStream starts streaming video frames at the given FPS using RBGA (raw pixel) format.
// Returns a channel that receives raw RGBA pixel data per frame.
// The channel is closed when the stream ends or the context is cancelled.
// The caller must cancel ctx to stop the stream; the goroutine will send
// a Stop message to idb_companion before closing.
//
// RBGA format is used instead of H264 because idb_companion's H264 encoder
// produces severe ghosting artifacts during rapid screen changes.
func (c *Client) VideoStream(ctx context.Context, fps int) (<-chan []byte, error) {
	stream, err := c.client.VideoStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting video stream: %w", err)
	}

	// Use RBGA (raw pixels) with ScaleFactor 0.5 to halve the resolution.
	// Each frame is ~3.5 MB at half resolution, which is manageable over local gRPC.
	// Do NOT call CloseSend — idb_companion requires the request stream
	// to stay open until a Stop message is sent.
	err = stream.Send(&pb.VideoStreamRequest{
		Control: &pb.VideoStreamRequest_Start_{
			Start: &pb.VideoStreamRequest_Start{
				Fps:         uint64(fps),
				Format:      pb.VideoStreamRequest_RBGA,
				ScaleFactor: 0.5,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("sending video stream start: %w", err)
	}

	frameCh := make(chan []byte, 4)
	go func() {
		defer close(frameCh)
		defer func() {
			// Graceful shutdown: send Stop then close the request stream.
			_ = stream.Send(&pb.VideoStreamRequest{
				Control: &pb.VideoStreamRequest_Stop_{
					Stop: &pb.VideoStreamRequest_Stop{},
				},
			})
			_ = stream.CloseSend()
		}()
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			if payload := resp.GetPayload(); payload != nil {
				if data := payload.GetData(); len(data) > 0 {
					select {
					case frameCh <- data:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return frameCh, nil
}

// Tap sends a tap event at the given pixel coordinates.
func (c *Client) Tap(ctx context.Context, x, y float64) error {
	stream, err := c.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("opening HID stream: %w", err)
	}

	point := &pb.Point{X: x, Y: y}

	// Press down
	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Press{
			Press: &pb.HIDEvent_HIDPress{
				Action: &pb.HIDEvent_HIDPressAction{
					Action: &pb.HIDEvent_HIDPressAction_Touch{
						Touch: &pb.HIDEvent_HIDTouch{Point: point},
					},
				},
				Direction: pb.HIDEvent_DOWN,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID press down: %w", err)
	}

	// Press up
	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Press{
			Press: &pb.HIDEvent_HIDPress{
				Action: &pb.HIDEvent_HIDPressAction{
					Action: &pb.HIDEvent_HIDPressAction_Touch{
						Touch: &pb.HIDEvent_HIDTouch{Point: point},
					},
				},
				Direction: pb.HIDEvent_UP,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID press up: %w", err)
	}

	_, err = stream.CloseAndRecv()
	return err
}

// Swipe sends a swipe gesture from (startX, startY) to (endX, endY) over durationSec seconds.
func (c *Client) Swipe(ctx context.Context, startX, startY, endX, endY float64, durationSec float64) error {
	stream, err := c.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("opening HID stream: %w", err)
	}

	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Swipe{
			Swipe: &pb.HIDEvent_HIDSwipe{
				Start:    &pb.Point{X: startX, Y: startY},
				End:      &pb.Point{X: endX, Y: endY},
				Duration: durationSec,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID swipe: %w", err)
	}

	_, err = stream.CloseAndRecv()
	return err
}

// Text types the given text on the simulator by sending individual key events.
// Each character is sent as a separate HID key press/release pair.
func (c *Client) Text(ctx context.Context, text string) error {
	stream, err := c.client.Hid(ctx)
	if err != nil {
		return fmt.Errorf("opening HID stream: %w", err)
	}

	for _, ch := range text {
		keycode := uint64(ch)
		// Press down
		if err := stream.Send(&pb.HIDEvent{
			Event: &pb.HIDEvent_Press{
				Press: &pb.HIDEvent_HIDPress{
					Action: &pb.HIDEvent_HIDPressAction{
						Action: &pb.HIDEvent_HIDPressAction_Key{
							Key: &pb.HIDEvent_HIDKey{Keycode: keycode},
						},
					},
					Direction: pb.HIDEvent_DOWN,
				},
			},
		}); err != nil {
			return fmt.Errorf("HID key down: %w", err)
		}
		// Press up
		if err := stream.Send(&pb.HIDEvent{
			Event: &pb.HIDEvent_Press{
				Press: &pb.HIDEvent_HIDPress{
					Action: &pb.HIDEvent_HIDPressAction{
						Action: &pb.HIDEvent_HIDPressAction_Key{
							Key: &pb.HIDEvent_HIDKey{Keycode: keycode},
						},
					},
					Direction: pb.HIDEvent_UP,
				},
			},
		}); err != nil {
			return fmt.Errorf("HID key up: %w", err)
		}
	}

	_, err = stream.CloseAndRecv()
	return err
}

// OpenHIDStream opens a persistent HID gRPC stream for real-time touch input.
// The caller uses TouchDown/TouchMove/TouchUp to send events, and TouchUp closes
// the stream.
func (c *Client) OpenHIDStream(ctx context.Context) (pb.CompanionService_HidClient, error) {
	stream, err := c.client.Hid(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening HID stream: %w", err)
	}
	return stream, nil
}

// TouchDown sends a finger-down event on an open HID stream.
func (c *Client) TouchDown(stream pb.CompanionService_HidClient, x, y float64) error {
	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Press{
			Press: &pb.HIDEvent_HIDPress{
				Action: &pb.HIDEvent_HIDPressAction{
					Action: &pb.HIDEvent_HIDPressAction_Touch{
						Touch: &pb.HIDEvent_HIDTouch{Point: &pb.Point{X: x, Y: y}},
					},
				},
				Direction: pb.HIDEvent_DOWN,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID touch down: %w", err)
	}
	return nil
}

// TouchMove sends a finger-move event on an open HID stream.
// idb's HID proto only has DOWN/UP directions; a continuous DOWN on the same
// stream is interpreted as touchesMoved by idb_companion.
func (c *Client) TouchMove(stream pb.CompanionService_HidClient, x, y float64) error {
	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Press{
			Press: &pb.HIDEvent_HIDPress{
				Action: &pb.HIDEvent_HIDPressAction{
					Action: &pb.HIDEvent_HIDPressAction_Touch{
						Touch: &pb.HIDEvent_HIDTouch{Point: &pb.Point{X: x, Y: y}},
					},
				},
				Direction: pb.HIDEvent_DOWN,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID touch move: %w", err)
	}
	return nil
}

// TouchUp sends a finger-up event and closes the HID stream.
func (c *Client) TouchUp(stream pb.CompanionService_HidClient, x, y float64) error {
	if err := stream.Send(&pb.HIDEvent{
		Event: &pb.HIDEvent_Press{
			Press: &pb.HIDEvent_HIDPress{
				Action: &pb.HIDEvent_HIDPressAction{
					Action: &pb.HIDEvent_HIDPressAction_Touch{
						Touch: &pb.HIDEvent_HIDTouch{Point: &pb.Point{X: x, Y: y}},
					},
				},
				Direction: pb.HIDEvent_UP,
			},
		},
	}); err != nil {
		return fmt.Errorf("HID touch up: %w", err)
	}
	_, err := stream.CloseAndRecv()
	return err
}

// Screenshot takes a single screenshot and returns the image data.
func (c *Client) Screenshot(ctx context.Context) ([]byte, error) {
	resp, err := c.client.Screenshot(ctx, &pb.ScreenshotRequest{})
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return resp.GetImageData(), nil
}

// Ensure Client satisfies the IDBClient interface at compile time.
var _ IDBClient = (*Client)(nil)

// HIDStream is a client-streaming gRPC connection for HID events.
type HIDStream = pb.CompanionService_HidClient

// IDBClient defines the interface for idb operations, enabling mock injection in tests.
type IDBClient interface {
	ScreenSize(ctx context.Context) (width, height int, err error)
	VideoStream(ctx context.Context, fps int) (<-chan []byte, error)
	Tap(ctx context.Context, x, y float64) error
	Swipe(ctx context.Context, startX, startY, endX, endY float64, durationSec float64) error
	Text(ctx context.Context, text string) error
	Screenshot(ctx context.Context) ([]byte, error)
	OpenHIDStream(ctx context.Context) (HIDStream, error)
	TouchDown(stream HIDStream, x, y float64) error
	TouchMove(stream HIDStream, x, y float64) error
	TouchUp(stream HIDStream, x, y float64) error
	Close() error
}
