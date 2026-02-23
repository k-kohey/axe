package preview

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// hidClient is the HID-relevant subset of idb.IDBClient.
// Extracting this interface allows hidHandler to be tested with lightweight mocks.
type hidClient interface {
	Tap(ctx context.Context, x, y float64) error
	Swipe(ctx context.Context, startX, startY, endX, endY float64, durationSec float64) error
	Text(ctx context.Context, text string) error
	OpenHIDStream(ctx context.Context) (idb.HIDStream, error)
	TouchDown(stream idb.HIDStream, x, y float64) error
	TouchMove(stream idb.HIDStream, x, y float64) error
	TouchUp(stream idb.HIDStream, x, y float64) error
}

// hidHandler processes HID input commands (tap, swipe, text, touch gestures)
// with its own mutex, independent of the file-watcher state.
type hidHandler struct {
	client       hidClient
	screenWidth  int
	screenHeight int

	mu              sync.Mutex
	activeHIDStream idb.HIDStream
	lastMoveTime    time.Time
}

// newHIDHandler creates a hidHandler. Returns nil when client is nil,
// so callers can safely call HandleInput on a nil receiver.
func newHIDHandler(client hidClient, screenWidth, screenHeight int) *hidHandler {
	if client == nil {
		return nil
	}
	return &hidHandler{
		client:       client,
		screenWidth:  screenWidth,
		screenHeight: screenHeight,
	}
}

// HandleInput dispatches a protocol Input message to the appropriate HID handler.
// Safe to call on a nil receiver.
func (h *hidHandler) HandleInput(ctx context.Context, input *pb.Input) {
	if h == nil || h.client == nil || input == nil {
		return
	}
	// text input does not require screen coordinates.
	if input.GetText() != nil {
		h.handleText(ctx, input.GetText().GetValue())
		return
	}
	if h.screenWidth <= 0 || h.screenHeight <= 0 {
		return
	}
	switch {
	case input.GetTouchDown() != nil:
		h.handleTouchDown(ctx, input.GetTouchDown().GetX(), input.GetTouchDown().GetY())
	case input.GetTouchMove() != nil:
		h.handleTouchMove(input.GetTouchMove().GetX(), input.GetTouchMove().GetY())
	case input.GetTouchUp() != nil:
		h.handleTouchUp(input.GetTouchUp().GetX(), input.GetTouchUp().GetY())
	}
}

// HandleTap dispatches a tap at normalized coordinates (0.0–1.0).
// Safe to call on a nil receiver. Used by the legacy stdin command path,
// since the protocol Input message does not define a tap type
// (tap is only available in the legacy JSON command format).
func (h *hidHandler) HandleTap(ctx context.Context, x, y float64) {
	if h == nil || h.client == nil {
		return
	}
	if h.screenWidth <= 0 || h.screenHeight <= 0 {
		return
	}
	h.handleTap(ctx, x, y)
}

// HandleSwipe dispatches a swipe gesture at normalized coordinates.
// Safe to call on a nil receiver. Used by the legacy stdin command path,
// since the protocol Input message does not define a swipe type.
func (h *hidHandler) HandleSwipe(ctx context.Context, startX, startY, endX, endY, duration float64) {
	if h == nil || h.client == nil {
		return
	}
	if h.screenWidth <= 0 || h.screenHeight <= 0 {
		return
	}
	h.handleSwipe(ctx, startX, startY, endX, endY, duration)
}

func (h *hidHandler) handleTap(ctx context.Context, x, y float64) {
	sw, sh := h.screenWidth, h.screenHeight
	go func() {
		if err := h.client.Tap(ctx, x*float64(sw), y*float64(sh)); err != nil {
			slog.Warn("Tap failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleSwipe(ctx context.Context, startX, startY, endX, endY, duration float64) {
	sw, sh := h.screenWidth, h.screenHeight
	dur := duration
	if dur <= 0 {
		dur = 0.5
	}
	go func() {
		if err := h.client.Swipe(ctx,
			startX*float64(sw), startY*float64(sh),
			endX*float64(sw), endY*float64(sh),
			dur); err != nil {
			slog.Warn("Swipe failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleText(ctx context.Context, value string) {
	if value == "" {
		return
	}
	go func() {
		if err := h.client.Text(ctx, value); err != nil {
			slog.Warn("Text input failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleTouchDown(ctx context.Context, x, y float64) {
	sw, sh := h.screenWidth, h.screenHeight

	// Close any existing stream first (e.g. if a previous touchUp was lost).
	h.mu.Lock()
	old := h.activeHIDStream
	h.activeHIDStream = nil
	h.mu.Unlock()
	if old != nil {
		_, _ = old.CloseAndRecv()
	}

	stream, err := h.client.OpenHIDStream(ctx)
	if err != nil {
		slog.Warn("OpenHIDStream failed", "err", err)
		return
	}
	if err := h.client.TouchDown(stream, x*float64(sw), y*float64(sh)); err != nil {
		slog.Warn("TouchDown failed", "err", err)
		// Close the stream to prevent leak on send failure.
		_, _ = stream.CloseAndRecv()
		return
	}
	h.mu.Lock()
	h.activeHIDStream = stream
	h.mu.Unlock()
}

func (h *hidHandler) handleTouchMove(x, y float64) {
	sw, sh := h.screenWidth, h.screenHeight
	h.mu.Lock()
	stream := h.activeHIDStream
	now := time.Now()
	throttled := now.Sub(h.lastMoveTime) < 16*time.Millisecond
	if !throttled {
		h.lastMoveTime = now
	}
	h.mu.Unlock()
	if stream != nil && !throttled {
		if err := h.client.TouchMove(stream, x*float64(sw), y*float64(sh)); err != nil {
			slog.Warn("TouchMove failed", "err", err)
		}
	}
}

func (h *hidHandler) handleTouchUp(x, y float64) {
	sw, sh := h.screenWidth, h.screenHeight
	h.mu.Lock()
	stream := h.activeHIDStream
	h.activeHIDStream = nil
	h.mu.Unlock()
	if stream != nil {
		if err := h.client.TouchUp(stream, x*float64(sw), y*float64(sh)); err != nil {
			slog.Warn("TouchUp failed", "err", err)
		}
	}
}
