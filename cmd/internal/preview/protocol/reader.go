package protocol

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// ReadCommands reads Command JSON Lines from r and calls handle for each.
// Empty lines are skipped; invalid JSON lines are logged and notified via ew.
// Returns when the reader is exhausted (EOF) or context is cancelled.
func ReadCommands(ctx context.Context, r io.Reader, ew *EventWriter, handle func(*pb.Command)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		cmd, err := UnmarshalCommand([]byte(line))
		if err != nil {
			slog.Warn("Invalid command JSON, skipping", "err", err, "line", line)
			if ew != nil {
				_ = ew.Send(&pb.Event{
					Payload: &pb.Event_ProtocolError{
						ProtocolError: &pb.ProtocolError{Message: fmt.Sprintf("invalid command: %v", err)},
					},
				})
			}
			continue
		}
		handle(cmd)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("stdin scanner error", "err", err)
	}
}
