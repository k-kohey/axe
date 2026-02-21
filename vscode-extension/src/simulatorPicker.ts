import * as vscode from "vscode";
import { spawn } from "child_process";

export interface ManagedSimulator {
  udid: string;
  name: string;
  runtime: string;
  runtimeId: string;
  state: string;
  isDefault: boolean;
}

export interface AvailableDeviceType {
  identifier: string;
  name: string;
  runtimes: { identifier: string; name: string }[];
}

const EXEC_TIMEOUT_MS = 30_000;

/**
 * Runs an axe CLI command and returns its stdout.
 */
export function execAxe(execPath: string, args: string[], cwd?: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const proc = spawn(execPath, args, {
      cwd,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    proc.stdout.on("data", (d: Buffer) => {
      stdout += d.toString();
    });
    proc.stderr.on("data", (d: Buffer) => {
      stderr += d.toString();
    });

    const timer = setTimeout(() => {
      proc.kill("SIGKILL");
      reject(new Error("axe command timed out"));
    }, EXEC_TIMEOUT_MS);

    proc.on("exit", (code) => {
      clearTimeout(timer);
      if (code === 0) {
        resolve(stdout.trim());
      } else {
        reject(new Error(`axe exited with code ${code}: ${stderr}`));
      }
    });
    proc.on("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
  });
}

/**
 * Shows a QuickPick to select an existing managed simulator.
 * Returns the selected UDID, or undefined if cancelled.
 * If no simulators exist, prompts the user to add one.
 */
export async function selectSimulator(
  execPath: string,
  cwd?: string
): Promise<string | undefined> {
  let managed: ManagedSimulator[];
  try {
    const raw = await execAxe(execPath, ["preview", "simulator", "list", "--json"], cwd);
    managed = JSON.parse(raw);
  } catch (err) {
    vscode.window.showErrorMessage(`Failed to list simulators: ${err}`);
    return undefined;
  }

  if (managed.length === 0) {
    const action = await vscode.window.showInformationMessage(
      "No preview simulators found. Add one?",
      "Add Simulator"
    );
    if (action === "Add Simulator") {
      return addSimulator(execPath, cwd);
    }
    return undefined;
  }

  interface SimItem extends vscode.QuickPickItem {
    udid: string;
  }

  const items: SimItem[] = managed.map((s) => ({
    label: s.name,
    description: `${s.runtime} · ${s.state}${s.isDefault ? " (default)" : ""}`,
    detail: s.udid,
    udid: s.udid,
  }));
  items.push({
    label: "$(add) Add new simulator...",
    description: "",
    detail: "",
    udid: "",
  });

  const picked = await vscode.window.showQuickPick(items, {
    placeHolder: "Select a simulator for preview",
  });

  if (!picked) {
    return undefined;
  }
  if (picked.udid === "") {
    return addSimulator(execPath, cwd);
  }

  // Set as global default in CLI config.
  try {
    await execAxe(execPath, ["preview", "simulator", "default", picked.udid], cwd);
  } catch (err) {
    vscode.window.showWarningMessage(`Selected simulator will be used, but failed to save as default: ${err}`);
  }

  return picked.udid;
}

/**
 * Shows a multi-step QuickPick to add a new simulator.
 * Returns the UDID of the created simulator, or undefined if cancelled.
 */
export async function addSimulator(
  execPath: string,
  cwd?: string
): Promise<string | undefined> {
  let available: AvailableDeviceType[];
  try {
    const raw = await execAxe(
      execPath,
      ["preview", "simulator", "list", "--available", "--json"],
      cwd
    );
    available = JSON.parse(raw);
  } catch (err) {
    vscode.window.showErrorMessage(`Failed to list available device types: ${err}`);
    return undefined;
  }

  if (available.length === 0) {
    vscode.window.showWarningMessage("No available device types found.");
    return undefined;
  }

  // Step 1: Pick device type.
  interface TypeItem extends vscode.QuickPickItem {
    identifier: string;
    runtimes: { identifier: string; name: string }[];
  }

  const typeItems: TypeItem[] = available.map((dt) => ({
    label: dt.name,
    detail: dt.identifier,
    identifier: dt.identifier,
    runtimes: dt.runtimes,
  }));

  const pickedType = await vscode.window.showQuickPick(typeItems, {
    placeHolder: "Select device type",
  });
  if (!pickedType) {
    return undefined;
  }

  // Step 2: Pick runtime.
  interface RuntimeItem extends vscode.QuickPickItem {
    identifier: string;
  }

  const runtimeItems: RuntimeItem[] = pickedType.runtimes.map((r) => ({
    label: r.name,
    detail: r.identifier,
    identifier: r.identifier,
  }));

  const pickedRuntime = await vscode.window.showQuickPick(runtimeItems, {
    placeHolder: "Select runtime",
  });
  if (!pickedRuntime) {
    return undefined;
  }

  // Step 3: Create the simulator.
  try {
    const raw = await execAxe(
      execPath,
      [
        "preview", "simulator", "add",
        "--device-type", pickedType.identifier,
        "--runtime", pickedRuntime.identifier,
        "--set-default",
        "--json",
      ],
      cwd
    );
    const created: ManagedSimulator = JSON.parse(raw);
    vscode.window.showInformationMessage(`Added simulator: ${created.name}`);
    return created.udid;
  } catch (err) {
    vscode.window.showErrorMessage(`Failed to add simulator: ${err}`);
    return undefined;
  }
}
