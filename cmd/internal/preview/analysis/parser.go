package analysis

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

var rePreviewable = regexp.MustCompile(`^\s*@Previewable\s+(.+)$`)

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

// previewableWrapperReplacements maps property wrappers used with @Previewable
// to their preview-compatible replacements.
// @Previewable は Preview 本体内でプロパティラッパーを宣言するための SwiftUI マクロ。
// 実際のプレビュー実行時には standalone な宣言に変換する必要があるため、
// 各ラッパーを適切な代替に置換する。
//
// NOTE: SwiftUI に新しい property wrapper が追加された場合はここに追記する。
// 本質的には SwiftUI コンパイラプラグインの変換ロジックと一致させる必要があるが、
// そのインターフェースは非公開のため、既知のラッパーを手動で列挙する暫定対応。
var previewableWrapperReplacements = []struct {
	from string
	to   string
}{
	{"@Binding", "@State"},
	{"@FocusState", "@State"},
	{"@SceneStorage", "@State"},
	{"@AppStorage", "@State"},
}

// TransformPreviewBlock splits a #Preview block into @Previewable property
// declarations and the remaining body source.
//   - Lines matching `@Previewable <decl>` have the prefix stripped and become properties.
//   - Property wrappers incompatible with standalone declarations are replaced
//     (e.g. @Binding → @State, @FocusState → @State).
//   - All other lines become the body source.
func TransformPreviewBlock(pb PreviewBlock) TransformedPreview {
	lines := strings.Split(pb.Source, "\n")
	var props []PreviewableProperty
	var bodyLines []string

	for _, line := range lines {
		if m := rePreviewable.FindStringSubmatch(line); m != nil {
			decl := m[1]
			for _, r := range previewableWrapperReplacements {
				decl = strings.Replace(decl, r.from, r.to, 1)
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
