---
name: preview
description: Capture a SwiftUI preview screenshot using axe CLI. Use when the user wants to see the current appearance of a SwiftUI View, check layout, or verify changes visually.
argument-hint: <file.swift> [--preview <title|index>]
allowed-tools: Bash(axe *), Bash(mktemp *), Read
---

# SwiftUI Preview Capture

Capture a screenshot of a SwiftUI View's `#Preview` block using `axe preview`.

## Prerequisites

Before running, verify `axe` is installed:

```bash
command -v axe >/dev/null || { echo "axe is not installed. Install with: curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh"; exit 1; }
```

The project must have a valid `.axerc` (with `PROJECT` or `WORKSPACE` and `SCHEME`) or the user must pass `--scheme`/`--project`/`--workspace` flags.

## Steps

1. Run `axe preview` to capture the preview as a PNG image:

```bash
PREVIEW_IMG=$(mktemp /tmp/axe-preview-XXXXXX.png)
ERR_LOG=$(mktemp /tmp/axe-preview-XXXXXX.log)
if ! axe preview $ARGUMENTS > "$PREVIEW_IMG" 2>"$ERR_LOG"; then
  cat "$ERR_LOG"
  exit 1
fi
```

2. Display the captured image using the Read tool on `$PREVIEW_IMG`.

3. Describe what you see in the preview to the user.

## Notes

- If the command fails, read `$ERR_LOG` and report the error to the user. Common causes:
  - The file does not contain a `#Preview` block
  - Missing `.axerc` or `--scheme` not specified
  - `axe` or `idb_companion` not installed
- If multiple `#Preview` blocks exist, use `--preview <title|index>` to select one
- The screenshot reflects the current state of the source code on disk. If the user has unsaved edits, remind them to save first.
