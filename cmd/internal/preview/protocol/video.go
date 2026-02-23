package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"math"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// StreamRetryConfig controls the retry behavior for video stream reconnection.
type StreamRetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultRetryConfig is the production retry configuration.
var DefaultRetryConfig = StreamRetryConfig{
	MaxRetries:     5,
	InitialBackoff: 500 * time.Millisecond,
	MaxBackoff:     5 * time.Second,
}

// VideoOutputConfig controls how video frames are output.
// When EW is non-nil, frames are sent as JSON Lines Events.
// When EW is nil, frames are written as raw base64 lines to stdout (legacy mode).
type VideoOutputConfig struct {
	EW       *EventWriter
	StreamID string
	Device   string
	File     string
}

// RelayVideoStream opens a raw-pixel video stream from idb_companion, converts
// frames to JPEG, and writes base64-encoded lines to stdout.
// On stream disconnection it retries with exponential backoff.
func RelayVideoStream(ctx context.Context, client idb.IDBClient, errCh chan<- error) {
	RelayVideoStreamWithConfig(ctx, client, errCh, DefaultRetryConfig, nil)
}

// RelayVideoStreamEvents is the serve-mode variant that outputs JSON Lines Events.
func RelayVideoStreamEvents(ctx context.Context, client idb.IDBClient, errCh chan<- error, voc *VideoOutputConfig) {
	RelayVideoStreamWithConfig(ctx, client, errCh, DefaultRetryConfig, voc)
}

// RelayVideoStreamWithConfig is the configurable video relay implementation.
func RelayVideoStreamWithConfig(ctx context.Context, client idb.IDBClient, errCh chan<- error, cfg StreamRetryConfig, voc *VideoOutputConfig) {
	backoff := cfg.InitialBackoff

	for attempt := 0; ; attempt++ {
		err := RunVideoStreamLoop(ctx, client, voc)
		if ctx.Err() != nil {
			return
		}
		if attempt >= cfg.MaxRetries {
			errCh <- fmt.Errorf("video stream failed after %d retries: %w", cfg.MaxRetries, err)
			return
		}
		slog.Warn("video stream disconnected, reconnecting",
			"attempt", attempt+1,
			"backoff", backoff,
			"err", err,
		)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, cfg.MaxBackoff)
	}
}

// RunVideoStreamLoop handles a single RBGA video stream session.
// idb_companion streams raw RGBA pixels (no inter-frame compression), which
// are converted to JPEG and written as base64 lines to stdout.
//
// RBGA format is used instead of H264 because idb_companion's H264 encoder
// produces severe ghosting artifacts during rapid screen changes.
// See survey/idb_companion_h264_issue.md for details.
func RunVideoStreamLoop(ctx context.Context, client idb.IDBClient, voc *VideoOutputConfig) error {
	frameCh, err := client.VideoStream(ctx, 30)
	if err != nil {
		return fmt.Errorf("video stream open: %w", err)
	}

	// Get screen dimensions to compute RBGA pixel dimensions.
	sw, sh, err := client.ScreenSize(ctx)
	if err != nil {
		return fmt.Errorf("screen size: %w", err)
	}

	var frameW, frameH int
	var buf bytes.Buffer

	for {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-frameCh:
			if !ok {
				return fmt.Errorf("video stream closed unexpectedly")
			}

			// Drain: RBGA frames are independent (no inter-frame dependencies),
			// so we can safely skip to the latest queued frame.
		drain:
			for {
				select {
				case newer, ok := <-frameCh:
					if !ok {
						return fmt.Errorf("video stream closed unexpectedly")
					}
					data = newer
				default:
					break drain
				}
			}

			// Detect frame dimensions from the first frame.
			if frameW == 0 {
				frameW, frameH = DetectFrameDimensions(len(data), sw, sh)
				if frameW == 0 {
					slog.Warn("cannot determine RBGA frame dimensions",
						"dataSize", len(data), "screen", fmt.Sprintf("%dx%d", sw, sh))
					continue
				}
				slog.Debug("RBGA frame dimensions", "width", frameW, "height", frameH)
			}

			if len(data) != frameW*frameH*4 {
				slog.Debug("RBGA frame size mismatch, skipping",
					"got", len(data), "want", frameW*frameH*4)
				continue
			}

			encoded, err := EncodeRBGAFrame(data, frameW, frameH, &buf)
			if err != nil {
				slog.Debug("JPEG encode failed", "err", err)
				continue
			}

			if voc != nil && voc.EW != nil {
				if sendErr := voc.EW.Send(&pb.Event{
					StreamId: voc.StreamID,
					Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: voc.Device, File: voc.File, Data: encoded}},
				}); sendErr != nil {
					return fmt.Errorf("frame send: %w", sendErr)
				}
			} else {
				fmt.Println(encoded)
			}
		}
	}
}

// EncodeRBGAFrame converts raw RGBA pixel data into a base64-encoded JPEG string.
func EncodeRBGAFrame(data []byte, frameW, frameH int, buf *bytes.Buffer) (string, error) {
	img := &image.NRGBA{
		Pix:    data,
		Stride: frameW * 4,
		Rect:   image.Rect(0, 0, frameW, frameH),
	}
	buf.Reset()
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// DetectFrameDimensions determines RBGA pixel dimensions from the data size
// and the screen aspect ratio (in points).
func DetectFrameDimensions(dataSize, screenW, screenH int) (width, height int) {
	if dataSize%4 != 0 || screenW == 0 || screenH == 0 {
		return 0, 0
	}
	totalPixels := dataSize / 4
	aspect := float64(screenW) / float64(screenH)

	approxW := int(math.Sqrt(float64(totalPixels) * aspect))
	for w := approxW - 20; w <= approxW+20; w++ {
		if w <= 0 {
			continue
		}
		if totalPixels%w == 0 {
			h := totalPixels / w
			if math.Abs(float64(w)/float64(h)-aspect) < 0.05 {
				return w, h
			}
		}
	}
	return 0, 0
}
