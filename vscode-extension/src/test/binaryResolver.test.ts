import * as assert from "node:assert";
import * as vscode from "vscode";
import {
	BinaryResolver,
	type BinaryResolverDeps,
	UserActionPendingError,
} from "../binaryResolver";
import { runInstallScript } from "../installScript";
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
		waitForTerminalClose: async () => {},
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

		test("waits for terminal close and retries which after install", async () => {
			let whichCallCount = 0;
			let terminalName = "";
			let sentText = "";
			const mockTerminal = {
				show() {},
				sendText(text: string) {
					sentText = text;
				},
				dispose() {},
			} as unknown as vscode.Terminal;

			const deps = createDeps({
				which: async () => {
					whichCallCount++;
					// First call: not found. Second call (retry after install): found.
					return whichCallCount >= 2 ? "/home/user/.local/bin/axe" : null;
				},
				showPrompt: async () => "Run Install Script",
				createTerminal: (name: string) => {
					terminalName = name;
					return mockTerminal;
				},
				waitForTerminalClose: async (terminal) => {
					assert.strictEqual(terminal, mockTerminal);
				},
			});
			const resolver = new BinaryResolver(deps);

			const result = await resolver.resolve();
			assert.strictEqual(result, "/home/user/.local/bin/axe");
			assert.strictEqual(terminalName, "axe install");
			assert.ok(sentText.includes("install.sh"));
			assert.strictEqual(whichCallCount, 2);
		});

		test("throws error when binary not found after installation", async () => {
			const deps = createDeps({
				showPrompt: async () => "Run Install Script",
				waitForTerminalClose: async () => {},
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(
				() => resolver.resolve(),
				(err: unknown) =>
					err instanceof Error &&
					!(err instanceof UserActionPendingError) &&
					(err as Error).message.includes("not found after installation"),
			);
		});

		test("install script sends ; exit so terminal closes after install", () => {
			let sentText = "";
			const terminal = runInstallScript((name) => {
				assert.strictEqual(name, "axe install");
				return {
					show() {},
					sendText(text: string) {
						sentText = text;
					},
					dispose() {},
				} as unknown as vscode.Terminal;
			});
			assert.ok(terminal);
			assert.ok(
				sentText.endsWith("; exit"),
				`Expected sendText to end with '; exit', got: ${sentText}`,
			);
		});

		test("throws when user dismisses prompt", async () => {
			const deps = createDeps({
				showPrompt: async () => undefined,
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(
				() => resolver.resolve(),
				(err: unknown) =>
					err instanceof Error && !(err instanceof UserActionPendingError),
			);
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

			await assert.rejects(
				() => resolver.resolve(),
				(err: unknown) => err instanceof UserActionPendingError,
			);
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

	suite("concurrent resolve()", () => {
		test("parallel calls show only one prompt and share the same rejection", async () => {
			let promptCount = 0;
			const deps = createDeps({
				showPrompt: async () => {
					promptCount++;
					return undefined;
				},
			});
			const resolver = new BinaryResolver(deps);

			const results = await Promise.allSettled([
				resolver.resolve(),
				resolver.resolve(),
				resolver.resolve(),
			]);

			assert.strictEqual(promptCount, 1);
			for (const r of results) {
				assert.strictEqual(r.status, "rejected");
			}
			// All callers receive the exact same Error instance
			const reasons = results.map((r) => (r as PromiseRejectedResult).reason);
			assert.strictEqual(reasons[0], reasons[1]);
			assert.strictEqual(reasons[1], reasons[2]);
		});

		test("parallel calls share install retry result", async () => {
			let whichCallCount = 0;
			let promptCount = 0;
			const deps = createDeps({
				which: async () => {
					whichCallCount++;
					// First call: not found. Second call (retry after install): found.
					return whichCallCount >= 2 ? "/home/user/.local/bin/axe" : null;
				},
				showPrompt: async () => {
					promptCount++;
					return "Run Install Script";
				},
				waitForTerminalClose: async () => {},
			});
			const resolver = new BinaryResolver(deps);

			const [r1, r2] = await Promise.all([
				resolver.resolve(),
				resolver.resolve(),
			]);

			assert.strictEqual(promptCount, 1);
			assert.strictEqual(r1, "/home/user/.local/bin/axe");
			assert.strictEqual(r2, "/home/user/.local/bin/axe");
		});

		test("failure clears inflight so next call retries", async () => {
			let callCount = 0;
			const deps = createDeps({
				which: async () => {
					callCount++;
					return callCount >= 2 ? "/usr/local/bin/axe" : null;
				},
				showPrompt: async () => undefined, // dismiss prompt → throws
			});
			const resolver = new BinaryResolver(deps);

			await assert.rejects(() => resolver.resolve());
			// After failure, inflight should be cleared; next call starts fresh
			const result = await resolver.resolve();
			assert.strictEqual(result, "/usr/local/bin/axe");
		});

		test("clearCache() during inflight prevents stale cache", async () => {
			let whichCallCount = 0;
			const pending: {
				resolve: (v: string | null) => void;
			} = { resolve: () => {} };
			const deps = createDeps({
				which: () => {
					whichCallCount++;
					return new Promise<string | null>((r) => {
						pending.resolve = r;
					});
				},
			});
			const resolver = new BinaryResolver(deps);

			const resolvePromise = resolver.resolve();
			assert.strictEqual(whichCallCount, 1);

			// clearCache() while resolve is in-flight
			resolver.clearCache();

			// Let the first which() resolve with a path
			pending.resolve("/usr/local/bin/axe");
			const result = await resolvePromise;

			assert.strictEqual(result, "/usr/local/bin/axe");
			// cachedPath should NOT be set because epoch changed.
			// A second resolve() must trigger a fresh which() call.
			resolver.resolve(); // starts a new doResolve → new which()
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
