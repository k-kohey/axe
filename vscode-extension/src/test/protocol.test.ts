import * as assert from "node:assert";
import {
	type Command,
	type Event,
	isFrame,
	isHello,
	isProtocolError,
	isStreamStarted,
	isStreamStatus,
	isStreamStopped,
	parseEvent,
	serializeCommand,
} from "../protocol";

suite("Protocol", () => {
	suite("Event parsing", () => {
		test("parses Frame event", () => {
			const json =
				'{"streamId":"a","frame":{"device":"iPhone 16 Pro","file":"HogeView.swift","data":"base64data"}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.strictEqual(event.streamId, "a");
			assert.ok(event.frame);
			assert.strictEqual(event.frame.device, "iPhone 16 Pro");
			assert.strictEqual(event.frame.file, "HogeView.swift");
			assert.strictEqual(event.frame.data, "base64data");
		});

		test("parses StreamStarted event", () => {
			const json = '{"streamId":"b","streamStarted":{"previewCount":3}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.strictEqual(event.streamId, "b");
			assert.ok(event.streamStarted);
			assert.strictEqual(event.streamStarted.previewCount, 3);
		});

		test("parses StreamStopped event", () => {
			const json =
				'{"streamId":"c","streamStopped":{"reason":"build_error","message":"Build failed","diagnostic":"error: expected }"}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.strictEqual(event.streamId, "c");
			assert.ok(event.streamStopped);
			assert.strictEqual(event.streamStopped.reason, "build_error");
			assert.strictEqual(event.streamStopped.message, "Build failed");
		});

		test("parses StreamStatus event", () => {
			const json = '{"streamId":"d","streamStatus":{"phase":"building"}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.strictEqual(event.streamId, "d");
			assert.ok(event.streamStatus);
			assert.strictEqual(event.streamStatus.phase, "building");
		});

		test("parses ProtocolError event (no streamId)", () => {
			const json =
				'{"streamId":"","protocolError":{"message":"invalid command: unexpected token"}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.ok(isProtocolError(event));
			assert.strictEqual(
				event.protocolError?.message,
				"invalid command: unexpected token",
			);
		});

		test("parses Hello event (no streamId)", () => {
			const json = '{"streamId":"","hello":{"protocolVersion":1}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.ok(isHello(event));
			assert.strictEqual(event.hello?.protocolVersion, 1);
		});

		test("parses ProtocolError without streamId field", () => {
			// CLI may emit protocolError without streamId entirely.
			const json = '{"protocolError":{"message":"bad command"}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.ok(isProtocolError(event));
			assert.strictEqual(event.streamId, "");
		});

		test("parses Hello without streamId field", () => {
			const json = '{"hello":{"protocolVersion":2}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.ok(isHello(event));
			assert.strictEqual(event.hello?.protocolVersion, 2);
			assert.strictEqual(event.streamId, "");
		});

		test("returns undefined for invalid JSON", () => {
			assert.strictEqual(parseEvent("not json"), undefined);
			assert.strictEqual(parseEvent(""), undefined);
			assert.strictEqual(parseEvent("{truncated"), undefined);
		});

		test("returns undefined for non-object JSON", () => {
			assert.strictEqual(parseEvent("[1,2,3]"), undefined);
			assert.strictEqual(parseEvent('"string"'), undefined);
			assert.strictEqual(parseEvent("42"), undefined);
			assert.strictEqual(parseEvent("null"), undefined);
		});

		test("returns undefined when streamId is missing and not protocol-level", () => {
			assert.strictEqual(parseEvent('{"frame":{"data":"abc"}}'), undefined);
		});

		test("returns undefined when streamId is not a string", () => {
			assert.strictEqual(
				parseEvent('{"streamId":123,"frame":{"data":"abc"}}'),
				undefined,
			);
		});

		test("tolerates unknown fields", () => {
			const json =
				'{"streamId":"a","frame":{"device":"iPhone","file":"V.swift","data":"abc","extra":"ignored"},"futureField":{}}';
			const event = parseEvent(json);
			assert.ok(event);
			assert.strictEqual(event.streamId, "a");
			assert.ok(event.frame);
			assert.strictEqual(event.frame.data, "abc");
		});
	});

	suite("Command serialization", () => {
		test("serializes AddStream command", () => {
			const cmd: Command = {
				streamId: "s1",
				addStream: {
					file: "/path/to/View.swift",
					deviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
					runtime: "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
				},
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.streamId, "s1");
			assert.strictEqual(parsed.addStream.file, "/path/to/View.swift");
			assert.strictEqual(
				parsed.addStream.deviceType,
				"com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
			);
			assert.strictEqual(
				parsed.addStream.runtime,
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2",
			);
		});

		test("serializes RemoveStream command", () => {
			const cmd: Command = { streamId: "s1", removeStream: {} };
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.streamId, "s1");
			assert.deepStrictEqual(parsed.removeStream, {});
		});

		test("serializes SwitchFile command", () => {
			const cmd: Command = {
				streamId: "s1",
				switchFile: { file: "/path/to/Other.swift" },
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.switchFile.file, "/path/to/Other.swift");
		});

		test("serializes NextPreview command", () => {
			const cmd: Command = { streamId: "s1", nextPreview: {} };
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.streamId, "s1");
			assert.deepStrictEqual(parsed.nextPreview, {});
		});

		test("serializes ForceRebuild command", () => {
			const cmd: Command = { streamId: "s1", forceRebuild: {} };
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.streamId, "s1");
			assert.deepStrictEqual(parsed.forceRebuild, {});
		});

		test("serializes Input command with TouchDown", () => {
			const cmd: Command = {
				streamId: "s1",
				input: { touchDown: { x: 0.5, y: 0.3 } },
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.input.touchDown.x, 0.5);
			assert.strictEqual(parsed.input.touchDown.y, 0.3);
		});

		test("serializes Input command with Text", () => {
			const cmd: Command = {
				streamId: "s1",
				input: { text: { value: "a" } },
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.input.text.value, "a");
		});

		test("omits undefined payload fields", () => {
			const cmd: Command = {
				streamId: "s1",
				addStream: { file: "/p", deviceType: "dt", runtime: "rt" },
			};
			const json = serializeCommand(cmd);
			assert.ok(!json.includes("removeStream"));
			assert.ok(!json.includes("switchFile"));
			assert.ok(!json.includes("nextPreview"));
			assert.ok(!json.includes("forceRebuild"));
			assert.ok(!json.includes("input"));
		});

		test("round-trip: serialize then parse as Event-like JSON", () => {
			// This simulates the CLI receiving a serialized Command
			const cmd: Command = {
				streamId: "s1",
				addStream: { file: "/p", deviceType: "dt", runtime: "rt" },
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);
			assert.strictEqual(parsed.streamId, "s1");
			assert.strictEqual(parsed.addStream.file, "/p");
		});
	});

	suite("Type guards", () => {
		test("isFrame returns true for Frame events", () => {
			const event: Event = {
				streamId: "a",
				frame: { device: "iPhone", file: "V.swift", data: "abc" },
			};
			assert.strictEqual(isFrame(event), true);
			assert.strictEqual(isStreamStarted(event), false);
			assert.strictEqual(isStreamStopped(event), false);
			assert.strictEqual(isStreamStatus(event), false);
			assert.strictEqual(isProtocolError(event), false);
			assert.strictEqual(isHello(event), false);
		});

		test("isStreamStarted returns true for StreamStarted events", () => {
			const event: Event = {
				streamId: "a",
				streamStarted: { previewCount: 2 },
			};
			assert.strictEqual(isFrame(event), false);
			assert.strictEqual(isStreamStarted(event), true);
		});

		test("isStreamStopped returns true for StreamStopped events", () => {
			const event: Event = {
				streamId: "a",
				streamStopped: { reason: "removed", message: "", diagnostic: "" },
			};
			assert.strictEqual(isStreamStopped(event), true);
			assert.strictEqual(isFrame(event), false);
		});

		test("isStreamStatus returns true for StreamStatus events", () => {
			const event: Event = {
				streamId: "a",
				streamStatus: { phase: "building" },
			};
			assert.strictEqual(isStreamStatus(event), true);
			assert.strictEqual(isFrame(event), false);
		});

		test("isProtocolError returns true for ProtocolError events", () => {
			const event: Event = {
				streamId: "",
				protocolError: { message: "bad input" },
			};
			assert.strictEqual(isProtocolError(event), true);
			assert.strictEqual(isFrame(event), false);
			assert.strictEqual(isHello(event), false);
		});

		test("isHello returns true for Hello events", () => {
			const event: Event = {
				streamId: "",
				hello: { protocolVersion: 1 },
			};
			assert.strictEqual(isHello(event), true);
			assert.strictEqual(isFrame(event), false);
			assert.strictEqual(isProtocolError(event), false);
		});

		test("all guards return false for empty event", () => {
			const event: Event = { streamId: "a" };
			assert.strictEqual(isFrame(event), false);
			assert.strictEqual(isStreamStarted(event), false);
			assert.strictEqual(isStreamStopped(event), false);
			assert.strictEqual(isStreamStatus(event), false);
			assert.strictEqual(isProtocolError(event), false);
			assert.strictEqual(isHello(event), false);
		});
	});

	suite("Cross-language compatibility", () => {
		test("Go-produced Frame JSON parses correctly in TS", () => {
			// Simulate JSON output from Go's json.Marshal of Event{StreamID:"a",Frame:&Frame{...}}
			const goJSON =
				'{"streamId":"a","frame":{"device":"iPhone 16 Pro","file":"HogeView.swift","data":"AAAA"}}';
			const event = parseEvent(goJSON);
			assert.ok(event);
			assert.ok(isFrame(event));
			assert.strictEqual(event.frame?.device, "iPhone 16 Pro");
			assert.strictEqual(event.frame?.data, "AAAA");
		});

		test("TS-produced AddStream JSON matches Go expected format", () => {
			const cmd: Command = {
				streamId: "stream-1",
				addStream: {
					file: "/path/to/HogeView.swift",
					deviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
					runtime: "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
				},
			};
			const json = serializeCommand(cmd);
			const parsed = JSON.parse(json);

			// Verify field names match Go json tags (camelCase)
			assert.ok("streamId" in parsed);
			assert.ok("addStream" in parsed);
			assert.ok("file" in parsed.addStream);
			assert.ok("deviceType" in parsed.addStream);
			assert.ok("runtime" in parsed.addStream);
		});

		test("Go-produced Hello JSON parses correctly in TS", () => {
			const goJSON = '{"streamId":"","hello":{"protocolVersion":1}}';
			const event = parseEvent(goJSON);
			assert.ok(event);
			assert.ok(isHello(event));
			assert.strictEqual(event.hello?.protocolVersion, 1);
		});

		test("Go-produced ProtocolError JSON parses correctly in TS", () => {
			const goJSON =
				'{"streamId":"","protocolError":{"message":"invalid command: proto: syntax error"}}';
			const event = parseEvent(goJSON);
			assert.ok(event);
			assert.ok(isProtocolError(event));
			assert.strictEqual(
				event.protocolError?.message,
				"invalid command: proto: syntax error",
			);
		});
	});
});
