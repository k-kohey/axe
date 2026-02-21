package preview

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

var rePreviewable = regexp.MustCompile(`^\s*@Previewable\s+(.+)$`)

// selectPreview selects a preview block by name or 0-based index string.
// If selector is empty, returns the first block.
func selectPreview(blocks []previewBlock, selector string) (previewBlock, error) {
	if len(blocks) == 0 {
		return previewBlock{}, fmt.Errorf("no #Preview blocks found")
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
			return previewBlock{}, fmt.Errorf("preview index %d out of range (0-%d)", idx, len(blocks)-1)
		}
		return blocks[idx], nil
	}

	// Try as title
	for _, b := range blocks {
		if b.Title == selector {
			return b, nil
		}
	}
	return previewBlock{}, fmt.Errorf("no preview with title %q found", selector)
}

// transformPreviewBlock splits a #Preview block into @Previewable property
// declarations and the remaining body source.
// - Lines matching `@Previewable <decl>` have the prefix stripped and become properties.
// - `@Binding` in those declarations is replaced with `@State` (since $x gives Binding access).
// - All other lines become the body source.
func transformPreviewBlock(pb previewBlock) transformedPreview {
	lines := strings.Split(pb.Source, "\n")
	var props []previewableProperty
	var bodyLines []string

	for _, line := range lines {
		if m := rePreviewable.FindStringSubmatch(line); m != nil {
			decl := m[1]
			// @Binding → @State (in a preview wrapper, $x provides Binding)
			decl = strings.Replace(decl, "@Binding", "@State", 1)
			props = append(props, previewableProperty{Source: decl})
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	return transformedPreview{
		Properties: props,
		BodySource: strings.Join(bodyLines, "\n"),
	}
}
