package preview

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/idb"
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
// so callers can safely call Handle on a nil receiver.
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

// Handle dispatches a stdinCommand to the appropriate HID handler.
// Safe to call on a nil receiver.
func (h *hidHandler) Handle(cmd stdinCommand) {
	if h == nil || h.client == nil {
		return
	}
	// text input does not require screen coordinates.
	if cmd.Type == "text" {
		h.handleText(cmd)
		return
	}

	if h.screenWidth <= 0 || h.screenHeight <= 0 {
		// Cannot convert normalised coordinates without valid screen dimensions.
		return
	}

	switch cmd.Type {
	case "tap":
		h.handleTap(cmd)
	case "swipe":
		h.handleSwipe(cmd)
	case "touchDown":
		h.handleTouchDown(cmd)
	case "touchMove":
		h.handleTouchMove(cmd)
	case "touchUp":
		h.handleTouchUp(cmd)
	}
}

func (h *hidHandler) handleTap(cmd stdinCommand) {
	sw, sh := h.screenWidth, h.screenHeight
	go func() {
		if err := h.client.Tap(context.Background(), cmd.X*float64(sw), cmd.Y*float64(sh)); err != nil {
			slog.Warn("Tap failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleSwipe(cmd stdinCommand) {
	sw, sh := h.screenWidth, h.screenHeight
	dur := cmd.Duration
	if dur <= 0 {
		dur = 0.5
	}
	go func() {
		if err := h.client.Swipe(context.Background(),
			cmd.StartX*float64(sw), cmd.StartY*float64(sh),
			cmd.EndX*float64(sw), cmd.EndY*float64(sh),
			dur); err != nil {
			slog.Warn("Swipe failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleText(cmd stdinCommand) {
	if cmd.Value == "" {
		return
	}
	go func() {
		if err := h.client.Text(context.Background(), cmd.Value); err != nil {
			slog.Warn("Text input failed", "err", err)
		}
	}()
}

func (h *hidHandler) handleTouchDown(cmd stdinCommand) {
	sw, sh := h.screenWidth, h.screenHeight
	stream, err := h.client.OpenHIDStream(context.Background())
	if err != nil {
		slog.Warn("OpenHIDStream failed", "err", err)
		return
	}
	if err := h.client.TouchDown(stream, cmd.X*float64(sw), cmd.Y*float64(sh)); err != nil {
		slog.Warn("TouchDown failed", "err", err)
		// Close the stream to prevent leak on send failure.
		_, _ = stream.CloseAndRecv()
		return
	}
	h.mu.Lock()
	h.activeHIDStream = stream
	h.mu.Unlock()
}

func (h *hidHandler) handleTouchMove(cmd stdinCommand) {
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
		if err := h.client.TouchMove(stream, cmd.X*float64(sw), cmd.Y*float64(sh)); err != nil {
			slog.Warn("TouchMove failed", "err", err)
		}
	}
}

func (h *hidHandler) handleTouchUp(cmd stdinCommand) {
	sw, sh := h.screenWidth, h.screenHeight
	h.mu.Lock()
	stream := h.activeHIDStream
	h.activeHIDStream = nil
	h.mu.Unlock()
	if stream != nil {
		if err := h.client.TouchUp(stream, cmd.X*float64(sw), cmd.Y*float64(sh)); err != nil {
			slog.Warn("TouchUp failed", "err", err)
		}
	}
}
