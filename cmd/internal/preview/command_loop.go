package preview

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// readCommands reads Command JSON Lines from r and calls handle for each.
// Empty lines are skipped; invalid JSON lines are logged and skipped.
// Returns when the reader is exhausted (EOF) or context is cancelled.
func readCommands(ctx context.Context, r io.Reader, handle func(*pb.Command)) {
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
			continue
		}
		handle(cmd)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("stdin scanner error", "err", err)
	}
}

// runCommandLoop reads Command JSON Lines from r and dispatches them to sm.
// It returns when the reader is exhausted (EOF) or the context is cancelled.
func runCommandLoop(ctx context.Context, r io.Reader, sm *StreamManager) {
	readCommands(ctx, r, func(cmd *pb.Command) {
		sm.HandleCommand(ctx, cmd)
	})
}
