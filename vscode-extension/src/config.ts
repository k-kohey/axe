import * as vscode from "vscode";

export interface AxeConfig {
	executablePath: string;
	project: string;
	workspace: string;
	scheme: string;
	configuration: string;
	additionalArgs: string[];
}

export function getConfig(): AxeConfig {
	const cfg = vscode.workspace.getConfiguration("axe");
	return {
		executablePath: cfg.get<string>("executablePath", "axe"),
		project: cfg.get<string>("project", ""),
		workspace: cfg.get<string>("workspace", ""),
		scheme: cfg.get<string>("scheme", ""),
		configuration: cfg.get<string>("configuration", ""),
		additionalArgs: cfg.get<string[]>("additionalArgs", []),
	};
}

/**
 * Build CLI arguments for axe preview serve.
 * Source files are provided via AddStream commands on stdin, not as CLI args.
 * Device selection is per-stream via AddStream, not a global flag.
 */
export function buildArgs(config: AxeConfig): string[] {
	const args = ["preview", "serve"];

	if (config.project) {
		args.push("--project", config.project);
	}
	if (config.workspace) {
		args.push("--workspace", config.workspace);
	}
	if (config.scheme) {
		args.push("--scheme", config.scheme);
	}
	if (config.configuration) {
		args.push("--configuration", config.configuration);
	}
	args.push(...config.additionalArgs);

	return args;
}
