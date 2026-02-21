import * as assert from "assert";
import { buildArgs, AxeConfig } from "../config";

suite("buildArgs", () => {
  const defaultConfig: AxeConfig = {
    executablePath: "axe",
    project: "",
    workspace: "",
    scheme: "",
    configuration: "",
    additionalArgs: [],
    previewDevice: "",
  };

  test("minimal args with default config", () => {
    const args = buildArgs("/path/to/File.swift", defaultConfig);
    assert.deepStrictEqual(args, [
      "preview",
      "/path/to/File.swift",
      "--watch",
      "--serve",
    ]);
  });

  test("includes --project when set", () => {
    const config = { ...defaultConfig, project: "App.xcodeproj" };
    const args = buildArgs("/path/to/File.swift", config);
    assert.ok(args.includes("--project"));
    assert.strictEqual(args[args.indexOf("--project") + 1], "App.xcodeproj");
  });

  test("includes --workspace when set", () => {
    const config = { ...defaultConfig, workspace: "App.xcworkspace" };
    const args = buildArgs("/path/to/File.swift", config);
    assert.ok(args.includes("--workspace"));
    assert.strictEqual(
      args[args.indexOf("--workspace") + 1],
      "App.xcworkspace"
    );
  });

  test("includes --scheme when set", () => {
    const config = { ...defaultConfig, scheme: "MyScheme" };
    const args = buildArgs("/path/to/File.swift", config);
    assert.ok(args.includes("--scheme"));
    assert.strictEqual(args[args.indexOf("--scheme") + 1], "MyScheme");
  });

  test("includes --configuration when set", () => {
    const config = { ...defaultConfig, configuration: "Debug" };
    const args = buildArgs("/path/to/File.swift", config);
    assert.ok(args.includes("--configuration"));
    assert.strictEqual(args[args.indexOf("--configuration") + 1], "Debug");
  });

  test("appends additionalArgs", () => {
    const config = {
      ...defaultConfig,
      additionalArgs: ["--verbose", "--no-color"],
    };
    const args = buildArgs("/path/to/File.swift", config);
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
      previewDevice: "",
    };
    const args = buildArgs("/src/View.swift", config);
    assert.deepStrictEqual(args, [
      "preview",
      "/src/View.swift",
      "--watch",
      "--project",
      "App.xcodeproj",
      "--scheme",
      "App",
      "--configuration",
      "Debug",
      "--serve",
      "--extra",
    ]);
  });

  test("includes --device when previewDevice is set", () => {
    const config = { ...defaultConfig, previewDevice: "ABCD-1234-UDID" };
    const args = buildArgs("/path/to/File.swift", config);
    assert.ok(args.includes("--device"));
    assert.strictEqual(args[args.indexOf("--device") + 1], "ABCD-1234-UDID");
    // --device should come before --serve
    assert.ok(args.indexOf("--device") < args.indexOf("--serve"));
  });

  test("always includes --serve", () => {
    const args = buildArgs("/path/to/File.swift", defaultConfig);
    assert.ok(args.includes("--serve"));
  });

  test("empty strings are not passed as flags", () => {
    const args = buildArgs("/path/to/File.swift", defaultConfig);
    assert.ok(!args.includes("--project"));
    assert.ok(!args.includes("--workspace"));
    assert.ok(!args.includes("--scheme"));
    assert.ok(!args.includes("--configuration"));
  });
});
