import * as assert from "assert";
import { EventEmitter } from "events";
import { Writable, Readable } from "stream";
import {
  PreviewManager,
  SpawnFn,
  AxeProcess,
  OutputChannel,
  StatusBarLike,
} from "../previewManager";
import { AxeConfig } from "../config";

// --- Helpers ---

interface FakeProcess extends AxeProcess {
  _kill: (signal?: string) => void;
  emit(event: string, ...args: unknown[]): boolean;
}

function createFakeProcess(): FakeProcess {
  const emitter = new EventEmitter();
  const proc: FakeProcess = Object.assign(emitter, {
    killed: false,
    stdin: new Writable({ write(_c, _e, cb) { cb(); } }) as NodeJS.WritableStream,
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
    appendLine(text: string) { lines.push(text); },
    append(text: string) { lines.push(text); },
    show(_preserveFocus?: boolean) {},
  };
}

function createFakeStatusBar(): StatusBarLike & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    showRunning(fileName: string) { calls.push(`running:${fileName}`); },
    showError() { calls.push("error"); },
    hide() { calls.push("hide"); },
  };
}

const DEFAULT_CONFIG: AxeConfig = {
  executablePath: "axe",
  project: "",
  workspace: "",
  scheme: "",
  configuration: "",
  additionalArgs: [],
  previewDevice: "",
};

suite("PreviewManager", () => {
  test("startPreview spawns a process and updates status bar", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/MyView.swift");

    assert.strictEqual(manager.isRunning, true);
    assert.ok(statusBar.calls.includes("running:MyView.swift"));
    assert.ok(output.lines.some((l) => l.includes("axe")));
  });

  test("startPreview with same file is idempotent", async () => {
    let spawnCount = 0;
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => { spawnCount++; return fakeProc; };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    await manager.startPreview("/path/to/File.swift");

    assert.strictEqual(spawnCount, 1);
  });

  test("switching to a different file writes JSON to stdin instead of killing", async () => {
    const fakeProc = createFakeProcess();
    const chunks: string[] = [];
    fakeProc.stdin = new Writable({
      write(chunk, _enc, cb) { chunks.push(chunk.toString()); cb(); },
    });

    let spawnCount = 0;
    const spawnFn: SpawnFn = () => { spawnCount++; return fakeProc; };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/A.swift");
    assert.strictEqual(spawnCount, 1);

    await manager.startPreview("/path/to/B.swift");
    assert.strictEqual(spawnCount, 1); // no new process spawned
    assert.strictEqual(fakeProc.killed, false); // old process still alive

    // Verify JSON protocol
    const parsed = JSON.parse(chunks[0].trim());
    assert.strictEqual(parsed.type, "switchFile");
    assert.strictEqual(parsed.path, "/path/to/B.swift");
    assert.strictEqual(manager.isRunning, true);
    assert.ok(statusBar.calls.includes("running:B.swift"));
  });

  test("switching file is idempotent for the new file", async () => {
    const fakeProc = createFakeProcess();
    const chunks: string[] = [];
    fakeProc.stdin = new Writable({
      write(chunk, _enc, cb) { chunks.push(chunk.toString()); cb(); },
    });

    let spawnCount = 0;
    const spawnFn: SpawnFn = () => { spawnCount++; return fakeProc; };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/A.swift");
    await manager.startPreview("/path/to/B.swift"); // stdin write
    await manager.startPreview("/path/to/B.swift"); // same file → no-op

    assert.strictEqual(spawnCount, 1);
    assert.strictEqual(chunks.length, 1); // only one write
  });

  test("switching file after process exit spawns a new process", async () => {
    const procs: FakeProcess[] = [];
    const spawnFn: SpawnFn = () => {
      const p = createFakeProcess();
      p._kill = () => {}; // don't auto-exit on kill
      procs.push(p);
      return p;
    };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/A.swift");
    assert.strictEqual(procs.length, 1);

    // Simulate process exit
    procs[0].emit("exit", 0, null);
    await new Promise((r) => setTimeout(r, 10));

    assert.strictEqual(manager.isRunning, false);

    await manager.startPreview("/path/to/B.swift");
    assert.strictEqual(procs.length, 2); // new process spawned
    assert.strictEqual(manager.isRunning, true);
  });

  test("process exit after file switch clears state correctly", async () => {
    const fakeProc = createFakeProcess();
    fakeProc._kill = () => {}; // don't auto-exit
    fakeProc.stdin = new Writable({
      write(_c, _e, cb) { cb(); },
    });

    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    let stopCount = 0;
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      onPreviewStop: () => { stopCount++; },
    });

    await manager.startPreview("/path/to/A.swift");
    await manager.startPreview("/path/to/B.swift"); // stdin switch

    // Process exits after file switch
    fakeProc.emit("exit", 1, null);
    await new Promise((r) => setTimeout(r, 10));

    assert.strictEqual(manager.isRunning, false);
    assert.strictEqual(stopCount, 1);
    assert.ok(statusBar.calls.includes("hide"));
  });

  test("stopPreview sends SIGTERM", async () => {
    let killSignal: string | undefined;
    const fakeProc = createFakeProcess();
    fakeProc._kill = (signal) => {
      killSignal = signal;
      process.nextTick(() => fakeProc.emit("exit", null, signal));
    };

    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    await manager.stopPreview();

    assert.strictEqual(killSignal, "SIGTERM");
    assert.strictEqual(manager.isRunning, false);
    assert.ok(statusBar.calls.includes("hide"));
  });

  test("stopPreview when no process is a no-op", async () => {
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: () => createFakeProcess(),
      getConfig: () => DEFAULT_CONFIG,
    });

    // Should not throw
    await manager.stopPreview();
    assert.strictEqual(manager.isRunning, false);
  });

  test("nextPreview writes JSON to stdin", async () => {
    const fakeProc = createFakeProcess();
    const chunks: string[] = [];
    fakeProc.stdin = new Writable({
      write(chunk, _enc, cb) { chunks.push(chunk.toString()); cb(); },
    });

    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    manager.nextPreview();

    const parsed = JSON.parse(chunks[0].trim());
    assert.strictEqual(parsed.type, "nextPreview");
  });

  test("nextPreview is a no-op when no process is running", () => {
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: () => createFakeProcess(),
      getConfig: () => DEFAULT_CONFIG,
    });

    // Should not throw
    manager.nextPreview();
  });

  test("sendInput writes JSON to stdin", async () => {
    const fakeProc = createFakeProcess();
    const chunks: string[] = [];
    fakeProc.stdin = new Writable({
      write(chunk, _enc, cb) { chunks.push(chunk.toString()); cb(); },
    });

    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    manager.sendInput({ type: "tap", x: 0.5, y: 0.3 });

    const parsed = JSON.parse(chunks[0].trim());
    assert.strictEqual(parsed.type, "tap");
    assert.strictEqual(parsed.x, 0.5);
    assert.strictEqual(parsed.y, 0.3);
  });

  test("sendInput is no-op when no process", () => {
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: () => createFakeProcess(),
      getConfig: () => DEFAULT_CONFIG,
    });

    // Should not throw
    manager.sendInput({ type: "tap", x: 0.5, y: 0.3 });
  });

  test("status bar shows error on non-zero exit", async () => {
    const fakeProc = createFakeProcess();
    fakeProc._kill = () => {}; // don't auto-exit
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    fakeProc.emit("exit", 1, null);

    // Wait for event processing
    await new Promise((r) => setTimeout(r, 10));

    assert.ok(statusBar.calls.includes("error"));
    assert.ok(output.lines.some((l) => l.includes("exited with code 1")));
  });

  test("dispose sends SIGTERM to running process", async () => {
    let killSignal: string | undefined;
    const fakeProc = createFakeProcess();
    fakeProc._kill = (signal) => { killSignal = signal; };

    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
    });

    await manager.startPreview("/path/to/File.swift");
    manager.dispose();

    assert.strictEqual(killSignal, "SIGTERM");
  });

  test("startPreview uses resolveExecutablePath when provided", async () => {
    let spawnedCommand = "";
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = (cmd) => { spawnedCommand = cmd; return fakeProc; };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      resolveExecutablePath: async () => "/resolved/bin/axe",
    });

    await manager.startPreview("/path/to/File.swift");

    assert.strictEqual(spawnedCommand, "/resolved/bin/axe");
    assert.ok(output.lines.some((l) => l.includes("/resolved/bin/axe")));
  });

  test("startPreview falls back to config when resolveExecutablePath is not set", async () => {
    let spawnedCommand = "";
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = (cmd) => { spawnedCommand = cmd; return fakeProc; };
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const config: AxeConfig = { ...DEFAULT_CONFIG, executablePath: "/config/axe" };
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => config,
    });

    await manager.startPreview("/path/to/File.swift");

    assert.strictEqual(spawnedCommand, "/config/axe");
  });

  test("startPreview propagates resolveExecutablePath error", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      resolveExecutablePath: async () => { throw new Error("axe binary not available"); },
    });

    await assert.rejects(
      () => manager.startPreview("/path/to/File.swift"),
      { message: "axe binary not available" }
    );
    assert.strictEqual(manager.isRunning, false);
  });

  test("onStdoutLine callback receives lines from stdout", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const lines: string[] = [];
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      onStdoutLine: (line) => { lines.push(line); },
    });

    await manager.startPreview("/path/to/File.swift");

    // Emit data with newlines
    (fakeProc.stdout as Readable).push(Buffer.from("line1\nline2\n"));

    await new Promise((r) => setTimeout(r, 10));

    assert.deepStrictEqual(lines, ["line1", "line2"]);
  });

  test("onStdoutLine buffers partial lines", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const lines: string[] = [];
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      onStdoutLine: (line) => { lines.push(line); },
    });

    await manager.startPreview("/path/to/File.swift");

    // Emit partial data
    (fakeProc.stdout as Readable).push(Buffer.from("part"));
    await new Promise((r) => setTimeout(r, 10));
    assert.deepStrictEqual(lines, []);

    // Complete the line
    (fakeProc.stdout as Readable).push(Buffer.from("ial\n"));
    await new Promise((r) => setTimeout(r, 10));
    assert.deepStrictEqual(lines, ["partial"]);
  });

  test("stdout goes to outputChannel when onStdoutLine is not set", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      // no onStdoutLine
    });

    await manager.startPreview("/path/to/File.swift");
    (fakeProc.stdout as Readable).push(Buffer.from("hello"));
    await new Promise((r) => setTimeout(r, 10));

    assert.ok(output.lines.some((l) => l.includes("hello")));
  });

  test("onPreviewStop fires when stopPreview is called", async () => {
    const fakeProc = createFakeProcess();
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    let stopped = false;
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      onPreviewStop: () => { stopped = true; },
    });

    await manager.startPreview("/path/to/File.swift");
    await manager.stopPreview();

    assert.strictEqual(stopped, true);
  });

  test("onPreviewStop fires on process exit", async () => {
    const fakeProc = createFakeProcess();
    fakeProc._kill = () => {}; // don't auto-exit
    const spawnFn: SpawnFn = () => fakeProc;
    const output = createFakeOutputChannel();
    const statusBar = createFakeStatusBar();

    let stopCount = 0;
    const manager = new PreviewManager(output, statusBar, {
      spawn: spawnFn,
      getConfig: () => DEFAULT_CONFIG,
      onPreviewStop: () => { stopCount++; },
    });

    await manager.startPreview("/path/to/File.swift");
    fakeProc.emit("exit", 0, null);
    await new Promise((r) => setTimeout(r, 10));

    assert.strictEqual(stopCount, 1);
  });
});
