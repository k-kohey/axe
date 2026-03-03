import * as vscode from "vscode";

const INSTALL_COMMAND =
	"curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh";

/** Open a terminal, run the install script, and return the terminal. */
export function runInstallScript(
	createTerminal: (name: string) => vscode.Terminal = (name) =>
		vscode.window.createTerminal(name),
): vscode.Terminal {
	const terminal = createTerminal("axe install");
	terminal.show();
	// `; exit` ensures the shell exits after install (success or failure),
	// so onDidCloseTerminal fires and the extension can retry resolution.
	terminal.sendText(`${INSTALL_COMMAND}; exit`);
	return terminal;
}
