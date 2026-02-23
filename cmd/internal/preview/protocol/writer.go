package protocol

import (
	"io"
	"sync"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// EventWriter writes JSON Lines (one Event per line) to an io.Writer.
// It is safe for concurrent use by multiple goroutines.
type EventWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewEventWriter creates an EventWriter that writes to w.
func NewEventWriter(w io.Writer) *EventWriter {
	return &EventWriter{w: w}
}

// Send marshals the event as JSON and writes it as a single line followed by a newline.
// The write is atomic: the mutex ensures no interleaving with concurrent Send calls.
func (ew *EventWriter) Send(event *pb.Event) error {
	data, err := MarshalEvent(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	ew.mu.Lock()
	defer ew.mu.Unlock()

	_, err = ew.w.Write(data)
	return err
}
