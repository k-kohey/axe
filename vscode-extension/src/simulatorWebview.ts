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
}

export class SimulatorWebviewPanel {
  private panel: vscode.WebviewPanel | null = null;
  private createPanel: NonNullable<SimulatorWebviewDeps["createWebviewPanel"]>;
  private dismissed = false;
  private onInput?: (msg: InputMessage) => void;
  private onWebViewMessage?: (msg: WebViewMessage) => void;

  constructor(deps?: SimulatorWebviewDeps) {
    this.createPanel =
      deps?.createWebviewPanel ??
      ((viewType, title, showOptions, options) =>
        vscode.window.createWebviewPanel(viewType, title, showOptions, options));
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
    this.panel.webview.html = getWebviewHtml();
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

function getWebviewHtml(): string {
  return `<!DOCTYPE html>
<html>
<head>
  <style>
    * { box-sizing: border-box; }
    body {
      margin: 0; padding: 8px;
      background: #1e1e1e; color: #ccc;
      font-family: -apple-system, BlinkMacSystemFont, sans-serif;
      font-size: 12px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
      gap: 12px;
    }
    .card {
      background: #2d2d2d; border-radius: 8px;
      overflow: hidden; display: flex; flex-direction: column;
    }
    .card .preview-container {
      position: relative; background: #000;
      display: flex; justify-content: center; align-items: center;
      min-height: 200px;
    }
    .card img {
      max-width: 100%; max-height: 60vh; object-fit: contain;
      user-select: none; -webkit-user-drag: none;
    }
    .card .status-overlay {
      position: absolute; inset: 0;
      display: flex; justify-content: center; align-items: center;
      color: #aaa; font-size: 14px;
    }
    .card .card-info {
      padding: 8px 12px; display: flex; flex-direction: column; gap: 2px;
    }
    .card .device-name { font-weight: 600; color: #eee; }
    .card .file-name { color: #888; font-size: 11px; }
    .card .card-actions {
      padding: 4px 12px 8px; display: flex; gap: 8px;
    }
    .card .card-actions button {
      background: #3c3c3c; border: none; color: #ccc;
      padding: 4px 8px; border-radius: 4px; cursor: pointer; font-size: 11px;
    }
    .card .card-actions button:hover { background: #4c4c4c; }
    .card .card-actions .btn-remove { margin-left: auto; color: #e88; }
  </style>
</head>
<body>
  <div class="grid" id="grid"></div>
  <script>
    const vscode = acquireVsCodeApi();
    const grid = document.getElementById('grid');

    // Track per-card drag state.
    const dragStates = {};

    window.addEventListener('message', (e) => {
      const msg = e.data;
      switch (msg.type) {
        case 'addCard': {
          if (document.querySelector('[data-stream-id="' + msg.streamId + '"]')) return;
          const card = document.createElement('div');
          card.className = 'card';
          card.dataset.streamId = msg.streamId;

          const previewContainer = document.createElement('div');
          previewContainer.className = 'preview-container';
          const img = document.createElement('img');
          img.className = 'preview-image';
          const overlay = document.createElement('div');
          overlay.className = 'status-overlay';
          overlay.textContent = 'Initializing...';
          previewContainer.appendChild(img);
          previewContainer.appendChild(overlay);

          const cardInfo = document.createElement('div');
          cardInfo.className = 'card-info';
          const deviceSpan = document.createElement('span');
          deviceSpan.className = 'device-name';
          deviceSpan.textContent = msg.deviceName || '';
          const fileSpan = document.createElement('span');
          fileSpan.className = 'file-name';
          fileSpan.textContent = msg.fileName || '';
          cardInfo.appendChild(deviceSpan);
          cardInfo.appendChild(fileSpan);

          const cardActions = document.createElement('div');
          cardActions.className = 'card-actions';
          const btnDevice = document.createElement('button');
          btnDevice.className = 'btn-device';
          btnDevice.textContent = 'Device';
          const btnNext = document.createElement('button');
          btnNext.className = 'btn-next';
          // Hidden by default; shown via showNextButton message when previewCount > 1.
          btnNext.style.display = 'none';
          btnNext.textContent = 'Next';
          const btnRemove = document.createElement('button');
          btnRemove.className = 'btn-remove';
          btnRemove.textContent = '\u00d7';
          cardActions.appendChild(btnDevice);
          cardActions.appendChild(btnNext);
          cardActions.appendChild(btnRemove);

          card.appendChild(previewContainer);
          card.appendChild(cardInfo);
          card.appendChild(cardActions);
          grid.appendChild(card);
          setupCardEvents(card, msg.streamId);
          break;
        }
        case 'removeCard': {
          const el = document.querySelector('[data-stream-id="' + msg.streamId + '"]');
          if (el) el.remove();
          break;
        }
        case 'frame': {
          const img = document.querySelector('[data-stream-id="' + msg.streamId + '"] .preview-image');
          if (img) {
            img.src = 'data:image/jpeg;base64,' + msg.data;
            const overlay = document.querySelector('[data-stream-id="' + msg.streamId + '"] .status-overlay');
            if (overlay) overlay.style.display = 'none';
          }
          break;
        }
        case 'status': {
          const overlay = document.querySelector('[data-stream-id="' + msg.streamId + '"] .status-overlay');
          if (overlay) {
            overlay.textContent = msg.phase;
            overlay.style.display = 'flex';
          }
          break;
        }
        case 'showNextButton': {
          const btn = document.querySelector('[data-stream-id="' + msg.streamId + '"] .btn-next');
          if (btn) btn.style.display = '';
          break;
        }
      }
    });

    function setupCardEvents(card, streamId) {
      const img = card.querySelector('.preview-image');

      // Touch events with streamId.
      img.addEventListener('mousedown', (e) => {
        e.preventDefault();
        const rect = img.getBoundingClientRect();
        dragStates[streamId] = { moveScheduled: false };
        const x = (e.clientX - rect.left) / rect.width;
        const y = (e.clientY - rect.top) / rect.height;
        vscode.postMessage({ type: 'touchDown', streamId, x, y });
      });
      img.addEventListener('mousemove', (e) => {
        const state = dragStates[streamId];
        if (!state || state.moveScheduled) return;
        state.moveScheduled = true;
        const rect = img.getBoundingClientRect();
        const x = (e.clientX - rect.left) / rect.width;
        const y = (e.clientY - rect.top) / rect.height;
        requestAnimationFrame(() => {
          vscode.postMessage({ type: 'touchMove', streamId, x, y });
          state.moveScheduled = false;
        });
      });
      img.addEventListener('mouseup', (e) => {
        if (!dragStates[streamId]) return;
        const rect = img.getBoundingClientRect();
        const x = (e.clientX - rect.left) / rect.width;
        const y = (e.clientY - rect.top) / rect.height;
        vscode.postMessage({ type: 'touchUp', streamId, x, y });
        delete dragStates[streamId];
      });
      img.addEventListener('mouseleave', (e) => {
        if (!dragStates[streamId]) return;
        const rect = img.getBoundingClientRect();
        const x = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
        const y = Math.max(0, Math.min(1, (e.clientY - rect.top) / rect.height));
        vscode.postMessage({ type: 'touchUp', streamId, x, y });
        delete dragStates[streamId];
      });

      // Action buttons.
      card.querySelector('.btn-remove').addEventListener('click', () => {
        vscode.postMessage({ type: 'removeStream', streamId });
      });
      card.querySelector('.btn-device').addEventListener('click', () => {
        vscode.postMessage({ type: 'changeDevice', streamId });
      });
      card.querySelector('.btn-next').addEventListener('click', () => {
        vscode.postMessage({ type: 'nextPreview', streamId });
      });
    }

    // Global keypress → send to all cards.
    // Future improvement: track which card has focus and send input only to that card.
    // Currently broadcasts to all streams because per-card focus management is out of scope.
    document.addEventListener('keypress', (e) => {
      if (e.key.length === 1) {
        const cards = document.querySelectorAll('.card');
        cards.forEach(card => {
          vscode.postMessage({ type: 'text', streamId: card.dataset.streamId, value: e.key });
        });
      }
    });
  </script>
</body>
</html>`;
}
