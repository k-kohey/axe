package protocol

import (
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/k-kohey/axe/internal/preview/previewproto"
)

// ProtocolVersion is the current version of the CLI↔Extension protocol.
// Bump this when making breaking changes to the wire format.
const ProtocolVersion = 1

var (
	jsonMarshalOpts   = protojson.MarshalOptions{EmitDefaultValues: true}
	jsonUnmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// MarshalEvent serializes a protobuf Event to JSON using protojson.
func MarshalEvent(e *pb.Event) ([]byte, error) {
	return jsonMarshalOpts.Marshal(e)
}

// UnmarshalCommand deserializes a JSON command into a protobuf Command.
func UnmarshalCommand(data []byte) (*pb.Command, error) {
	cmd := &pb.Command{}
	if err := jsonUnmarshalOpts.Unmarshal(data, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

// UnmarshalEvent deserializes a JSON event into a protobuf Event.
func UnmarshalEvent(data []byte) (*pb.Event, error) {
	e := &pb.Event{}
	if err := jsonUnmarshalOpts.Unmarshal(data, e); err != nil {
		return nil, err
	}
	return e, nil
}
