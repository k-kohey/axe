package simruntime

import (
	"context"
	"testing"

	"github.com/k-kohey/axe/internal/idb"
)

type fakeIDBClient struct {
	tapX, tapY       float64
	swipe            Swipe
	text             string
	openedHIDStreams int
	touchDownX       float64
	touchDownY       float64
	touchMoveX       float64
	touchMoveY       float64
	touchUpX         float64
	touchUpY         float64
}

func (c *fakeIDBClient) ScreenSize(context.Context) (int, int, error) { return 390, 844, nil }
func (c *fakeIDBClient) VideoStream(context.Context, int) (<-chan []byte, error) {
	return nil, nil
}
func (c *fakeIDBClient) Tap(_ context.Context, x, y float64) error {
	c.tapX, c.tapY = x, y
	return nil
}
func (c *fakeIDBClient) Swipe(_ context.Context, startX, startY, endX, endY, durationSec float64) error {
	c.swipe = Swipe{StartX: startX, StartY: startY, EndX: endX, EndY: endY, DurationSeconds: durationSec}
	return nil
}
func (c *fakeIDBClient) Text(_ context.Context, text string) error {
	c.text = text
	return nil
}
func (c *fakeIDBClient) Screenshot(context.Context) ([]byte, error) { return nil, nil }
func (c *fakeIDBClient) OpenHIDStream(context.Context) (idb.HIDStream, error) {
	c.openedHIDStreams++
	return &fakeHIDStream{}, nil
}
func (c *fakeIDBClient) TouchDown(_ idb.HIDStream, x, y float64) error {
	c.touchDownX, c.touchDownY = x, y
	return nil
}
func (c *fakeIDBClient) TouchMove(_ idb.HIDStream, x, y float64) error {
	c.touchMoveX, c.touchMoveY = x, y
	return nil
}
func (c *fakeIDBClient) TouchUp(_ idb.HIDStream, x, y float64) error {
	c.touchUpX, c.touchUpY = x, y
	return nil
}
func (c *fakeIDBClient) Close() error { return nil }

type fakeHIDStream struct {
	idb.HIDStream
}

func TestSessionSendInputScalesTap(t *testing.T) {
	client := &fakeIDBClient{}
	session := &Session{client: client, screenWidth: 390, screenHeight: 844}

	if err := session.sendInput(context.Background(), InputEvent{Tap: &Point{X: 0.5, Y: 0.25}}); err != nil {
		t.Fatal(err)
	}
	if client.tapX != 195 || client.tapY != 211 {
		t.Fatalf("tap = %f,%f", client.tapX, client.tapY)
	}
}

func TestSessionSendInputText(t *testing.T) {
	client := &fakeIDBClient{}
	session := &Session{client: client, screenWidth: 390, screenHeight: 844}

	if err := session.sendInput(context.Background(), InputEvent{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if client.text != "hello" {
		t.Fatalf("text = %q", client.text)
	}
}

func TestSessionSendInputScalesSwipe(t *testing.T) {
	client := &fakeIDBClient{}
	session := &Session{client: client, screenWidth: 390, screenHeight: 844}

	err := session.sendInput(context.Background(), InputEvent{Swipe: &Swipe{
		StartX: 0.1, StartY: 0.2, EndX: 0.3, EndY: 0.4, DurationSeconds: 0.5,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if client.swipe.StartX != 39 || client.swipe.StartY != 168.8 || client.swipe.EndX != 117 || client.swipe.EndY != 337.6 {
		t.Fatalf("swipe = %+v", client.swipe)
	}
	if client.swipe.DurationSeconds != 0.5 {
		t.Fatalf("duration = %f", client.swipe.DurationSeconds)
	}
}

func TestSessionTouchSequence(t *testing.T) {
	client := &fakeIDBClient{}
	session := &Session{client: client, screenWidth: 390, screenHeight: 844}

	if err := session.sendInput(context.Background(), InputEvent{TouchDown: &Point{X: 0.5, Y: 0.5}}); err != nil {
		t.Fatal(err)
	}
	session.lastMoveTime = session.lastMoveTime.Add(-1000000000)
	if err := session.sendInput(context.Background(), InputEvent{TouchMove: &Point{X: 0.6, Y: 0.6}}); err != nil {
		t.Fatal(err)
	}
	if err := session.sendInput(context.Background(), InputEvent{TouchUp: &Point{X: 0.7, Y: 0.7}}); err != nil {
		t.Fatal(err)
	}

	if client.openedHIDStreams != 1 {
		t.Fatalf("opened streams = %d", client.openedHIDStreams)
	}
	if client.touchDownX != 195 || client.touchDownY != 422 {
		t.Fatalf("touch down = %f,%f", client.touchDownX, client.touchDownY)
	}
	if client.touchMoveX != 234 || client.touchMoveY != 506.4 {
		t.Fatalf("touch move = %f,%f", client.touchMoveX, client.touchMoveY)
	}
	if client.touchUpX != 273 || client.touchUpY != 590.8 {
		t.Fatalf("touch up = %f,%f", client.touchUpX, client.touchUpY)
	}
}
