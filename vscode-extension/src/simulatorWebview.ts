import * as vscode from "vscode";
import { InputMessage } from "./previewManager";

export { InputMessage };

/** Messages sent from WebView to Extension. */
export interface WebViewMessage {
  type: string;
  streamId?: string;
  [key: string]: unknown;
}

export interface SimulatorWebviewDeps {
  createWebviewPanel?: (
    viewType: string,
    title: string,
    showOptions: vscode.ViewColumn,
    options?: vscode.WebviewPanelOptions & vscode.WebviewOptions
  ) => vscode.WebviewPanel;
  getWebviewHtml?: () => string;
}

export class SimulatorWebviewPanel {
  private panel: vscode.WebviewPanel | null = null;
  private createPanel: NonNullable<SimulatorWebviewDeps["createWebviewPanel"]>;
  private getHtml: NonNullable<SimulatorWebviewDeps["getWebviewHtml"]>;
  private dismissed = false;
  private onInput?: (msg: InputMessage) => void;
  private onWebViewMessage?: (msg: WebViewMessage) => void;

  constructor(deps?: SimulatorWebviewDeps) {
    this.createPanel =
      deps?.createWebviewPanel ??
      ((viewType, title, showOptions, options) =>
        vscode.window.createWebviewPanel(viewType, title, showOptions, options));
    this.getHtml = deps?.getWebviewHtml ?? (() => "");
  }

  setInputHandler(handler: (msg: InputMessage) => void): void {
    this.onInput = handler;
  }

  setWebViewMessageHandler(handler: (msg: WebViewMessage) => void): void {
    this.onWebViewMessage = handler;
  }

  show(onDispose?: () => void): void {
    if (this.dismissed || this.panel) {
      return;
    }
    this.panel = this.createPanel(
      "axe.simulatorPreview",
      "axe Preview",
      vscode.ViewColumn.Beside,
      { enableScripts: true }
    );
    this.panel.webview.html = this.getHtml();
    this.panel.webview.onDidReceiveMessage((msg: WebViewMessage) => {
      // Route touch/text input events to the input handler.
      if (msg.type === "touchDown" || msg.type === "touchMove" || msg.type === "touchUp" || msg.type === "text") {
        this.onInput?.(msg as InputMessage);
      }
      // Route all messages to the generic handler (for removeStream, changeDevice, nextPreview).
      this.onWebViewMessage?.(msg);
    });
    this.panel.onDidDispose(() => {
      this.panel = null;
      this.dismissed = true;
      onDispose?.();
    });
  }

  resetDismissed(): void {
    this.dismissed = false;
  }

  /** Add a new card to the grid. */
  addCard(streamId: string, deviceName: string, fileName: string): void {
    this.panel?.webview.postMessage({ type: "addCard", streamId, deviceName, fileName });
  }

  /** Remove a card from the grid. */
  removeCard(streamId: string): void {
    this.panel?.webview.postMessage({ type: "removeCard", streamId });
  }

  /** Update a card's preview frame. */
  postFrame(streamId: string, base64: string): void {
    this.panel?.webview.postMessage({ type: "frame", streamId, data: base64 });
  }

  /** Update a card's status overlay. */
  postStatus(streamId: string, phase: string): void {
    this.panel?.webview.postMessage({ type: "status", streamId, phase });
  }

  /** Show the Next button on a card (when previewCount > 1). */
  showNextButton(streamId: string): void {
    this.panel?.webview.postMessage({ type: "showNextButton", streamId });
  }

  dispose(): void {
    this.panel?.dispose();
    this.panel = null;
  }

  get visible(): boolean {
    return this.panel !== null;
  }
}
