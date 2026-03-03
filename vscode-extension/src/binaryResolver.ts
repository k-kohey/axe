import { execFile } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";
import { runInstallScript } from "./installScript";
import { checkCliVersion, type VersionCheckDeps } from "./versionCheck";

/** Default install location used by install.sh */
const DEFAULT_INSTALL_DIR = path.join(os.homedir(), ".local", "bin");

export interface BinaryResolverDeps {
	which?: (command: string) => Promise<string | null>;
	showPrompt?: (
		message: string,
		...items: string[]
	) => Thenable<string | undefined>;
	openSettings?: (settingId: string) => Promise<void>;
	createTerminal?: (name: string) => vscode.Terminal;
	versionCheck?: VersionCheckDeps;
	waitForTerminalClose?: (terminal: vscode.Terminal) => Promise<void>;
}

function defaultWhich(command: string): Promise<string | null> {
	return new Promise((resolve) => {
		execFile("which", [command], (err, stdout) => {
			if (!err) {
				const p = stdout.trim();
				if (p) {
					resolve(p);
					return;
				}
			}
			// Fallback: check the default install location (~/.local/bin).
			// `which` uses the Node.js process PATH which may not include
			// the directory where install.sh places the binary.
			const fallbackPath = path.join(DEFAULT_INSTALL_DIR, command);
			fs.access(fallbackPath, fs.constants.X_OK, (accessErr) => {
				resolve(accessErr ? null : fallbackPath);
			});
		});
	});
}

function defaultWaitForTerminalClose(terminal: vscode.Terminal): Promise<void> {
	return new Promise((resolve) => {
		const disposable = vscode.window.onDidCloseTerminal((t) => {
			if (t === terminal) {
				disposable.dispose();
				resolve();
			}
		});
	});
}

/** Interval (ms) between install-retry attempts. */
const INSTALL_RETRY_INTERVAL_MS = 90 * 1000; // 1.5 minutes
/** Maximum number of times to retry `which` after running the install script. */
const MAX_INSTALL_RETRIES = 3;

async function defaultOpenSettings(settingId: string): Promise<void> {
	await vscode.commands.executeCommand(
		"workbench.action.openSettings",
		settingId,
	);
}

/**
 * Thrown when the user has initiated an action (install / configure) that
 * will eventually provide the binary.  Callers should silently swallow
 * this error instead of showing an error dialog.
 */
export class UserActionPendingError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "UserActionPendingError";
	}
}

export class BinaryResolver {
	private cachedPath: string | null = null;
	private readonly deps: Required<BinaryResolverDeps>;

	constructor(deps?: BinaryResolverDeps) {
		this.deps = {
			which: deps?.which ?? defaultWhich,
			showPrompt:
				deps?.showPrompt ??
				((msg, ...items) =>
					vscode.window.showInformationMessage(msg, ...items)),
			openSettings: deps?.openSettings ?? defaultOpenSettings,
			createTerminal:
				deps?.createTerminal ?? ((name) => vscode.window.createTerminal(name)),
			versionCheck: deps?.versionCheck ?? {},
			waitForTerminalClose:
				deps?.waitForTerminalClose ?? defaultWaitForTerminalClose,
		};
	}

	async resolve(): Promise<string> {
		if (this.cachedPath) {
			return this.cachedPath;
		}

		// 1. Check explicit setting
		const cfg = vscode.workspace.getConfiguration("axe");
		const configured = cfg.get<string>("executablePath", "axe");
		if (configured !== "axe") {
			this.cachedPath = configured;
			// Fire-and-forget: version check runs in the background so it never
			// blocks binary resolution. Warnings are shown asynchronously.
			checkCliVersion(configured, this.deps.versionCheck);
			return configured;
		}

		// 2. Check PATH via `which`
		const whichPath = await this.deps.which("axe");
		if (whichPath) {
			this.cachedPath = whichPath;
			// Fire-and-forget: same rationale as above.
			checkCliVersion(whichPath, this.deps.versionCheck);
			return whichPath;
		}

		// 3. Prompt to install
		const choice = await this.deps.showPrompt(
			"axe CLI was not found. Install it?",
			"Run Install Script",
			"Configure Path",
		);

		if (choice === "Run Install Script") {
			const terminal = runInstallScript(this.deps.createTerminal);
			const terminalClosed = this.deps.waitForTerminalClose(terminal);

			// Retry `which` up to MAX_INSTALL_RETRIES times, waiting
			// INSTALL_RETRY_INTERVAL_MS between each attempt. If the terminal
			// closes early the interval resolves immediately on subsequent iterations.
			for (let i = 0; i < MAX_INSTALL_RETRIES; i++) {
				await Promise.race([
					terminalClosed,
					new Promise<void>((r) => setTimeout(r, INSTALL_RETRY_INTERVAL_MS)),
				]);

				const retryPath = await this.deps.which("axe");
				if (retryPath) {
					this.cachedPath = retryPath;
					checkCliVersion(retryPath, this.deps.versionCheck);
					return retryPath;
				}
			}

			throw new Error(
				"axe CLI was not found after installation. " +
					"Ensure ~/.local/bin is in your PATH, or set axe.executablePath in settings.",
			);
		}

		if (choice === "Configure Path") {
			await this.deps.openSettings("axe.executablePath");
			throw new UserActionPendingError("axe binary not available");
		}

		throw new Error("axe binary not available");
	}

	clearCache(): void {
		this.cachedPath = null;
	}
}
