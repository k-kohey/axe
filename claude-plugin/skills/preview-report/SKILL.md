---
name: preview-report
description: Capture previews of multiple SwiftUI files and generate a visual report. Use when the user wants to see all previews at once, create PR documentation, or batch-check UI across files.
argument-hint: <file1.swift> [file2.swift...]
disable-model-invocation: true
allowed-tools: Bash(axe *), Bash(mktemp *), Bash(git diff *), Read, Glob, Grep
---

# Batch Preview Report

Capture screenshots of all `#Preview` blocks across multiple SwiftUI files and generate a report.

## Prerequisites

Before running, verify `axe` is installed:

```bash
command -v axe >/dev/null || { echo "axe is not installed. Install with: curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh"; exit 1; }
```

The project must have a valid `.axerc` (with `PROJECT` or `WORKSPACE` and `SCHEME`) or the user must pass `--scheme`/`--project`/`--workspace` flags.

## Steps

### 1. Determine target files

If `$ARGUMENTS` specifies files, use those directly.

If no arguments are provided, detect changed Swift files:

```bash
FILES=$(git diff --name-only --diff-filter=ACMR HEAD -- '*.swift')
if [ -z "$FILES" ]; then
  echo "No changed Swift files found. Specify files explicitly."
  exit 1
fi
```

### 2. Generate the report

```bash
REPORT_DIR=$(mktemp -d /tmp/axe-report-XXXXXX)
ERR_LOG=$(mktemp /tmp/axe-report-XXXXXX.log)
if ! axe preview report $FILES --format md --output "$REPORT_DIR" 2>"$ERR_LOG"; then
  cat "$ERR_LOG"
  exit 1
fi
```

When `$ARGUMENTS` is provided, use `$ARGUMENTS` instead of `$FILES`:

```bash
axe preview report $ARGUMENTS --format md --output "$REPORT_DIR" 2>"$ERR_LOG"
```

This generates:
- `axe_swiftui_preview_report.md` — Markdown report with embedded image references
- `axe_swiftui_preview_report_assets/*.png` — Individual preview screenshots

### 3. Display the results

Read the generated Markdown report with the Read tool. Then read each preview image in the assets directory to show them to the user.

### 4. Summarize

Provide a summary of all captured previews, noting:
- Total number of `#Preview` blocks found
- Any previews that failed to capture (and likely reasons from stderr)
- Quick observations about the UI state of each view

## Options

- Use `-j <n>` to control parallel simulator count for faster capture
- Use `--reuse-build` to skip rebuilding if the project was recently built
- Replace `--format md` with `--format html` for an interactive HTML report with lightbox

## Use Cases

- **PR review**: Capture all changed views before creating a pull request
- **Visual regression**: Compare previews across branches
- **Documentation**: Generate a visual catalog of UI components
