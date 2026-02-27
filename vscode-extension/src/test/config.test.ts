import * as assert from "node:assert";
import { type AxeConfig, buildArgs } from "../config";

suite("buildArgs", () => {
	const defaultConfig: AxeConfig = {
		executablePath: "axe",
		project: "",
		workspace: "",
		scheme: "",
		configuration: "",
		additionalArgs: [],
	};

	test("serve mode args with default config", () => {
		const args = buildArgs(defaultConfig);
		assert.deepStrictEqual(args, ["preview", "serve"]);
	});

	test("includes --project when set", () => {
		const config = { ...defaultConfig, project: "App.xcodeproj" };
		const args = buildArgs(config);
		assert.ok(args.includes("--project"));
		assert.strictEqual(args[args.indexOf("--project") + 1], "App.xcodeproj");
	});

	test("includes --workspace when set", () => {
		const config = { ...defaultConfig, workspace: "App.xcworkspace" };
		const args = buildArgs(config);
		assert.ok(args.includes("--workspace"));
		assert.strictEqual(
			args[args.indexOf("--workspace") + 1],
			"App.xcworkspace",
		);
	});

	test("includes --scheme when set", () => {
		const config = { ...defaultConfig, scheme: "MyScheme" };
		const args = buildArgs(config);
		assert.ok(args.includes("--scheme"));
		assert.strictEqual(args[args.indexOf("--scheme") + 1], "MyScheme");
	});

	test("includes --configuration when set", () => {
		const config = { ...defaultConfig, configuration: "Debug" };
		const args = buildArgs(config);
		assert.ok(args.includes("--configuration"));
		assert.strictEqual(args[args.indexOf("--configuration") + 1], "Debug");
	});

	test("appends additionalArgs", () => {
		const config = {
			...defaultConfig,
			additionalArgs: ["--verbose", "--no-color"],
		};
		const args = buildArgs(config);
		assert.ok(args.includes("--verbose"));
		assert.ok(args.includes("--no-color"));
	});

	test("all flags combined", () => {
		const config: AxeConfig = {
			executablePath: "axe",
			project: "App.xcodeproj",
			workspace: "",
			scheme: "App",
			configuration: "Debug",
			additionalArgs: ["--extra"],
		};
		const args = buildArgs(config);
		assert.deepStrictEqual(args, [
			"preview",
			"serve",
			"--project",
			"App.xcodeproj",
			"--scheme",
			"App",
			"--configuration",
			"Debug",
			"--extra",
		]);
	});

	test("always includes serve subcommand", () => {
		const args = buildArgs(defaultConfig);
		assert.ok(args.includes("serve"));
		assert.ok(!args.includes("--serve"));
		assert.ok(!args.includes("--watch"));
	});

	test("empty strings are not passed as flags", () => {
		const args = buildArgs(defaultConfig);
		assert.ok(!args.includes("--project"));
		assert.ok(!args.includes("--workspace"));
		assert.ok(!args.includes("--scheme"));
		assert.ok(!args.includes("--configuration"));
	});
});
