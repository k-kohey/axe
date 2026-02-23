package preview

import (
	"context"
	"io"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
)

// runCommandLoop reads Command JSON Lines from r and dispatches them to sm.
// It returns when the reader is exhausted (EOF) or the context is cancelled.
func runCommandLoop(ctx context.Context, r io.Reader, ew *protocol.EventWriter, sm *StreamManager) {
	protocol.ReadCommands(ctx, r, ew, func(cmd *pb.Command) {
		sm.HandleCommand(ctx, cmd)
	})
}
