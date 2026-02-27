import * as assert from "node:assert";
import * as fs from "node:fs";
import * as path from "node:path";
import {
	type InputMessage,
	type SimulatorWebviewDeps,
	SimulatorWebviewPanel,
	type WebViewMessage,
} from "../simulatorWebview";

interface FakeWebviewPanel {
	viewType: string;
	title: string;
	showOptions: unknown;
	options: unknown;
	webview: {
		html: string;
		messages: unknown[];
		postMessage(message: unknown): Thenable<boolean>;
		onDidReceiveMessage(listener: (msg: unknown) => void): { dispose(): void };
		_messageListeners: ((msg: unknown) => void)[];
	};
	disposed: boolean;
	onDidDispose(listener: () => void): { dispose(): void };
	dispose(): void;
	_disposeListeners: (() => void)[];
}

function createFakePanel(): FakeWebviewPanel {
	const disposeListeners: (() => void)[] = [];
	const messages: unknown[] = [];
	const messageListeners: ((msg: unknown) => void)[] = [];
	return {
		viewType: "",
		title: "",
		showOptions: undefined,
		options: undefined,
		webview: {
			html: "",
			messages,
			_messageListeners: messageListeners,
			postMessage(message: unknown): Thenable<boolean> {
				messages.push(message);
				return Promise.resolve(true);
			},
			onDidReceiveMessage(listener: (msg: unknown) => void) {
				messageListeners.push(listener);
				return { dispose() {} };
			},
		},
		disposed: false,
		_disposeListeners: disposeListeners,
		onDidDispose(listener: () => void) {
			disposeListeners.push(listener);
			return { dispose() {} };
		},
		dispose() {
			this.disposed = true;
			for (const l of disposeListeners) {
				l();
			}
		},
	};
}

const htmlPath = path.resolve(__dirname, "..", "..", "media", "simulator.html");

function createDeps(panel: FakeWebviewPanel): SimulatorWebviewDeps {
	return {
		createWebviewPanel: (viewType, title, showOptions, options) => {
			panel.viewType = viewType;
			panel.title = title;
			panel.showOptions = showOptions;
			panel.options = options;
			return panel as unknown as import("vscode").WebviewPanel;
		},
		getWebviewHtml: () => fs.readFileSync(htmlPath, "utf-8"),
	};
}

suite("SimulatorWebviewPanel", () => {
	test("show creates a webview panel", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();

		assert.strictEqual(fakePanel.viewType, "axe.simulatorPreview");
		assert.strictEqual(fakePanel.title, "axe Preview");
		assert.ok(fakePanel.webview.html.includes("grid"));
		assert.strictEqual(webview.visible, true);
	});

	test("show is idempotent", () => {
		let createCount = 0;
		const fakePanel = createFakePanel();
		const deps: SimulatorWebviewDeps = {
			createWebviewPanel: (viewType, title, showOptions, options) => {
				createCount++;
				fakePanel.viewType = viewType;
				fakePanel.title = title;
				fakePanel.showOptions = showOptions;
				fakePanel.options = options;
				return fakePanel as unknown as import("vscode").WebviewPanel;
			},
			getWebviewHtml: () => fs.readFileSync(htmlPath, "utf-8"),
		};
		const webview = new SimulatorWebviewPanel(deps);

		webview.show();
		webview.show();

		assert.strictEqual(createCount, 1);
	});

	test("addCard sends addCard message to webview", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.addCard("stream-a", "iPhone 16 Pro", "HogeView.swift");

		assert.deepStrictEqual(fakePanel.webview.messages, [
			{
				type: "addCard",
				streamId: "stream-a",
				deviceName: "iPhone 16 Pro",
				fileName: "HogeView.swift",
			},
		]);
	});

	test("removeCard sends removeCard message to webview", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.removeCard("stream-a");

		assert.deepStrictEqual(fakePanel.webview.messages, [
			{ type: "removeCard", streamId: "stream-a" },
		]);
	});

	test("postFrame sends frame message with streamId", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.postFrame("stream-a", "AAAA");

		assert.deepStrictEqual(fakePanel.webview.messages, [
			{ type: "frame", streamId: "stream-a", data: "AAAA" },
		]);
	});

	test("postFrame is no-op when panel is not shown", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.postFrame("stream-a", "AAAA");
		assert.strictEqual(fakePanel.webview.messages.length, 0);
	});

	test("postStatus sends status message with streamId", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.postStatus("stream-a", "building");

		assert.deepStrictEqual(fakePanel.webview.messages, [
			{ type: "status", streamId: "stream-a", phase: "building" },
		]);
	});

	test("dispose closes the panel", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.dispose();

		assert.strictEqual(fakePanel.disposed, true);
		assert.strictEqual(webview.visible, false);
	});

	test("dispose is safe when no panel exists", () => {
		const webview = new SimulatorWebviewPanel({
			createWebviewPanel: () =>
				createFakePanel() as unknown as import("vscode").WebviewPanel,
			getWebviewHtml: () => fs.readFileSync(htmlPath, "utf-8"),
		});

		webview.dispose();
		assert.strictEqual(webview.visible, false);
	});

	test("onDispose callback fires when panel is closed externally", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		let disposed = false;
		webview.show(() => {
			disposed = true;
		});

		fakePanel.dispose();

		assert.strictEqual(disposed, true);
		assert.strictEqual(webview.visible, false);
	});

	test("show is suppressed after user closes panel", () => {
		let createCount = 0;
		const panels: FakeWebviewPanel[] = [];
		const deps: SimulatorWebviewDeps = {
			createWebviewPanel: () => {
				createCount++;
				const p = createFakePanel();
				panels.push(p);
				return p as unknown as import("vscode").WebviewPanel;
			},
			getWebviewHtml: () => fs.readFileSync(htmlPath, "utf-8"),
		};
		const webview = new SimulatorWebviewPanel(deps);

		webview.show();
		panels[0].dispose();
		webview.show();

		assert.strictEqual(createCount, 1);
		assert.strictEqual(webview.visible, false);
	});

	test("resetDismissed allows show after user close", () => {
		let createCount = 0;
		const panels: FakeWebviewPanel[] = [];
		const deps: SimulatorWebviewDeps = {
			createWebviewPanel: () => {
				createCount++;
				const p = createFakePanel();
				panels.push(p);
				return p as unknown as import("vscode").WebviewPanel;
			},
			getWebviewHtml: () => fs.readFileSync(htmlPath, "utf-8"),
		};
		const webview = new SimulatorWebviewPanel(deps);

		webview.show();
		panels[0].dispose();
		webview.resetDismissed();
		webview.show();

		assert.strictEqual(createCount, 2);
		assert.strictEqual(webview.visible, true);
	});

	test("setInputHandler receives touchDown messages", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		const received: InputMessage[] = [];
		webview.setInputHandler((msg) => {
			received.push(msg);
		});

		webview.show();

		const touchDownMsg = {
			type: "touchDown",
			streamId: "stream-a",
			x: 0.5,
			y: 0.3,
		};
		for (const listener of fakePanel.webview._messageListeners) {
			listener(touchDownMsg);
		}

		assert.strictEqual(received.length, 1);
		assert.strictEqual(received[0].type, "touchDown");
	});

	test("setInputHandler receives text messages", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		const received: InputMessage[] = [];
		webview.setInputHandler((msg) => {
			received.push(msg);
		});

		webview.show();

		const textMsg = { type: "text", streamId: "stream-a", value: "a" };
		for (const listener of fakePanel.webview._messageListeners) {
			listener(textMsg);
		}

		assert.strictEqual(received.length, 1);
		assert.strictEqual(received[0].type, "text");
	});

	test("setWebViewMessageHandler receives removeStream messages", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		const received: WebViewMessage[] = [];
		webview.setWebViewMessageHandler((msg) => {
			received.push(msg);
		});

		webview.show();

		const removeMsg = { type: "removeStream", streamId: "stream-a" };
		for (const listener of fakePanel.webview._messageListeners) {
			listener(removeMsg);
		}

		assert.strictEqual(received.length, 1);
		assert.strictEqual(received[0].type, "removeStream");
		assert.strictEqual(received[0].streamId, "stream-a");
	});

	test("setWebViewMessageHandler receives changeDevice messages", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		const received: WebViewMessage[] = [];
		webview.setWebViewMessageHandler((msg) => {
			received.push(msg);
		});

		webview.show();

		const changeMsg = { type: "changeDevice", streamId: "stream-b" };
		for (const listener of fakePanel.webview._messageListeners) {
			listener(changeMsg);
		}

		assert.strictEqual(received.length, 1);
		assert.strictEqual(received[0].type, "changeDevice");
		assert.strictEqual(received[0].streamId, "stream-b");
	});

	test("showNextButton sends showNextButton message to webview", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();
		webview.showNextButton("stream-a");

		assert.deepStrictEqual(fakePanel.webview.messages, [
			{ type: "showNextButton", streamId: "stream-a" },
		]);
	});

	test("showNextButton is no-op when panel is not shown", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.showNextButton("stream-a");
		assert.strictEqual(fakePanel.webview.messages.length, 0);
	});

	test("HTML includes multi-card grid layout", () => {
		const fakePanel = createFakePanel();
		const webview = new SimulatorWebviewPanel(createDeps(fakePanel));

		webview.show();

		assert.ok(fakePanel.webview.html.includes("grid"));
		assert.ok(fakePanel.webview.html.includes("addCard"));
		assert.ok(fakePanel.webview.html.includes("removeCard"));
		assert.ok(fakePanel.webview.html.includes("data-stream-id"));
		assert.ok(fakePanel.webview.html.includes("mousedown"));
		assert.ok(fakePanel.webview.html.includes("touchDown"));
		assert.ok(fakePanel.webview.html.includes("removeStream"));
		assert.ok(fakePanel.webview.html.includes("changeDevice"));
		assert.ok(fakePanel.webview.html.includes("showNextButton"));
	});
});
