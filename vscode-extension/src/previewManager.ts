import { spawn as nodeSpawn } from "node:child_process";
import * as path from "node:path";
import * as vscode from "vscode";
import {
	type AxeConfig,
	buildArgs,
	getConfig as defaultGetConfig,
} from "./config";
import { type Command, serializeCommand } from "./protocol";

const KILL_TIMEOUT_MS = 3000;

export interface AxeProcess {
	killed: boolean;
	stdin: NodeJS.WritableStream | null;
	stdout: NodeJS.ReadableStream | null;
	stderr: NodeJS.ReadableStream | null;
	kill(signal?: string): boolean;
	on(event: "error", listener: (err: Error) => void): this;
	on(
		event: "exit",
		listener: (code: number | null, signal: string | null) => void,
	): this;
	on(event: string, listener: (...args: unknown[]) => void): this;
	once(event: "exit", listener: () => void): this;
	once(event: string, listener: (...args: unknown[]) => void): this;
}

export type SpawnFn = (
	command: string,
	args: string[],
	options: { cwd?: string; stdio: ["pipe", "pipe", "pipe"] },
) => AxeProcess;

export interface OutputChannel {
	appendLine(value: string): void;
	append(value: string): void;
	show(preserveFocus?: boolean): void;
}

export interface StatusBarLike {
	showRunning(fileName: string): void;
	showError(): void;
	hide(): void;
}

export interface InputMessage {
	type: string;
	streamId: string;
	[key: string]: unknown;
}

export interface StreamInfo {
	file: string;
	deviceType: string;
	runtime: string;
}

export interface PreviewManagerDeps {
	spawn?: SpawnFn;
	getConfig?: () => AxeConfig;
	resolveExecutablePath?: () => Promise<string>;
	onStdoutLine?: (line: string) => void;
	onPreviewStop?: () => void;
}

export class PreviewManager {
	private process: AxeProcess | null = null;
	private streams: Map<string, StreamInfo> = new Map();
	private outputChannel: OutputChannel;
	private statusBar: StatusBarLike;
	private spawnFn: SpawnFn;
	private getConfigFn: () => AxeConfig;
	private resolveExecutablePath?: () => Promise<string>;
	private onStdoutLine?: (line: string) => void;
	private onPreviewStop?: () => void;
	private stdoutBuf = "";

	constructor(
		outputChannel: OutputChannel,
		statusBar: StatusBarLike,
		deps?: PreviewManagerDeps,
	) {
		this.outputChannel = outputChannel;
		this.statusBar = statusBar;
		this.spawnFn = deps?.spawn ?? (nodeSpawn as unknown as SpawnFn);
		this.getConfigFn = deps?.getConfig ?? defaultGetConfig;
		this.resolveExecutablePath = deps?.resolveExecutablePath;
		this.onStdoutLine = deps?.onStdoutLine;
		this.onPreviewStop = deps?.onPreviewStop;
	}

	/** Add a new preview stream. Lazily spawns the axe process on first call. */
	async addStream(
		streamId: string,
		file: string,
		deviceType: string,
		runtime: string,
	): Promise<void> {
		if (this.streams.has(streamId)) {
			return;
		}

		if (!this.process || this.process.killed) {
			await this.spawnProcess();
		}

		this.streams.set(streamId, { file, deviceType, runtime });
		this.sendCommand({ streamId, addStream: { file, deviceType, runtime } });

		const fileName = path.basename(file);
		this.statusBar.showRunning(fileName);
	}

	/** Remove a preview stream. Stops the process when the last stream is removed. */
	async removeStream(streamId: string): Promise<void> {
		if (!this.streams.has(streamId)) {
			return;
		}
		this.sendCommand({ streamId, removeStream: {} });
		this.streams.delete(streamId);

		if (this.streams.size === 0) {
			await this.stopPreview();
		}
	}

	/**
	 * Replace all existing streams with a single new stream.
	 * Sends RemoveStream commands for old streams and AddStream for the new one
	 * without stopping the process (avoids unnecessary restart during auto-switch).
	 */
	async replaceAllStreams(
		streamId: string,
		file: string,
		deviceType: string,
		runtime: string,
	): Promise<void> {
		// Send RemoveStream for all existing streams.
		for (const [sid] of this.streams) {
			this.sendCommand({ streamId: sid, removeStream: {} });
		}
		this.streams.clear();

		// Ensure process is running.
		if (!this.process || this.process.killed) {
			await this.spawnProcess();
		}

		// Add the new stream.
		this.streams.set(streamId, { file, deviceType, runtime });
		this.sendCommand({ streamId, addStream: { file, deviceType, runtime } });

		const fileName = path.basename(file);
		this.statusBar.showRunning(fileName);
	}

	/** Send a nextPreview command for a specific stream. */
	nextPreview(streamId: string): void {
		this.sendCommand({ streamId, nextPreview: {} });
	}

	/** Force a rebuild + relaunch for a specific stream. */
	forceRebuild(streamId: string): void {
		this.sendCommand({ streamId, forceRebuild: {} });
	}

	/** Stop the axe process and clear all streams. */
	async stopPreview(): Promise<void> {
		if (!this.process) {
			return;
		}

		const proc = this.process;
		this.process = null;
		this.streams.clear();
		this.statusBar.hide();
		this.onPreviewStop?.();

		if (proc.killed) {
			return;
		}

		return new Promise<void>((resolve) => {
			const killTimer = setTimeout(() => {
				if (!proc.killed) {
					proc.kill("SIGKILL");
				}
				resolve();
			}, KILL_TIMEOUT_MS);

			proc.once("exit", () => {
				clearTimeout(killTimer);
				resolve();
			});

			proc.kill("SIGTERM");
		});
	}

	// sendCommand writes a protocol Command to the axe process stdin.
	private sendCommand(cmd: Command): void {
		if (!this.process || this.process.killed || !this.process.stdin) {
			return;
		}
		this.process.stdin.write(`${serializeCommand(cmd)}\n`);
	}

	// sendInput converts a WebView InputMessage to a protocol Command and sends it.
	sendInput(msg: InputMessage): void {
		if (!this.process || this.process.killed || !this.process.stdin) {
			return;
		}
		if (!msg.streamId) {
			return;
		}
		const cmd: Command = { streamId: msg.streamId };
		switch (msg.type) {
			case "touchDown":
				cmd.input = { touchDown: { x: msg.x as number, y: msg.y as number } };
				break;
			case "touchMove":
				cmd.input = { touchMove: { x: msg.x as number, y: msg.y as number } };
				break;
			case "touchUp":
				cmd.input = { touchUp: { x: msg.x as number, y: msg.y as number } };
				break;
			case "text":
				cmd.input = { text: { value: msg.value as string } };
				break;
			default:
				return;
		}
		this.sendCommand(cmd);
	}

	get isRunning(): boolean {
		return this.process !== null && !this.process.killed;
	}

	get streamCount(): number {
		return this.streams.size;
	}

	dispose(): void {
		if (this.process && !this.process.killed) {
			this.process.kill("SIGTERM");
		}
	}

	private async spawnProcess(): Promise<void> {
		const config = this.getConfigFn();
		const executablePath = this.resolveExecutablePath
			? await this.resolveExecutablePath()
			: config.executablePath;
		const args = buildArgs(config);
		const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

		this.outputChannel.appendLine(`> ${executablePath} ${args.join(" ")}`);
		this.outputChannel.show(true);

		this.process = this.spawnFn(executablePath, args, {
			cwd,
			stdio: ["pipe", "pipe", "pipe"],
		});

		this.process.stdin?.on("error", (err: Error) => {
			this.outputChannel.appendLine(`[axe stdin] ${err.message}`);
		});

		this.stdoutBuf = "";
		this.process.stdout?.on("data", (data: Buffer) => {
			if (this.onStdoutLine) {
				this.stdoutBuf += data.toString();
				const lines = this.stdoutBuf.split("\n");
				this.stdoutBuf = lines.pop() ?? "";
				for (const line of lines) {
					this.onStdoutLine(line);
				}
			} else {
				this.outputChannel.append(data.toString());
			}
		});

		this.process.stderr?.on("data", (data: Buffer) => {
			this.outputChannel.append(data.toString());
		});

		this.process.on("error", (err: Error) => {
			this.outputChannel.appendLine(`[axe error] ${err.message}`);
			this.statusBar.showError();
		});

		const proc = this.process;
		this.process.on("exit", (code: number | null, signal: string | null) => {
			if (signal) {
				this.outputChannel.appendLine(`[axe] terminated by ${signal}`);
			} else if (code !== 0) {
				this.outputChannel.appendLine(`[axe] exited with code ${code}`);
				this.statusBar.showError();
			}
			if (this.process === proc) {
				this.process = null;
				this.streams.clear();
				this.statusBar.hide();
				this.onPreviewStop?.();
			}
		});
	}
}
