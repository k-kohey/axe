package preview

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
)

// streamRetryConfig controls the retry behavior for video stream reconnection.
type streamRetryConfig struct {
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// defaultRetryConfig is the production retry configuration.
var defaultRetryConfig = streamRetryConfig{
	maxRetries:     5,
	initialBackoff: 500 * time.Millisecond,
	maxBackoff:     5 * time.Second,
}

// relayVideoStream opens a raw-pixel video stream from idb_companion, converts
// frames to JPEG, and writes base64-encoded lines to stdout.
// On stream disconnection it retries with exponential backoff.
func relayVideoStream(ctx context.Context, client idb.IDBClient, errCh chan<- error) {
	relayVideoStreamWithConfig(ctx, client, errCh, defaultRetryConfig)
}

func relayVideoStreamWithConfig(ctx context.Context, client idb.IDBClient, errCh chan<- error, cfg streamRetryConfig) {
	backoff := cfg.initialBackoff

	for attempt := 0; ; attempt++ {
		err := runVideoStreamLoop(ctx, client)
		if ctx.Err() != nil {
			return
		}
		if attempt >= cfg.maxRetries {
			errCh <- fmt.Errorf("video stream failed after %d retries: %w", cfg.maxRetries, err)
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
		backoff = min(backoff*2, cfg.maxBackoff)
	}
}

// runVideoStreamLoop handles a single RBGA video stream session.
// idb_companion streams raw RGBA pixels (no inter-frame compression), which
// are converted to JPEG and written as base64 lines to stdout.
//
// RBGA format is used instead of H264 because idb_companion's H264 encoder
// produces severe ghosting artifacts during rapid screen changes.
// See survey/idb_companion_h264_issue.md for details.
func runVideoStreamLoop(ctx context.Context, client idb.IDBClient) error {
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
				frameW, frameH = detectFrameDimensions(len(data), sw, sh)
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

			encoded, err := encodeRBGAFrame(data, frameW, frameH, &buf)
			if err != nil {
				slog.Debug("JPEG encode failed", "err", err)
				continue
			}

			fmt.Println(encoded)
		}
	}
}

// encodeRBGAFrame converts raw RGBA pixel data into a base64-encoded JPEG string.
func encodeRBGAFrame(data []byte, frameW, frameH int, buf *bytes.Buffer) (string, error) {
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

// detectFrameDimensions determines RBGA pixel dimensions from the data size
// and the screen aspect ratio (in points).
func detectFrameDimensions(dataSize, screenW, screenH int) (width, height int) {
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
