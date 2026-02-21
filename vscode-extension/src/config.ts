import * as vscode from "vscode";

export interface AxeConfig {
  executablePath: string;
  project: string;
  workspace: string;
  scheme: string;
  configuration: string;
  additionalArgs: string[];
  previewDevice: string;
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
    previewDevice: cfg.get<string>("preview.device", ""),
  };
}

export function buildArgs(filePath: string, config: AxeConfig): string[] {
  const args = ["preview", filePath, "--watch"];

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
  if (config.previewDevice) {
    args.push("--device", config.previewDevice);
  }
  args.push("--serve");
  args.push(...config.additionalArgs);

  return args;
}
