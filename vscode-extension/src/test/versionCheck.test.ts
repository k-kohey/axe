import * as assert from "node:assert";
import { checkCliVersion, compareSemver } from "../versionCheck";

suite("versionCheck", () => {
	suite("compareSemver()", () => {
		test("returns 0 for equal versions", () => {
			assert.strictEqual(compareSemver("1.2.3", "1.2.3"), 0);
		});

		test("returns negative when a < b (major)", () => {
			assert.ok(compareSemver("0.9.9", "1.0.0") < 0);
		});

		test("returns negative when a < b (minor)", () => {
			assert.ok(compareSemver("1.0.9", "1.1.0") < 0);
		});

		test("returns negative when a < b (patch)", () => {
			assert.ok(compareSemver("1.1.0", "1.1.1") < 0);
		});

		test("returns positive when a > b", () => {
			assert.ok(compareSemver("2.0.0", "1.9.9") > 0);
		});

		test("handles single-digit components", () => {
			assert.strictEqual(compareSemver("0.0.0", "0.0.0"), 0);
		});
	});

	suite("checkCliVersion()", () => {
		test("skips check when MIN_CLI_VERSION is 0.0.0 (dev mode)", async () => {
			let warned = false;
			await checkCliVersion("/dummy/axe", {
				queryCliVersion: async () => "0.0.1",
				showWarning: async () => {
					warned = true;
					return undefined;
				},
			});
			// MIN_CLI_VERSION = "0.0.0" in dev → early return
			assert.strictEqual(warned, false);
		});

		test("does not throw when version query fails", async () => {
			await assert.doesNotReject(
				checkCliVersion("/nonexistent/axe", {
					queryCliVersion: async () => {
						throw new Error("command not found");
					},
				}),
			);
		});

		// Note: testing the warning + terminal flow requires MIN_CLI_VERSION !== "0.0.0",
		// which only happens in CI-built releases. The compareSemver logic is unit-tested above.
	});
});
