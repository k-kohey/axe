package simruntime

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"
)

func encodeBGRAFrame(data []byte, frameW, frameH int, buf *bytes.Buffer) ([]byte, error) {
	for i := 0; i+2 < len(data); i += 4 {
		data[i], data[i+2] = data[i+2], data[i]
	}
	img := &image.NRGBA{
		Pix:    data,
		Stride: frameW * 4,
		Rect:   image.Rect(0, 0, frameW, frameH),
	}
	buf.Reset()
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

func detectFrameDimensions(dataSize, screenW, screenH int) (int, int) {
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
		if totalPixels%w != 0 {
			continue
		}
		h := totalPixels / w
		if math.Abs(float64(w)/float64(h)-aspect) < 0.05 {
			return w, h
		}
	}
	return 0, 0
}

func validateFrameSize(data []byte, width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid frame dimensions %dx%d", width, height)
	}
	if len(data) != width*height*4 {
		return fmt.Errorf("frame size mismatch: got %d, want %d", len(data), width*height*4)
	}
	return nil
}
