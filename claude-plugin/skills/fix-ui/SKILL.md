---
name: fix-ui
description: Iteratively fix a SwiftUI View to match a design image. Captures previews with axe, compares against the target design, and modifies the code until the UI matches. Use when the user provides a design image and wants the View adjusted to match it.
argument-hint: <file.swift> <design-image-path>
disable-model-invocation: true
allowed-tools: Bash(axe *), Bash(cat *), Read, Edit, Glob, Grep
---

# Design-Driven UI Fix Loop

Iteratively modify a SwiftUI View until its preview matches a target design image.

## Inputs

- `$0`: Path to the SwiftUI source file
- `$1`: Path to the design image (PNG, JPG, etc.)

## Procedure

### 1. Read the design target

Read the design image at `$1` using the Read tool to understand the target appearance.

### 2. Read the source code

Read the SwiftUI source file at `$0` to understand the current implementation.

### 3. Capture the current preview

```bash
axe preview report "$0" --output <output.png>
```

`axe preview report` is preferred because it waits for rendering to complete and retries on failure.

If the file has multiple `#Preview` blocks, `--output file.png` will fail (it requires exactly one preview). In that case:
- Use a directory as `--output` to capture all, then pick the relevant one
- Or fall back to `axe preview "$0" --preview <title|index>` (oneshot, no render wait)

Read the captured image to see the current appearance.

### 4. Compare and identify differences

Compare the current preview against the design image. Focus on:
- Layout structure (VStack/HStack/ZStack arrangement)
- Spacing and padding
- Font sizes and weights
- Colors and backgrounds
- Corner radius and borders
- Image sizing and aspect ratios
- Text content and alignment

### 5. Edit the source code

Make targeted edits to the SwiftUI source file to address the identified differences. Prioritize the most visually impactful changes first.

### 6. Re-capture and verify

Capture a new preview after the edits. Use `--reuse-build` to skip rebuilding since only the View source changed:

```bash
axe preview report "$0" --output <output.png> --reuse-build
```

Read the new image and compare against the design.

### 7. Iterate

Repeat steps 4-6 until the preview closely matches the design image. Stop after at most 5 iterations. If the design still does not match, report remaining differences to the user.

### 8. Report

Show the final preview image and summarize all changes made to the source file.

## Guidelines

- Make minimal, focused changes per iteration
- Prefer SwiftUI modifiers over restructuring the view hierarchy
- If the file has multiple `#Preview` blocks, use `--preview <title|index>` to target the correct one
- If the design requires assets (images, custom fonts) not present in the project, note this to the user
- If the design cannot be perfectly replicated due to platform constraints, explain the limitations

## Prerequisites

Run this if the command fails because `axe` is not found::

```bash
curl -fsSL https://raw.githubusercontent.com/k-kohey/axe/main/install.sh | sh
```
