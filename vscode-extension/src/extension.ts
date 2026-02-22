import * as vscode from "vscode";
import * as crypto from "crypto";
import * as path from "path";
import { PreviewManager } from "./previewManager";
import { StatusBar } from "./statusBar";
import { containsPreview } from "./previewDetector";
import { BinaryResolver } from "./binaryResolver";
import { SimulatorWebviewPanel } from "./simulatorWebview";
import { selectDevice, DeviceSelection } from "./simulatorPicker";
import { parseEvent, isFrame, isStreamStarted, isStreamStopped, isStreamStatus } from "./protocol";

let previewManager: PreviewManager;
let statusBar: StatusBar;
let webviewPanel: SimulatorWebviewPanel;
let resolver: BinaryResolver;

// Track active streams: file → streamId.
const activeStreams = new Map<string, string>();
let lastDevice: DeviceSelection | null = null;

function generateStreamId(): string {
  return crypto.randomUUID();
}

export function activate(context: vscode.ExtensionContext): void {
  const outputChannel = vscode.window.createOutputChannel("axe SwiftUI Preview");
  statusBar = new StatusBar();

  resolver = new BinaryResolver();

  webviewPanel = new SimulatorWebviewPanel();

  previewManager = new PreviewManager(outputChannel, statusBar, {
    resolveExecutablePath: () => resolver.resolve(),
    onStdoutLine: (line) => {
      const event = parseEvent(line);
      if (!event) {
        return;
      }

      if (isFrame(event)) {
        webviewPanel.postFrame(event.streamId, event.frame.data);
      } else if (isStreamStarted(event)) {
        // Panel is already shown when addStream is called.
      } else if (isStreamStopped(event)) {
        outputChannel.appendLine(
          `[axe] Stream stopped: ${event.streamStopped.reason} - ${event.streamStopped.message}`
        );
        if (event.streamStopped.diagnostic) {
          outputChannel.appendLine(event.streamStopped.diagnostic);
        }
        webviewPanel.postStatus(event.streamId, `Stopped: ${event.streamStopped.reason}`);
      } else if (isStreamStatus(event)) {
        webviewPanel.postStatus(event.streamId, event.streamStatus.phase);
      }
    },
    onPreviewStop: () => {
      // Process exited — clear all tracking and remove cards.
      for (const [, streamId] of activeStreams) {
        webviewPanel.removeCard(streamId);
      }
      activeStreams.clear();
    },
  });

  // Connect WebView input events (touch/text) to the preview process.
  webviewPanel.setInputHandler((msg) => {
    previewManager.sendInput(msg);
  });

  // Connect WebView control messages (removeStream, changeDevice, nextPreview).
  webviewPanel.setWebViewMessageHandler((msg) => {
    if (msg.type === "removeStream" && msg.streamId) {
      void untrackStream(msg.streamId);
    } else if (msg.type === "changeDevice" && msg.streamId) {
      void handleChangeDevice(msg.streamId);
    } else if (msg.type === "nextPreview" && msg.streamId) {
      previewManager.nextPreview(msg.streamId);
    }
  });

  // Handle active editor changes — auto-start preview for #Preview files.
  const editorListener = vscode.window.onDidChangeActiveTextEditor(
    (editor) => {
      if (!editor) {
        return;
      }
      handleEditor(editor);
    }
  );

  // Register showPreview command — explicit device picker + addStream.
  const showPreviewCmd = vscode.commands.registerCommand(
    "axe.showPreview",
    () => {
      webviewPanel.resetDismissed();
      const editor = vscode.window.activeTextEditor;
      if (!editor) {
        return;
      }
      const file = editor.document.uri.fsPath;
      showDevicePickerAndAddStream(file);
    }
  );

  // Register stopPreview command — stop all streams.
  const stopPreviewCmd = vscode.commands.registerCommand(
    "axe.stopPreview",
    () => {
      previewManager.stopPreview();
    }
  );

  // Register nextPreview command — cycle preview in all active streams.
  const nextPreviewCmd = vscode.commands.registerCommand(
    "axe.nextPreview",
    () => {
      for (const [, streamId] of activeStreams) {
        previewManager.nextPreview(streamId);
      }
    }
  );

  // Clear resolver cache when executablePath changes.
  const configListener = vscode.workspace.onDidChangeConfiguration((e) => {
    if (e.affectsConfiguration("axe.executablePath")) {
      resolver.clearCache();
    }
  });

  context.subscriptions.push(
    editorListener,
    showPreviewCmd,
    stopPreviewCmd,
    nextPreviewCmd,
    configListener,
    {
      dispose: () => {
        previewManager.dispose();
        webviewPanel.dispose();
        statusBar.dispose();
        outputChannel.dispose();
      },
    }
  );

  // Check the currently active editor on activation.
  if (vscode.window.activeTextEditor) {
    handleEditor(vscode.window.activeTextEditor);
  }
}

/** Resolve the axe binary path via BinaryResolver. */
async function resolveExecPath(): Promise<string> {
  return resolver.resolve();
}

/** Show device picker using the resolved binary. */
async function pickDevice(): Promise<DeviceSelection | undefined> {
  let execPath: string;
  try {
    execPath = await resolveExecPath();
  } catch (err) {
    vscode.window.showErrorMessage(`Failed to resolve axe binary: ${err}`);
    return undefined;
  }
  const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  return selectDevice(execPath, cwd);
}

/** Remove a stream from PreviewManager, WebView, and activeStreams tracking. */
async function untrackStream(streamId: string): Promise<void> {
  await previewManager.removeStream(streamId);
  webviewPanel.removeCard(streamId);
  for (const [file, sid] of activeStreams) {
    if (sid === streamId) {
      activeStreams.delete(file);
      break;
    }
  }
}

/**
 * Auto-detect #Preview files and manage streams.
 * Uses the last device selection for auto-start. If no device has been
 * selected yet, this is a no-op (user must run "axe: Show Preview" first).
 */
function handleEditor(editor: vscode.TextEditor): void {
  const doc = editor.document;
  if (doc.languageId !== "swift") {
    return;
  }
  if (!containsPreview(doc)) {
    return;
  }

  const file = doc.uri.fsPath;

  // Already previewing this file → no-op.
  if (activeStreams.has(file)) {
    return;
  }

  // No device selected yet → user must run "axe: Show Preview" first.
  if (!lastDevice) {
    return;
  }

  // Replace all existing streams with the new file (avoids unnecessary process restart).
  replaceWithNewStream(file, lastDevice);
}

/** Show device picker and add a stream for the given file. */
async function showDevicePickerAndAddStream(file: string): Promise<void> {
  const device = await pickDevice();
  if (!device) {
    return;
  }
  lastDevice = device;

  // If this file already has a stream, remove it first (replace with new device).
  const existing = activeStreams.get(file);
  if (existing) {
    await untrackStream(existing);
  }

  await addStreamForFile(file, device);
}

/** Handle "Change Device" button from WebView card. */
async function handleChangeDevice(oldStreamId: string): Promise<void> {
  // Find the file for this stream.
  let file: string | undefined;
  for (const [f, sid] of activeStreams) {
    if (sid === oldStreamId) {
      file = f;
      break;
    }
  }
  if (!file) {
    return;
  }

  const device = await pickDevice();
  if (!device) {
    return;
  }
  lastDevice = device;

  await untrackStream(oldStreamId);
  await addStreamForFile(file, device);
}

/**
 * Replace all existing streams with a single new stream.
 * Uses replaceAllStreams to avoid unnecessary process restart during auto-switch.
 */
async function replaceWithNewStream(file: string, device: DeviceSelection): Promise<void> {
  const streamId = generateStreamId();
  const fileName = path.basename(file);

  // Remove old cards from WebView.
  for (const [, sid] of activeStreams) {
    webviewPanel.removeCard(sid);
  }
  activeStreams.clear();

  webviewPanel.resetDismissed();
  webviewPanel.show(() => {
    previewManager.stopPreview();
    activeStreams.clear();
  });
  webviewPanel.addCard(streamId, device.name, fileName);
  activeStreams.set(file, streamId);

  try {
    await previewManager.replaceAllStreams(streamId, file, device.deviceType, device.runtime);
  } catch (err) {
    webviewPanel.removeCard(streamId);
    activeStreams.delete(file);
    vscode.window.showErrorMessage(`Failed to start preview: ${err}`);
  }
}

/** Add a stream for a file with the given device, updating WebView and tracking. */
async function addStreamForFile(file: string, device: DeviceSelection): Promise<void> {
  const streamId = generateStreamId();
  const fileName = path.basename(file);

  webviewPanel.resetDismissed();
  webviewPanel.show(() => {
    // WebView panel closed by user — stop all streams.
    previewManager.stopPreview();
    activeStreams.clear();
  });
  webviewPanel.addCard(streamId, device.name, fileName);
  activeStreams.set(file, streamId);

  try {
    await previewManager.addStream(streamId, file, device.deviceType, device.runtime);
  } catch (err) {
    // Process spawn failed — clean up the ghost card.
    webviewPanel.removeCard(streamId);
    activeStreams.delete(file);
    vscode.window.showErrorMessage(`Failed to start preview: ${err}`);
  }
}

export function deactivate(): void {
  previewManager?.dispose();
  webviewPanel?.dispose();
  statusBar?.dispose();
  activeStreams.clear();
  lastDevice = null;
}
