import * as assert from "node:assert";
import { EventEmitter } from "node:events";
import { Readable, Writable } from "node:stream";
import type { AxeConfig } from "../config";
import {
	type AxeProcess,
	type OutputChannel,
	PreviewManager,
	type SpawnFn,
	type StatusBarLike,
} from "../previewManager";

// --- Helpers ---

interface FakeProcess extends AxeProcess {
	_kill: (signal?: string) => void;
	emit(event: string, ...args: unknown[]): boolean;
}

function createFakeProcess(): FakeProcess {
	const emitter = new EventEmitter();
	const proc: FakeProcess = Object.assign(emitter, {
		killed: false,
		stdin: new Writable({
			write(_c, _e, cb) {
				cb();
			},
		}) as NodeJS.WritableStream,
		stdout: new Readable({ read() {} }) as NodeJS.ReadableStream,
		stderr: new Readable({ read() {} }) as NodeJS.ReadableStream,
		_kill(_signal?: string) {
			process.nextTick(() => proc.emit("exit", null, _signal));
		},
		kill(signal?: string): boolean {
			proc.killed = true;
			proc._kill(signal ?? "SIGTERM");
			return true;
		},
	});
	return proc;
}

function createFakeOutputChannel(): OutputChannel & { lines: string[] } {
	const lines: string[] = [];
	return {
		lines,
		appendLine(text: string) {
			lines.push(text);
		},
		append(text: string) {
			lines.push(text);
		},
		show(_preserveFocus?: boolean) {},
	};
}

function createFakeStatusBar(): StatusBarLike & { calls: string[] } {
	const calls: string[] = [];
	return {
		calls,
		showRunning(fileName: string) {
			calls.push(`running:${fileName}`);
		},
		showError() {
			calls.push("error");
		},
		hide() {
			calls.push("hide");
		},
	};
}

const DEFAULT_CONFIG: AxeConfig = {
	executablePath: "axe",
	project: "",
	workspace: "",
	scheme: "",
	configuration: "",
	additionalArgs: [],
};

suite("PreviewManager", () => {
	test("addStream spawns process and sends AddStream command", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		assert.strictEqual(manager.isRunning, true);
		assert.strictEqual(manager.streamCount, 1);
		assert.ok(statusBar.calls.includes("running:HogeView.swift"));
		assert.ok(output.lines.some((l) => l.includes("axe")));

		const parsed = JSON.parse(chunks[0].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.strictEqual(parsed.addStream.file, "/path/to/HogeView.swift");
		assert.strictEqual(parsed.addStream.deviceType, "iPhone16,1");
		assert.strictEqual(parsed.addStream.runtime, "iOS-18-0");
	});

	test("addStream with same streamId is idempotent", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		assert.strictEqual(chunks.length, 1); // only one AddStream command
		assert.strictEqual(manager.streamCount, 1);
	});

	test("addStream with different streamId reuses process", async () => {
		let spawnCount = 0;
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => {
			spawnCount++;
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.addStream(
			"stream-b",
			"/path/to/FugaView.swift",
			"iPhone15,4",
			"iOS-17-0",
		);

		assert.strictEqual(spawnCount, 1); // single process
		assert.strictEqual(chunks.length, 2); // two AddStream commands
		assert.strictEqual(manager.streamCount, 2);
	});

	test("removeStream sends RemoveStream command", async () => {
		const fakeProc = createFakeProcess();
		fakeProc._kill = () => {}; // don't auto-exit on kill
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.addStream(
			"stream-b",
			"/path/to/FugaView.swift",
			"iPhone15,4",
			"iOS-17-0",
		);

		await manager.removeStream("stream-a");

		assert.strictEqual(manager.streamCount, 1);
		// Last chunk should be RemoveStream
		const parsed = JSON.parse(chunks[chunks.length - 1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.deepStrictEqual(parsed.removeStream, {});
		// Process should still be running (stream-b is active)
		assert.strictEqual(manager.isRunning, true);
	});

	test("removeStream stops process when last stream removed", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		let stopped = false;
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			onPreviewStop: () => {
				stopped = true;
			},
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.removeStream("stream-a");

		assert.strictEqual(manager.streamCount, 0);
		assert.strictEqual(stopped, true);
		assert.ok(statusBar.calls.includes("hide"));
	});

	test("removeStream is no-op for unknown streamId", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.removeStream("nonexistent");

		assert.strictEqual(manager.streamCount, 1);
		// Only the AddStream command was sent, no RemoveStream
		assert.strictEqual(chunks.length, 1);
	});

	test("addStream after process exit spawns new process", async () => {
		const procs: FakeProcess[] = [];
		const spawnFn: SpawnFn = () => {
			const p = createFakeProcess();
			p._kill = () => {};
			procs.push(p);
			return p;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		assert.strictEqual(procs.length, 1);

		// Simulate process exit
		procs[0].emit("exit", 0, null);
		await new Promise((r) => setTimeout(r, 10));

		assert.strictEqual(manager.isRunning, false);
		assert.strictEqual(manager.streamCount, 0);

		await manager.addStream(
			"stream-b",
			"/path/to/FugaView.swift",
			"iPhone15,4",
			"iOS-17-0",
		);
		assert.strictEqual(procs.length, 2);
		assert.strictEqual(manager.isRunning, true);
	});

	test("process exit clears all streams and fires onPreviewStop", async () => {
		const fakeProc = createFakeProcess();
		fakeProc._kill = () => {};
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		let stopCount = 0;
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			onPreviewStop: () => {
				stopCount++;
			},
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.addStream(
			"stream-b",
			"/path/to/FugaView.swift",
			"iPhone15,4",
			"iOS-17-0",
		);
		assert.strictEqual(manager.streamCount, 2);

		fakeProc.emit("exit", 1, null);
		await new Promise((r) => setTimeout(r, 10));

		assert.strictEqual(manager.isRunning, false);
		assert.strictEqual(manager.streamCount, 0);
		assert.strictEqual(stopCount, 1);
		assert.ok(statusBar.calls.includes("error"));
		assert.ok(statusBar.calls.includes("hide"));
	});

	test("stopPreview sends SIGTERM and clears streams", async () => {
		let killSignal: string | undefined;
		const fakeProc = createFakeProcess();
		fakeProc._kill = (signal) => {
			killSignal = signal;
			process.nextTick(() => fakeProc.emit("exit", null, signal));
		};
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.stopPreview();

		assert.strictEqual(killSignal, "SIGTERM");
		assert.strictEqual(manager.isRunning, false);
		assert.strictEqual(manager.streamCount, 0);
		assert.ok(statusBar.calls.includes("hide"));
	});

	test("stopPreview when no process is a no-op", async () => {
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: () => createFakeProcess(),
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.stopPreview();
		assert.strictEqual(manager.isRunning, false);
		assert.strictEqual(manager.streamCount, 0);
	});

	test("nextPreview writes Command JSON to stdin with streamId", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.nextPreview("stream-a");

		// chunks[0] is AddStream, chunks[1] is nextPreview
		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.deepStrictEqual(parsed.nextPreview, {});
	});

	test("forceRebuild writes Command JSON to stdin with streamId", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.forceRebuild("stream-a");

		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.deepStrictEqual(parsed.forceRebuild, {});
	});

	test("sendInput writes Command JSON with streamId from message", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({
			type: "touchDown",
			streamId: "stream-a",
			x: 0.5,
			y: 0.3,
		});

		// chunks[0] is AddStream, chunks[1] is input
		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.ok(parsed.input);
		assert.strictEqual(parsed.input.touchDown.x, 0.5);
		assert.strictEqual(parsed.input.touchDown.y, 0.3);
	});

	test("sendInput is no-op when no process", () => {
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: () => createFakeProcess(),
			getConfig: () => DEFAULT_CONFIG,
		});

		manager.sendInput({
			type: "touchDown",
			streamId: "stream-a",
			x: 0.5,
			y: 0.3,
		});
		// Should not throw
		assert.strictEqual(manager.isRunning, false);
	});

	test("sendInput is no-op when streamId is missing", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({ type: "touchDown", streamId: "", x: 0.5, y: 0.3 });

		// Only the AddStream command was sent
		assert.strictEqual(chunks.length, 1);
	});

	test("status bar shows error on non-zero exit", async () => {
		const fakeProc = createFakeProcess();
		fakeProc._kill = () => {};
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		fakeProc.emit("exit", 1, null);
		await new Promise((r) => setTimeout(r, 10));

		assert.ok(statusBar.calls.includes("error"));
		assert.ok(output.lines.some((l) => l.includes("exited with code 1")));
	});

	test("dispose sends SIGTERM to running process", async () => {
		let killSignal: string | undefined;
		const fakeProc = createFakeProcess();
		fakeProc._kill = (signal) => {
			killSignal = signal;
		};
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.dispose();

		assert.strictEqual(killSignal, "SIGTERM");
	});

	test("addStream uses resolveExecutablePath when provided", async () => {
		let spawnedCommand = "";
		const fakeProc = createFakeProcess();
		const spawnFn: SpawnFn = (cmd) => {
			spawnedCommand = cmd;
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			resolveExecutablePath: async () => "/resolved/bin/axe",
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		assert.strictEqual(spawnedCommand, "/resolved/bin/axe");
		assert.ok(output.lines.some((l) => l.includes("/resolved/bin/axe")));
	});

	test("addStream falls back to config when resolveExecutablePath is not set", async () => {
		let spawnedCommand = "";
		const fakeProc = createFakeProcess();
		const spawnFn: SpawnFn = (cmd) => {
			spawnedCommand = cmd;
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const config: AxeConfig = {
			...DEFAULT_CONFIG,
			executablePath: "/config/axe",
		};
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => config,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		assert.strictEqual(spawnedCommand, "/config/axe");
	});

	test("addStream propagates resolveExecutablePath error", async () => {
		const fakeProc = createFakeProcess();
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			resolveExecutablePath: async () => {
				throw new Error("axe binary not available");
			},
		});

		await assert.rejects(
			() =>
				manager.addStream(
					"stream-a",
					"/path/to/HogeView.swift",
					"iPhone16,1",
					"iOS-18-0",
				),
			{ message: "axe binary not available" },
		);
		assert.strictEqual(manager.isRunning, false);
	});

	test("onStdoutLine callback receives lines from stdout", async () => {
		const fakeProc = createFakeProcess();
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const lines: string[] = [];
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			onStdoutLine: (line) => {
				lines.push(line);
			},
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		(fakeProc.stdout as Readable).push(Buffer.from("line1\nline2\n"));
		await new Promise((r) => setTimeout(r, 10));

		assert.deepStrictEqual(lines, ["line1", "line2"]);
	});

	test("onStdoutLine buffers partial lines", async () => {
		const fakeProc = createFakeProcess();
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const lines: string[] = [];
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			onStdoutLine: (line) => {
				lines.push(line);
			},
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		(fakeProc.stdout as Readable).push(Buffer.from("part"));
		await new Promise((r) => setTimeout(r, 10));
		assert.deepStrictEqual(lines, []);

		(fakeProc.stdout as Readable).push(Buffer.from("ial\n"));
		await new Promise((r) => setTimeout(r, 10));
		assert.deepStrictEqual(lines, ["partial"]);
	});

	test("stdout goes to outputChannel when onStdoutLine is not set", async () => {
		const fakeProc = createFakeProcess();
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		(fakeProc.stdout as Readable).push(Buffer.from("hello"));
		await new Promise((r) => setTimeout(r, 10));

		assert.ok(output.lines.some((l) => l.includes("hello")));
	});

	test("onPreviewStop fires when stopPreview is called", async () => {
		const fakeProc = createFakeProcess();
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		let stopped = false;
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
			onPreviewStop: () => {
				stopped = true;
			},
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.stopPreview();

		assert.strictEqual(stopped, true);
	});

	test("spawn args use serve subcommand (no source file)", async () => {
		const spawnedArgs: string[][] = [];
		const fakeProc = createFakeProcess();
		fakeProc.stdin = new Writable({
			write(_c, _e, cb) {
				cb();
			},
		});
		const spawnFn: SpawnFn = (_cmd, args) => {
			spawnedArgs.push(args);
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const config: AxeConfig = {
			...DEFAULT_CONFIG,
			project: "/path/to/project.xcodeproj",
			scheme: "MyScheme",
		};
		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => config,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		const args = spawnedArgs[0];
		assert.ok(args.includes("preview"), "should include 'preview' subcommand");
		assert.ok(args.includes("serve"), "should include 'serve' subcommand");
		assert.ok(!args.includes("--watch"), "should not include --watch flag");
		assert.ok(!args.includes("--serve"), "should not include --serve flag");
		assert.ok(args.includes("--project"), "should include --project");
		assert.ok(args.includes("MyScheme"), "should include scheme value");
		// Source file should NOT be in args (it's sent via AddStream command)
		assert.ok(
			!args.includes("/path/to/HogeView.swift"),
			"should not include source file in spawn args",
		);
	});

	test("sendInput touchMove writes correct Command JSON", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({
			type: "touchMove",
			streamId: "stream-a",
			x: 0.7,
			y: 0.8,
		});

		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.strictEqual(parsed.input.touchMove.x, 0.7);
		assert.strictEqual(parsed.input.touchMove.y, 0.8);
	});

	test("sendInput touchUp writes correct Command JSON", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({
			type: "touchUp",
			streamId: "stream-a",
			x: 0.2,
			y: 0.9,
		});

		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.strictEqual(parsed.input.touchUp.x, 0.2);
		assert.strictEqual(parsed.input.touchUp.y, 0.9);
	});

	test("sendInput text writes correct Command JSON", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({ type: "text", streamId: "stream-a", value: "hello" });

		const parsed = JSON.parse(chunks[1].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.strictEqual(parsed.input.text.value, "hello");
	});

	test("sendInput with unknown type is no-op", async () => {
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => fakeProc;
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		manager.sendInput({ type: "unknownEvent", streamId: "stream-a" });

		// Only the AddStream command was sent, unknown type was ignored
		assert.strictEqual(chunks.length, 1);
	});

	test("nextPreview is no-op when no process", () => {
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: () => createFakeProcess(),
			getConfig: () => DEFAULT_CONFIG,
		});

		// Should not throw
		manager.nextPreview("stream-a");
		assert.strictEqual(manager.isRunning, false);
	});

	test("forceRebuild is no-op when no process", () => {
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: () => createFakeProcess(),
			getConfig: () => DEFAULT_CONFIG,
		});

		// Should not throw
		manager.forceRebuild("stream-a");
		assert.strictEqual(manager.isRunning, false);
	});

	test("replaceAllStreams sends RemoveStream for old streams and AddStream for new", async () => {
		let spawnCount = 0;
		const fakeProc = createFakeProcess();
		fakeProc._kill = () => {};
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => {
			spawnCount++;
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		// Add two streams first.
		await manager.addStream(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);
		await manager.addStream(
			"stream-b",
			"/path/to/FugaView.swift",
			"iPhone15,4",
			"iOS-17-0",
		);
		assert.strictEqual(spawnCount, 1);
		assert.strictEqual(manager.streamCount, 2);

		const chunksBeforeReplace = chunks.length;

		// Replace all with a new stream.
		await manager.replaceAllStreams(
			"stream-c",
			"/path/to/PiyoView.swift",
			"iPhone16,2",
			"iOS-18-1",
		);

		assert.strictEqual(spawnCount, 1); // no new process spawned
		assert.strictEqual(manager.streamCount, 1);
		assert.strictEqual(manager.isRunning, true);

		// Should have: RemoveStream(stream-a), RemoveStream(stream-b), AddStream(stream-c)
		const newChunks = chunks.slice(chunksBeforeReplace);
		assert.strictEqual(newChunks.length, 3);

		const remove1 = JSON.parse(newChunks[0].trim());
		assert.ok(remove1.removeStream !== undefined);

		const remove2 = JSON.parse(newChunks[1].trim());
		assert.ok(remove2.removeStream !== undefined);

		const removedIds = [remove1.streamId, remove2.streamId].sort();
		assert.deepStrictEqual(removedIds, ["stream-a", "stream-b"]);

		const add = JSON.parse(newChunks[2].trim());
		assert.strictEqual(add.streamId, "stream-c");
		assert.strictEqual(add.addStream.file, "/path/to/PiyoView.swift");
		assert.strictEqual(add.addStream.deviceType, "iPhone16,2");
		assert.strictEqual(add.addStream.runtime, "iOS-18-1");
	});

	test("replaceAllStreams spawns process if not running", async () => {
		let spawnCount = 0;
		const fakeProc = createFakeProcess();
		const chunks: string[] = [];
		fakeProc.stdin = new Writable({
			write(chunk, _enc, cb) {
				chunks.push(chunk.toString());
				cb();
			},
		});

		const spawnFn: SpawnFn = () => {
			spawnCount++;
			return fakeProc;
		};
		const output = createFakeOutputChannel();
		const statusBar = createFakeStatusBar();

		const manager = new PreviewManager(output, statusBar, {
			spawn: spawnFn,
			getConfig: () => DEFAULT_CONFIG,
		});

		assert.strictEqual(manager.isRunning, false);

		await manager.replaceAllStreams(
			"stream-a",
			"/path/to/HogeView.swift",
			"iPhone16,1",
			"iOS-18-0",
		);

		assert.strictEqual(spawnCount, 1);
		assert.strictEqual(manager.isRunning, true);
		assert.strictEqual(manager.streamCount, 1);

		const parsed = JSON.parse(chunks[0].trim());
		assert.strictEqual(parsed.streamId, "stream-a");
		assert.ok(parsed.addStream);
	});
});
