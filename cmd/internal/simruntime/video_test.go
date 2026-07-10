package simruntime

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestDetectFrameDimensions(t *testing.T) {
	width, height := detectFrameDimensions(200*400*4, 390, 844)
	if width != 200 || height != 400 {
		t.Fatalf("dimensions = %dx%d", width, height)
	}
}

func TestValidateFrameSize(t *testing.T) {
	if err := validateFrameSize(make([]byte, 2*3*4), 2, 3); err != nil {
		t.Fatalf("expected valid frame: %v", err)
	}
	if err := validateFrameSize(make([]byte, 10), 2, 3); err == nil {
		t.Fatal("expected size mismatch")
	}
}

func TestEncodeBGRAFrame(t *testing.T) {
	data := []byte{
		0, 0, 255, 255,
		0, 255, 0, 255,
	}
	jpegData, err := encodeBGRAFrame(data, 2, 1, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jpegData) == 0 {
		t.Fatal("expected jpeg bytes")
	}
	if _, err := jpeg.Decode(bytes.NewReader(jpegData)); err != nil {
		t.Fatalf("invalid jpeg: %v", err)
	}
}
