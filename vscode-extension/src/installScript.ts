import * as vscode from "vscode";

const INSTALL_COMMAND =
	"curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh";

/** Open a terminal and run the install script. */
export function runInstallScript(
	createTerminal: (name: string) => vscode.Terminal = (name) =>
		vscode.window.createTerminal(name),
): void {
	const terminal = createTerminal("axe install");
	terminal.show();
	terminal.sendText(INSTALL_COMMAND);
}
