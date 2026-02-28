---
name: preview
description: Capture a SwiftUI preview screenshot using axe CLI. Use when the user wants to see the current appearance of a SwiftUI View, check layout, or verify changes visually.
argument-hint: <file.swift> [--preview <title|index>] [--reuse-build] [--wait <duration>]
allowed-tools: Bash(axe *), Bash(cat *), Read
---

# SwiftUI Preview Capture

Capture a screenshot of a SwiftUI View's `#Preview` block using `axe preview report` (preferred) or `axe preview`.

## Steps

### Default: Use `axe preview report`

`axe preview report` is preferred because it waits for rendering to complete (`--wait`, default 10s) and retries on failure. Use this unless you need to select a specific preview from multiple `#Preview` blocks.

1. Run `axe preview report` to capture the preview as a PNG image:

```bash
axe preview report $ARGUMENTS --output <output.png>
```

2. Display the captured image using the Read tool.
   - If the file contains multiple `#Preview` blocks, `--output` must be a directory. In that case, each screenshot is saved as `<basename>--preview-<index>.png`. Display all captured images.

3. Describe what you see in the preview to the user.

### Fallback: Use `axe preview` (oneshot)

Use `axe preview` when you need to select a specific preview from a file that contains multiple `#Preview` blocks, via `--preview <title|index>`.

Note: oneshot mode captures immediately after the app signals readiness, with no rendering delay. This may result in a blank screenshot for views that require time to render.

```bash
axe preview $ARGUMENTS > <output.png>
```

## Options

- `--wait <duration>` — Rendering delay before screenshot capture (default 10s, `report` only). Reduce for simple views, increase for complex ones.
- `--reuse-build` — Skip xcodebuild and reuse artifacts from a previous build. Useful when capturing multiple previews in succession or when only the View source changed.
- `--preview <title|index>` — Select a specific preview by title or index (oneshot only).

## Notes

- If the command fails, read `$ERR_LOG` and report the error to the user. Common causes:
  - The file does not contain a `#Preview` block
  - Missing `.axerc` or `--scheme` not specified
  - `axe` or `idb_companion` not installed
- The screenshot reflects the current state of the source code on disk. If the user has unsaved edits, remind them to save first.
- Clean up generated screenshot and log files after they are no longer needed.

## Prerequisites

Run this if the command fails because `axe` is not found::

```bash
curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh
```
