# axe

**Alternative Xcode Environment** — SwiftUI live preview with hot-reload, and view hierarchy inspection, all from the command line.

![demo](docs/demo.gif)

## Features

- **SwiftUI Live Preview** — `@_dynamicReplacement` based hot-reload. Only the changed view is recompiled into a dylib and injected at runtime — no full rebuild required.
- **View Hierarchy Inspection** — UIKit/SwiftUI view tree dump via LLDB, with an interactive TUI browser and per-view PNG snapshots.
- **VS Code / Cursor Extension** — Open a Swift file with `#Preview` and the preview starts automatically. See [vscode-extension/README.md](vscode-extension/README.md).

## Requirements

- macOS (Apple Silicon)
- Xcode (command-line tools)
- [`idb_companion`](https://github.com/facebook/idb) — for headless simulator management

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh
```

The installer also installs `idb_companion` via Homebrew if not present. To install it manually:

```bash
brew install facebook/fb/idb-companion
```

Or download a binary from the [Releases](https://github.com/k-kohey/axe/releases) page.

## Quick Start

Create a `.axerc` in your project root so you don't have to pass flags every time:

```
PROJECT=MyApp.xcodeproj
SCHEME=MyApp
```

Then preview any Swift file containing `#Preview`:

```bash
axe preview MyView.swift --watch
```

## Usage

### `axe preview`

Launch a SwiftUI preview on a headless iOS Simulator.

```bash
axe preview <source-file.swift> [flags]
```

| Flag | Description |
|---|---|
| `--project` | Path to `.xcodeproj` |
| `--workspace` | Path to `.xcworkspace` (mutually exclusive with `--project`) |
| `--scheme` | Xcode scheme to build (required) |
| `--watch` | Watch for file changes and hot-reload |
| `--preview` | Select a `#Preview` block by title or index (e.g. `--preview "Dark Mode"` or `--preview 1`) |
| `--device` | Simulator UDID to use |
| `--configuration` | Build configuration (e.g. `Debug`) |
| `--reuse-build` | Skip xcodebuild and reuse previous build artifacts |
| `--serve` | Run as IDE backend (JSON Lines protocol on stdin/stdout) |

All flags fall back to `.axerc` values when not specified.

#### Simulator Management

axe manages its own isolated simulator device set, separate from your normal simulators.

```bash
# List managed simulators
axe preview simulator list

# List available device types and runtimes
axe preview simulator list --available

# Add a simulator
axe preview simulator add \
  --device-type com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro \
  --runtime com.apple.CoreSimulator.SimRuntime.iOS-18-2

# Set the default simulator
axe preview simulator default <udid>

# Remove a simulator
axe preview simulator remove <udid>
```

### `axe view`

Inspect the UIKit view hierarchy of a running app on a simulator.

```bash
axe view [0xADDRESS] [flags]
```

**Tree mode** (default):

```bash
# Full view hierarchy
axe view

# Frontmost view controller only
axe view --frontmost

# Limit depth
axe view --depth 3
```

**Detail mode** (with address):

```bash
# Detailed info + PNG snapshot for a specific view
axe view 0x10150e5a0

# Include SwiftUI tree
axe view 0x10150e5a0 --swiftui compact
```

**Interactive mode**:

```bash
axe view -i
```

| Flag | Description |
|---|---|
| `--depth` | Maximum tree depth to display |
| `--frontmost` | Show only the frontmost view controller's subtree |
| `--swiftui` | SwiftUI tree mode: `none`, `compact`, `full` (detail mode only) |
| `-i`, `--interactive` | Interactive TUI navigation |
| `--simulator` | Target simulator by UDID or name |

### Global Flags

| Flag | Description |
|---|---|
| `--app` | Target app process name (overrides `.axerc`) |
| `-v`, `--verbose` | Verbose output |

## VS Code Extension

A VS Code / Cursor extension that runs `axe preview` automatically when you open a Swift file containing `#Preview`.

Download the `.vsix` from the [Releases](https://github.com/k-kohey/axe/releases) page:

- **VS Code**: `code --install-extension axe-swiftui-preview-<version>.vsix`
- **Cursor**: `Cmd+Shift+P` > "Install from VSIX..."

See [vscode-extension/README.md](vscode-extension/README.md) for configuration and development details.

## Configuration (`.axerc`)

Place a `.axerc` file in your project root. Flags specified on the command line take precedence.

```
PROJECT=MyApp.xcodeproj
SCHEME=MyApp
CONFIGURATION=Debug
DEVICE=<simulator-udid>
```

## Known Issues

### Hot Reload (`preview --watch`)

- **Stored properties cannot be hot-reloaded**: `let`, `@State`, `@Published` etc. change memory layout and automatically trigger a full rebuild. Computed properties and methods are hot-reloaded.
- **Generic/static/class methods and initializers are not hot-reloaded**.
- **Transitive dependency changes are not detected**: Only direct dependencies are watched. Re-save the target file to force a reload.

### Source Parsing

- **Indirect protocol conformance is not detected**: `struct Foo: MyProtocol` where `MyProtocol: View` is not recognized. Direct `: View` and `extension`-based conformance are supported.

### Preview Macro

- **`#Preview(traits:)` is not supported**: Display trait parameters are ignored.

### Platform

- **Apple Silicon only**: The preview compiler targets `arm64` exclusively.

## Contributing

Contributions are welcome! Bug reports, feature requests, and pull requests are all appreciated.

```bash
mise install   # Set up dependencies
mise run test  # Run tests
mise run check # Lint
```

Running `mise install` sets up the full development environment.
