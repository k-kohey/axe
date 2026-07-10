package simruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/idb"
)

type Session struct {
	mu           sync.Mutex
	id           string
	deviceUDID   string
	deviceType   string
	runtime      string
	state        SessionState
	bundleID     string
	screenWidth  int
	screenHeight int
	cancel       context.CancelFunc

	bootCompanion *idb.Companion
	idbCompanion  *idb.Companion
	client        idb.IDBClient

	subscribers  map[chan Event]struct{}
	eventsClosed bool

	activeHIDStream idb.HIDStream
	lastMoveTime    time.Time
}

func newSession(id, deviceType, runtime string, cancel context.CancelFunc) *Session {
	return &Session{
		id:          id,
		deviceType:  deviceType,
		runtime:     runtime,
		state:       SessionStarting,
		cancel:      cancel,
		subscribers: make(map[chan Event]struct{}),
	}
}

func (s *Session) info() *SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &SessionInfo{
		ID:           s.id,
		DeviceUDID:   s.deviceUDID,
		DeviceType:   s.deviceType,
		Runtime:      s.runtime,
		State:        s.state,
		BundleID:     s.bundleID,
		ScreenWidth:  s.screenWidth,
		ScreenHeight: s.screenHeight,
	}
}

func (s *Session) setState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *Session) publish(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventsClosed {
		return
	}
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Session) subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 16)
	s.mu.Lock()
	if s.eventsClosed {
		close(ch)
		s.mu.Unlock()
		return ch
	}
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()
	return ch
}

func (s *Session) closeEvents() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventsClosed {
		return
	}
	s.eventsClosed = true
	for ch := range s.subscribers {
		close(ch)
	}
	s.subscribers = nil
}

func (s *Session) sendInput(ctx context.Context, input InputEvent) error {
	if s.client == nil {
		return fmt.Errorf("session is not ready for input")
	}
	switch {
	case input.Text != "":
		return s.client.Text(ctx, input.Text)
	case input.Tap != nil:
		return s.client.Tap(ctx, input.Tap.X*float64(s.screenWidth), input.Tap.Y*float64(s.screenHeight))
	case input.Swipe != nil:
		return s.client.Swipe(ctx,
			input.Swipe.StartX*float64(s.screenWidth),
			input.Swipe.StartY*float64(s.screenHeight),
			input.Swipe.EndX*float64(s.screenWidth),
			input.Swipe.EndY*float64(s.screenHeight),
			input.Swipe.DurationSeconds,
		)
	case input.TouchDown != nil:
		return s.touchDown(ctx, input.TouchDown)
	case input.TouchMove != nil:
		return s.touchMove(input.TouchMove)
	case input.TouchUp != nil:
		return s.touchUp(input.TouchUp)
	default:
		return nil
	}
}

func (s *Session) touchDown(ctx context.Context, p *Point) error {
	s.mu.Lock()
	old := s.activeHIDStream
	s.activeHIDStream = nil
	s.mu.Unlock()
	if old != nil {
		_, _ = old.CloseAndRecv()
	}
	stream, err := s.client.OpenHIDStream(ctx)
	if err != nil {
		return err
	}
	if err := s.client.TouchDown(stream, p.X*float64(s.screenWidth), p.Y*float64(s.screenHeight)); err != nil {
		_, _ = stream.CloseAndRecv()
		return err
	}
	s.mu.Lock()
	s.activeHIDStream = stream
	s.mu.Unlock()
	return nil
}

func (s *Session) touchMove(p *Point) error {
	s.mu.Lock()
	stream := s.activeHIDStream
	now := time.Now()
	throttled := now.Sub(s.lastMoveTime) < 16*time.Millisecond
	if !throttled {
		s.lastMoveTime = now
	}
	s.mu.Unlock()
	if stream == nil || throttled {
		return nil
	}
	return s.client.TouchMove(stream, p.X*float64(s.screenWidth), p.Y*float64(s.screenHeight))
}

func (s *Session) touchUp(p *Point) error {
	s.mu.Lock()
	stream := s.activeHIDStream
	s.activeHIDStream = nil
	s.mu.Unlock()
	if stream == nil {
		return nil
	}
	return s.client.TouchUp(stream, p.X*float64(s.screenWidth), p.Y*float64(s.screenHeight))
}
