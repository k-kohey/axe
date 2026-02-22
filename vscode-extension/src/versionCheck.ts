import { execFile } from "node:child_process";
import * as vscode from "vscode";
import { runInstallScript } from "./installScript";

/** Minimum CLI version this extension supports. Replaced by CI at release time. */
export const MIN_CLI_VERSION = "0.0.0";

const VERSION_QUERY_TIMEOUT_MS = 5_000;

export interface VersionCheckDeps {
	queryCliVersion?: (execPath: string) => Promise<string>;
	showWarning?: (
		message: string,
		...items: string[]
	) => Thenable<string | undefined>;
	runInstallScript?: () => void;
}

/** Run `axe --version` and return the version string. */
export function queryCliVersion(execPath: string): Promise<string> {
	return new Promise((resolve, reject) => {
		const child = execFile(
			execPath,
			["--version"],
			{ timeout: VERSION_QUERY_TIMEOUT_MS },
			(err, stdout) => {
				if (err) {
					reject(new Error(`Failed to query axe version: ${err.message}`));
					return;
				}
				// CLI outputs "axe version v1.2.3"; extract the numeric semver part.
				const match = stdout.trim().match(/(\d+\.\d+\.\d+)/);
				if (!match) {
					reject(new Error(`Unexpected version output: ${stdout.trim()}`));
					return;
				}
				resolve(match[1]);
			},
		);
		child.unref();
	});
}

/**
 * Compare two semver strings (major.minor.patch).
 * Returns negative if a < b, 0 if equal, positive if a > b.
 */
export function compareSemver(a: string, b: string): number {
	const pa = a.split(".").map(Number);
	const pb = b.split(".").map(Number);
	for (let i = 0; i < 3; i++) {
		const diff = (pa[i] ?? 0) - (pb[i] ?? 0);
		if (diff !== 0) return diff;
	}
	return 0;
}

/**
 * Check that the CLI version meets the minimum requirement.
 * Shows a warning if the version is too old. Never throws — failures are logged only.
 */
export async function checkCliVersion(
	execPath: string,
	deps?: VersionCheckDeps,
): Promise<void> {
	if (MIN_CLI_VERSION === "0.0.0") {
		return;
	}

	const query = deps?.queryCliVersion ?? queryCliVersion;
	const showWarning =
		deps?.showWarning ??
		((msg: string, ...items: string[]) =>
			vscode.window.showWarningMessage(msg, ...items));
	const install = deps?.runInstallScript ?? runInstallScript;

	try {
		const cliVersion = await query(execPath);
		if (compareSemver(cliVersion, MIN_CLI_VERSION) < 0) {
			const choice = await showWarning(
				`axe CLI version ${cliVersion} is older than the minimum supported version ${MIN_CLI_VERSION}.`,
				"Run Install Script",
			);
			if (choice === "Run Install Script") {
				install();
			}
		}
	} catch (e) {
		const message = e instanceof Error ? e.message : String(e);
		console.warn(`[axe] Version check skipped: ${message}`);
	}
}
