import { execFile } from "node:child_process";
import * as vscode from "vscode";
import { runInstallScript } from "./installScript";
import { checkCliVersion, type VersionCheckDeps } from "./versionCheck";

export interface BinaryResolverDeps {
	which?: (command: string) => Promise<string | null>;
	showPrompt?: (
		message: string,
		...items: string[]
	) => Thenable<string | undefined>;
	openSettings?: (settingId: string) => Promise<void>;
	createTerminal?: (name: string) => vscode.Terminal;
	versionCheck?: VersionCheckDeps;
}

function defaultWhich(command: string): Promise<string | null> {
	return new Promise((resolve) => {
		execFile("which", [command], (err, stdout) => {
			if (err) {
				resolve(null);
				return;
			}
			const p = stdout.trim();
			resolve(p || null);
		});
	});
}

async function defaultOpenSettings(settingId: string): Promise<void> {
	await vscode.commands.executeCommand(
		"workbench.action.openSettings",
		settingId,
	);
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
			runInstallScript(this.deps.createTerminal);
		}

		if (choice === "Configure Path") {
			await this.deps.openSettings("axe.executablePath");
		}

		throw new Error("axe binary not available");
	}

	clearCache(): void {
		this.cachedPath = null;
	}
}
