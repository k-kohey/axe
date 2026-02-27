package analysis

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

var rePreviewable = regexp.MustCompile(`^\s*@Previewable\s+(.+)$`)

// previewableWrapperRewrites maps @Previewable declarations to wrappers that
// can compile in the generated preview wrapper struct.
// Keep this list in sync with wrappers we support in preview transformation.
var previewableWrapperRewrites = []struct {
	from string
	to   string
}{
	{from: "@Binding", to: "@State"},
	{from: "@FocusState", to: "@State"},
	{from: "@SceneStorage", to: "@State"},
	{from: "@AppStorage", to: "@State"},
}

// SelectPreview selects a preview block by name or 0-based index string.
// If selector is empty, returns the first block.
func SelectPreview(blocks []PreviewBlock, selector string) (PreviewBlock, error) {
	if len(blocks) == 0 {
		return PreviewBlock{}, fmt.Errorf("no #Preview blocks found")
	}

	// Log available previews when multiple exist
	if len(blocks) > 1 {
		for i, b := range blocks {
			if b.Title != "" {
				slog.Info("Found preview", "index", i, "title", b.Title)
			} else {
				slog.Info("Found preview", "index", i, "title", "(unnamed)")
			}
		}
	}

	if selector == "" {
		return blocks[0], nil
	}

	// Try as index first
	if idx, err := strconv.Atoi(selector); err == nil {
		if idx < 0 || idx >= len(blocks) {
			return PreviewBlock{}, fmt.Errorf("preview index %d out of range (0-%d)", idx, len(blocks)-1)
		}
		return blocks[idx], nil
	}

	// Try as title
	for _, b := range blocks {
		if b.Title == selector {
			return b, nil
		}
	}
	return PreviewBlock{}, fmt.Errorf("no preview with title %q found", selector)
}

// TransformPreviewBlock splits a #Preview block into @Previewable property
// declarations and the remaining body source.
//   - Lines matching `@Previewable <decl>` have the prefix stripped and become properties.
//   - Known wrappers are rewritten to preview-safe forms
//     (e.g. @Binding/@FocusState/@SceneStorage/@AppStorage -> @State).
//   - All other lines become the body source.
func TransformPreviewBlock(pb PreviewBlock) TransformedPreview {
	lines := strings.Split(pb.Source, "\n")
	var props []PreviewableProperty
	var bodyLines []string

	for _, line := range lines {
		if m := rePreviewable.FindStringSubmatch(line); m != nil {
			decl := m[1]
			for _, rw := range previewableWrapperRewrites {
				decl = strings.Replace(decl, rw.from, rw.to, 1)
			}
			props = append(props, PreviewableProperty{Source: decl})
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	return TransformedPreview{
		Properties: props,
		BodySource: strings.Join(bodyLines, "\n"),
	}
}
