---
name: inspect-view
description: Inspect the view hierarchy of a running simulator app using axe view. Use when debugging layout issues, investigating view structure, or diagnosing UI problems in a running app.
argument-hint: [--app <name>] [0xADDRESS] [--frontmost] [--depth N]
allowed-tools: Bash(axe *), Read, Grep
---

# View Hierarchy Inspector

Inspect the UIKit/SwiftUI view hierarchy of a running app to diagnose layout issues.

## Prerequisites

Before running, verify `axe` is installed:

```bash
command -v axe >/dev/null || { echo "axe is not installed. Install with: curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh"; exit 1; }
```

The `--app` flag or `APP_NAME` in `.axerc` is required for `axe view`. A simulator must be booted with the target app running. Use `axe ps` to list running app processes if needed.

## Steps

### 1. Identify the running app

If the user hasn't specified an app name, list running processes first:

```bash
axe ps
```

### 2. Get the view hierarchy

Pass all user arguments directly to `axe view`:

```bash
axe view $ARGUMENTS
```

Common invocation patterns:
- **Full tree**: `axe view --app MyApp`
- **Frontmost only**: `axe view --app MyApp --frontmost`
- **Limited depth**: `axe view --app MyApp --depth 3`
- **Specific view detail**: `axe view 0x10150e5a0 --app MyApp --swiftui compact`

Note: `--swiftui` (none/compact/full) is only valid in detail mode (when a `0x` address is provided).

### 3. Analyze the hierarchy

Examine the view tree output (YAML format) and identify:
- **Unexpected nesting**: Views wrapped in unnecessary containers
- **Frame issues**: Zero-sized frames, overlapping views, off-screen elements
- **Constraint violations**: Ambiguous layouts or conflicting constraints
- **Hidden views**: Elements with `isHidden: true` or zero alpha
- **SwiftUI structure**: Body composition and modifier chains (when using `--swiftui`)

### 4. Correlate with source code

If the user provides source file context, correlate the hierarchy nodes with the SwiftUI/UIKit source to pinpoint where issues originate.

### 5. Report findings

Present:
1. A simplified view of the relevant hierarchy section
2. Identified issues with their view addresses
3. Suggested code changes to fix layout problems

## Tips

- Use `--depth 3` to limit output for deeply nested hierarchies
- Use `--frontmost` to focus on the visible view controller
- In detail mode output, the SwiftUI tree can be large. Focus on the relevant section.
