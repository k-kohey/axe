---
name: ui-review
description: Capture a SwiftUI preview and review its UI/UX quality. Checks layout, spacing, HIG compliance, accessibility, and visual consistency. Use when the user asks for UI feedback or wants a design review of a View.
argument-hint: <file.swift> [--preview <title|index>]
allowed-tools: Bash(axe *), Bash(cat *), Read, Glob, Grep
---

# SwiftUI UI/UX Review

Capture a preview screenshot and perform a comprehensive UI/UX review.

## Prerequisites

Before running, verify `axe` is installed:

```bash
command -v axe >/dev/null || { echo "axe is not installed. Install with: curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh"; exit 1; }
```

The project must have a valid `.axerc` (with `PROJECT` or `WORKSPACE` and `SCHEME`) or the user must pass `--scheme`/`--project`/`--workspace` flags.

## Steps

### 1. Capture the preview

```bash
PREVIEW_IMG="$(pwd)/axe-ui-review-$(date +%s)-$$.png"
ERR_LOG="$(pwd)/axe-ui-review-err-$(date +%s)-$$.log"
if ! axe preview report $ARGUMENTS --output "$PREVIEW_IMG" 2>"$ERR_LOG"; then
  cat "$ERR_LOG"
  # If it failed because of multiple #Preview blocks, fall back to directory output or oneshot
  exit 1
fi
```

`axe preview report` is preferred over oneshot `axe preview` because it waits for rendering to complete (`--wait`, default 10s) and retries on failure.

If the file has multiple `#Preview` blocks, `--output file.png` will fail (it requires exactly one preview). In that case:
- Use a directory as `--output` to capture all, then review each
- Or fall back to `axe preview --preview <title|index>` (oneshot, no render wait)

Read the captured image with the Read tool.

### 2. Read the source code

Extract the file path from `$ARGUMENTS` (the first positional argument) and read the SwiftUI source file to understand the implementation alongside the visual output.

### 3. Review the UI

Evaluate the preview image and source code against these criteria:

#### Layout & Spacing
- Consistent padding and margins
- Proper alignment of elements
- No clipped or overflowing content
- Appropriate use of spacing between elements

#### Typography
- Readable font sizes (minimum 11pt for body text)
- Proper font weight hierarchy (title > headline > body)
- Text truncation handled gracefully

#### Color & Contrast
- Sufficient contrast ratios for text readability
- Consistent color usage across elements
- Check source code for semantic colors (`.foregroundStyle` / `.background`) vs hardcoded values for Dark Mode support

#### Apple HIG Compliance
- Standard navigation patterns
- Appropriate use of system components
- Touch target sizes (minimum 44x44pt)
- Safe area handling

#### Accessibility (source code check)
- Dynamic Type support (look for fixed font sizes vs `.font(.body)` etc.)
- VoiceOver considerations (`.accessibilityLabel`, `.accessibilityHint`)
- Color-only information indicators (should have alternative cues)

### 4. Report findings

Present findings organized by severity:
1. **Issues**: Problems that should be fixed (layout bugs, readability issues, HIG violations)
2. **Suggestions**: Improvements that would enhance quality
3. **Good**: Aspects that are well-implemented

For each finding, reference the specific line in the source code and suggest a concrete fix.
