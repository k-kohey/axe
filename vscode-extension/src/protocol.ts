// Protocol types for CLI ↔ Extension communication.
// Types are generated from cmd/internal/preview/proto/preview.proto via ts-proto.
// Wire format is JSON Lines (one JSON object per line).

// Re-export generated types as the public API.
export type {
	AddStream,
	Command,
	Event,
	Frame,
	ForceRebuild,
	Hello,
	Input,
	NextPreview,
	ProtocolError,
	RemoveStream,
	StreamStarted,
	StreamStatus,
	StreamStopped,
	SwitchFile,
	TextEvent,
	TouchEvent,
} from "./generated/preview";

import type {
	Command,
	Event,
	Frame,
	Hello,
	ProtocolError,
	StreamStarted,
	StreamStatus,
	StreamStopped,
} from "./generated/preview";

/** The protocol version supported by this extension. */
export const supportedProtocolVersion = 1;

// --- Type guards ---

export function isFrame(event: Event): event is Event & { frame: Frame } {
	return event.frame !== undefined;
}

export function isStreamStarted(
	event: Event,
): event is Event & { streamStarted: StreamStarted } {
	return event.streamStarted !== undefined;
}

export function isStreamStopped(
	event: Event,
): event is Event & { streamStopped: StreamStopped } {
	return event.streamStopped !== undefined;
}

export function isStreamStatus(
	event: Event,
): event is Event & { streamStatus: StreamStatus } {
	return event.streamStatus !== undefined;
}

export function isProtocolError(
	event: Event,
): event is Event & { protocolError: ProtocolError } {
	return event.protocolError !== undefined;
}

export function isHello(event: Event): event is Event & { hello: Hello } {
	return event.hello !== undefined;
}

// --- Parsing ---

/**
 * Parse a JSON line into an Event. Returns undefined if the line is not valid JSON
 * or does not look like a protocol Event.
 *
 * streamId may be empty for protocol-level events (ProtocolError, Hello).
 */
export function parseEvent(line: string): Event | undefined {
	try {
		const obj = JSON.parse(line);
		if (typeof obj !== "object" || obj === null) {
			return undefined;
		}
		// Accept events with a string streamId OR protocol-level events without one.
		if (
			typeof obj.streamId !== "string" &&
			!("protocolError" in obj) &&
			!("hello" in obj)
		) {
			return undefined;
		}
		// Ensure streamId defaults to empty string for protocol-level events.
		if (typeof obj.streamId !== "string") {
			obj.streamId = "";
		}
		return obj as Event;
	} catch {
		return undefined;
	}
}

/**
 * Serialize a Command to a JSON line (without trailing newline).
 */
export function serializeCommand(cmd: Command): string {
	return JSON.stringify(cmd);
}
