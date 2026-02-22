import * as assert from "node:assert";
import * as vscode from "vscode";
import { BinaryResolver, type BinaryResolverDeps } from "../binaryResolver";
import type { VersionCheckDeps } from "../versionCheck";

// --- Helpers ---

function createDeps(
	overrides?: Partial<BinaryResolverDeps>,
): BinaryResolverDeps {
	return {
		which: async () => null,
		showPrompt: async () => undefined,
		openSettings: async () => {},
		createTerminal: () =>
			({
				show() {},
				sendText() {},
				dispose() {},
			}) as unknown as vscode.Terminal,
		...overrides,
	};
}

suite("BinaryResolver", () => {
	teardown(async () => {
		// Reset executablePath to default
		await vscode.workspace
			.getConfiguration("axe")
			.update("executablePath", undefined, vscode.ConfigurationTarget.Global);
	});

	suite("resolve()", () => {
		test("returns explicit setting when not default", async () => {
			await vscode.workspace
				.getConfiguration("axe")
				.update(
					"executablePath",
					"/custom/path/axe",
					vscode.ConfigurationTarget.Global,
				);

			const resolver = new BinaryResolver(createDeps());

			const result = await resolver.resolve();
			assert.strictEqual(result, "/custom/path/axe");
		});

		test("returns which result when found in PATH", async () => {
			const deps = createDeps({
				which: async () => "/usr/local/bin/axe",
			});
			const resolver = new BinaryResolver(deps);

			const result = await resolver.resolve();
			assert.strictEqual(result, "/usr/local/bin/axe");
		});

		test("runs install script when user chooses Run Install Script", async () => {
			let terminalName = "";
			let sentText = "";
			const deps = createDeps({
				showPrompt: async () => "Run Install Script",
				createTerminal: (name: string) => {
					terminalName = name;
					return {
						show() {},
						sendText(text: string) {
							sentText = text;
						},
						dispose() {},
					} as unknown as vscode.Terminal;
				},
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(() => resolver.resolve(), {
				message: "axe binary not available",
			});
			assert.strictEqual(terminalName, "axe install");
			assert.ok(sentText.includes("install.sh"));
		});

		test("throws when user dismisses prompt", async () => {
			const deps = createDeps({
				showPrompt: async () => undefined,
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(() => resolver.resolve(), {
				message: "axe binary not available",
			});
		});

		test("opens settings when user chooses Configure Path", async () => {
			let openedSetting = "";
			const deps = createDeps({
				showPrompt: async () => "Configure Path",
				openSettings: async (id) => {
					openedSetting = id;
				},
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(() => resolver.resolve());
			assert.strictEqual(openedSetting, "axe.executablePath");
		});

		test("caches resolved path on subsequent calls", async () => {
			let whichCallCount = 0;
			const deps = createDeps({
				which: async () => {
					whichCallCount++;
					return "/usr/local/bin/axe";
				},
			});
			const resolver = new BinaryResolver(deps);

			await resolver.resolve();
			await resolver.resolve();
			assert.strictEqual(whichCallCount, 1);
		});

		test("priority: explicit setting > which", async () => {
			await vscode.workspace
				.getConfiguration("axe")
				.update(
					"executablePath",
					"/custom/axe",
					vscode.ConfigurationTarget.Global,
				);
			const deps = createDeps({
				which: async () => "/usr/local/bin/axe",
			});
			const resolver = new BinaryResolver(deps);

			const result = await resolver.resolve();
			assert.strictEqual(result, "/custom/axe");
		});
	});

	suite("clearCache()", () => {
		test("forces re-resolution on next resolve call", async () => {
			let whichCallCount = 0;
			const deps = createDeps({
				which: async () => {
					whichCallCount++;
					return "/usr/local/bin/axe";
				},
			});
			const resolver = new BinaryResolver(deps);

			await resolver.resolve();
			assert.strictEqual(whichCallCount, 1);

			resolver.clearCache();
			await resolver.resolve();
			assert.strictEqual(whichCallCount, 2);
		});
	});

	suite("version check integration", () => {
		test("skips version check in dev mode (MIN_CLI_VERSION=0.0.0)", async () => {
			let queriedPath = "";
			const versionCheckDeps: VersionCheckDeps = {
				queryCliVersion: async (path) => {
					queriedPath = path;
					return "1.0.0";
				},
				showWarning: async () => undefined,
			};
			const deps = createDeps({
				which: async () => "/usr/local/bin/axe",
				versionCheck: versionCheckDeps,
			});
			const resolver = new BinaryResolver(deps);

			await resolver.resolve();
			// MIN_CLI_VERSION is "0.0.0" in dev builds, so checkCliVersion
			// returns early without calling queryCliVersion.
			assert.strictEqual(queriedPath, "");
		});

		test("does not block resolve when version check fails", async () => {
			const versionCheckDeps: VersionCheckDeps = {
				queryCliVersion: async () => {
					throw new Error("version query failed");
				},
			};
			const deps = createDeps({
				which: async () => "/usr/local/bin/axe",
				versionCheck: versionCheckDeps,
			});
			const resolver = new BinaryResolver(deps);

			const result = await resolver.resolve();
			assert.strictEqual(result, "/usr/local/bin/axe");
		});
	});
});
