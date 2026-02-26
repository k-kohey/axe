package preview

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"

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

// stdinCommand represents a command received from stdin (JSON Lines protocol).
type stdinCommand struct {
	Type     string  `json:"type"`
	Path     string  `json:"path,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	StartX   float64 `json:"startX,omitempty"`
	StartY   float64 `json:"startY,omitempty"`
	EndX     float64 `json:"endX,omitempty"`
	EndY     float64 `json:"endY,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Value    string  `json:"value,omitempty"`
}

// readStdinCommands reads JSON Lines from stdin and sends commands on ch.
// In serve mode, JSON objects are parsed; non-JSON lines are treated as legacy
// file path commands for backwards compatibility.
// In non-serve mode, any input triggers a preview cycle.
func readStdinCommands(ch chan<- stdinCommand, serve bool) {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		var cmd stdinCommand

		if serve && line != "" {
			// Try JSON parse first.
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				// Legacy: treat non-JSON as file path.
				cmd = stdinCommand{Type: "switchFile", Path: line}
			}
		} else {
			// Empty line or any input in non-serve mode → preview cycle.
			cmd = stdinCommand{Type: "nextPreview"}
		}

		select {
		case ch <- cmd:
		default: // don't block if previous command hasn't been processed
		}
	}
}

// readProtocolCommands reads JSON Lines from stdin and parses them as Command structs.
// Invalid JSON lines are logged and skipped. EOF causes the channel to close.
func readProtocolCommands(ctx context.Context, ew *protocol.EventWriter, ch chan<- *pb.Command) {
	protocol.ReadCommands(ctx, os.Stdin, ew, func(cmd *pb.Command) {
		select {
		case ch <- cmd:
		default:
		}
	})
	close(ch)
}

// dispatchStdinCommands reads stdinCommands and dispatches them to typed channels.
// Commands that require multi-step HID operations (tap, swipe) are handled
// directly via the HIDHandler since they cannot be represented as pb.Input.
func dispatchStdinCommands(ctx context.Context, cmdCh <-chan stdinCommand, hid *protocol.HIDHandler,
	switchFileCh chan<- string, nextPreviewCh chan<- struct{}, forceRebuildCh chan<- struct{}, inputCh chan<- *pb.Input) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			switch cmd.Type {
			case "switchFile":
				select {
				case switchFileCh <- cmd.Path:
				default:
				}
			case "nextPreview":
				select {
				case nextPreviewCh <- struct{}{}:
				default:
				}
			case "forceRebuild":
				select {
				case forceRebuildCh <- struct{}{}:
				default:
				}
			case "tap":
				hid.HandleTap(ctx, cmd.X, cmd.Y)
			case "swipe":
				hid.HandleSwipe(ctx, cmd.StartX, cmd.StartY, cmd.EndX, cmd.EndY, cmd.Duration)
			case "text":
				select {
				case inputCh <- &pb.Input{Event: &pb.Input_Text{Text: &pb.TextEvent{Value: cmd.Value}}}:
				default:
				}
			case "touchDown":
				select {
				case inputCh <- &pb.Input{Event: &pb.Input_TouchDown{TouchDown: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}}:
				default:
				}
			case "touchMove":
				select {
				case inputCh <- &pb.Input{Event: &pb.Input_TouchMove{TouchMove: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}}:
				default:
				}
			case "touchUp":
				select {
				case inputCh <- &pb.Input{Event: &pb.Input_TouchUp{TouchUp: &pb.TouchEvent{X: cmd.X, Y: cmd.Y}}}:
				default:
				}
			}
		}
	}
}

// dispatchProtocolCommands reads protocol Commands and dispatches them to typed channels.
func dispatchProtocolCommands(ctx context.Context, protoCmdCh <-chan *pb.Command, hid *protocol.HIDHandler,
	switchFileCh chan<- string, nextPreviewCh chan<- struct{}, forceRebuildCh chan<- struct{}, inputCh chan<- *pb.Input) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-protoCmdCh:
			if !ok {
				return
			}
			switch {
			case cmd.GetSwitchFile() != nil:
				select {
				case switchFileCh <- cmd.GetSwitchFile().GetFile():
				default:
				}
			case cmd.GetNextPreview() != nil:
				select {
				case nextPreviewCh <- struct{}{}:
				default:
				}
			case cmd.GetForceRebuild() != nil:
				select {
				case forceRebuildCh <- struct{}{}:
				default:
				}
			case cmd.GetInput() != nil:
				select {
				case inputCh <- cmd.GetInput():
				default:
				}
			default:
				slog.Warn("Ignoring unhandled command in single-stream mode", "streamId", cmd.GetStreamId())
			}
		}
	}
}
