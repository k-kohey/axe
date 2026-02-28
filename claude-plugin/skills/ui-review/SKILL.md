---
name: ui-review
description: Capture a SwiftUI preview and review its UI/UX quality. Checks layout, spacing, HIG compliance, accessibility, and visual consistency. Use when the user asks for UI feedback or wants a design review of a View.
argument-hint: <file.swift> [--preview <title|index>]
allowed-tools: Bash(axe *), Bash(cat *), Read, Glob, Grep
---

# SwiftUI UI/UX Review

Capture a preview screenshot and perform a comprehensive UI/UX review.

## Steps

### 1. Capture the preview

Resolve `$ARGUMENTS` to a `.swift` file path. `$ARGUMENTS` may contain only a View name (e.g. `ContentView`), so use Glob to find the actual path (e.g. `TodoApp/ContentView.swift`).

```bash
axe preview report <path/to/File.swift> --output <output.png>
```

`axe preview report` is preferred over oneshot `axe preview` because it waits for rendering to complete (`--wait`, default 10s) and retries on failure.

If the file has multiple `#Preview` blocks, `--output file.png` will fail (it requires exactly one preview). In that case:
- Use a directory as `--output` to capture all, then review each
- Or fall back to `axe preview --preview <title|index>` (oneshot, no render wait)

Read the captured image with the Read tool.

### 2. Read the source code

Read the SwiftUI source file (resolved in the previous step) to understand the implementation alongside the visual output.

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

## Prerequisites

Run this if the command fails because `axe` is not found::

```bash
curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh
```
