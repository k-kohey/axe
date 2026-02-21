package preview

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/idb"
)

// fakeIDBClient implements idb.IDBClient for testing.
type fakeIDBClient struct {
	screenshotData []byte
	screenshotErr  error
	screenshotN    int
	screenW        int
	screenH        int
	screenErr      error
	videoFrames    [][]byte // frames to send on VideoStream
	videoErr       error    // error from VideoStream open
}

func (f *fakeIDBClient) ScreenSize(_ context.Context) (int, int, error) {
	return f.screenW, f.screenH, f.screenErr
}

func (f *fakeIDBClient) VideoStream(_ context.Context, _ int) (<-chan []byte, error) {
	if f.videoErr != nil {
		return nil, f.videoErr
	}
	ch := make(chan []byte, len(f.videoFrames))
	for _, frame := range f.videoFrames {
		ch <- frame
	}
	close(ch)
	return ch, nil
}

func (f *fakeIDBClient) Tap(_ context.Context, _, _ float64) error            { return nil }
func (f *fakeIDBClient) Swipe(_ context.Context, _, _, _, _, _ float64) error { return nil }
func (f *fakeIDBClient) Text(_ context.Context, _ string) error               { return nil }
func (f *fakeIDBClient) Screenshot(_ context.Context) ([]byte, error) {
	f.screenshotN++
	return f.screenshotData, f.screenshotErr
}
func (f *fakeIDBClient) OpenHIDStream(_ context.Context) (idb.HIDStream, error) {
	return nil, fmt.Errorf("not implemented in fake")
}
func (f *fakeIDBClient) TouchDown(_ idb.HIDStream, _, _ float64) error { return nil }
func (f *fakeIDBClient) TouchMove(_ idb.HIDStream, _, _ float64) error { return nil }
func (f *fakeIDBClient) TouchUp(_ idb.HIDStream, _, _ float64) error   { return nil }
func (f *fakeIDBClient) Close() error                                  { return nil }

// fastRetryConfig is used in tests to avoid slow backoff waits.
var fastRetryConfig = streamRetryConfig{
	maxRetries:     2,
	initialBackoff: time.Millisecond,
	maxBackoff:     time.Millisecond,
}

func TestRelayVideoStream_VideoStreamError(t *testing.T) {
	client := &fakeIDBClient{
		videoErr: fmt.Errorf("connection refused"),
		screenW:  420,
		screenH:  912,
	}
	errCh := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go relayVideoStreamWithConfig(ctx, client, errCh, fastRetryConfig)

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("unexpected error: %v", err)
		}
	case <-ctx.Done():
		t.Error("timed out waiting for error")
	}
}

func TestRelayVideoStream_ContextCancelled(t *testing.T) {
	client := &fakeIDBClient{screenW: 420, screenH: 912}
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	relayVideoStream(ctx, client, errCh)

	select {
	case err := <-errCh:
		t.Errorf("unexpected error when context cancelled: %v", err)
	default:
	}
}

func TestRelayVideoStream_StreamCloses(t *testing.T) {
	// Empty videoFrames = channel closes immediately → triggers retry then error
	client := &fakeIDBClient{videoFrames: nil, screenW: 420, screenH: 912}
	errCh := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go relayVideoStreamWithConfig(ctx, client, errCh, fastRetryConfig)

	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "video stream") {
			t.Errorf("unexpected error: %v", err)
		}
	case <-ctx.Done():
		t.Error("timed out waiting for error")
	}
}

func TestRunVideoStreamLoop_ScreenSizeError(t *testing.T) {
	client := &fakeIDBClient{
		videoFrames: [][]byte{{0x00}},
		screenW:     0,
		screenH:     0,
		screenErr:   fmt.Errorf("describe: rpc error"),
	}

	err := runVideoStreamLoop(context.Background(), client)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "screen size") {
		t.Errorf("expected screen size error, got: %v", err)
	}
}

func TestEncodeRBGAFrame(t *testing.T) {
	const w, h = 4, 4
	// Create a small 4x4 RGBA frame (red pixels).
	data := make([]byte, w*h*4)
	for i := 0; i < len(data); i += 4 {
		data[i] = 0xFF   // R
		data[i+1] = 0x00 // G
		data[i+2] = 0x00 // B
		data[i+3] = 0xFF // A
	}

	var buf bytes.Buffer
	encoded, err := encodeRBGAFrame(data, w, h, &buf)
	if err != nil {
		t.Fatalf("encodeRBGAFrame failed: %v", err)
	}

	// Verify base64 decodes to valid JPEG.
	jpegData, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}

	img, err := jpeg.Decode(bytes.NewReader(jpegData))
	if err != nil {
		t.Fatalf("JPEG decode failed: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != w || bounds.Dy() != h {
		t.Errorf("JPEG dimensions = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), w, h)
	}
}

func TestDetectFrameDimensions(t *testing.T) {
	tests := []struct {
		name     string
		dataSize int
		screenW  int
		screenH  int
		wantW    int
		wantH    int
	}{
		{
			name:     "640x1368 from 420x912 screen",
			dataSize: 640 * 1368 * 4,
			screenW:  420,
			screenH:  912,
			wantW:    640,
			wantH:    1368,
		},
		{
			name:     "1260x2736 from 420x912 screen",
			dataSize: 1260 * 2736 * 4,
			screenW:  420,
			screenH:  912,
			wantW:    1260,
			wantH:    2736,
		},
		{
			name:     "zero screen dimensions",
			dataSize: 640 * 1368 * 4,
			screenW:  0,
			screenH:  0,
			wantW:    0,
			wantH:    0,
		},
		{
			name:     "invalid data size",
			dataSize: 13,
			screenW:  420,
			screenH:  912,
			wantW:    0,
			wantH:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := detectFrameDimensions(tt.dataSize, tt.screenW, tt.screenH)
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("detectFrameDimensions(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tt.dataSize, tt.screenW, tt.screenH, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}
