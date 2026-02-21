package preview

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// thunkFuncMap provides helper functions for thunk templates.
var thunkFuncMap = template.FuncMap{
	// topLevelName extracts the top-level type name from a potentially qualified name.
	// e.g. "HelloView.HogeView" → "HelloView", "SimpleView" → "SimpleView"
	"topLevelName": func(name string) string {
		if i := strings.Index(name, "."); i >= 0 {
			return name[:i]
		}
		return name
	},
	// escapeSwiftString escapes backslashes and double quotes for use inside Swift string literals.
	"escapeSwiftString": func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	},
	// isView returns true if the typeInfo conforms to View.
	"isView": func(t typeInfo) bool {
		return t.isView()
	},
}

// thunkTmpl generates a combined thunk for one or more source files.
// Each file gets its own @_private import to access private members used in bodies.
// Files with private type name collisions must be excluded before calling this.
// Only the target file's #Preview is used for the preview wrapper.
var thunkTmpl = template.Must(template.New("thunk").Funcs(thunkFuncMap).Parse(
	`{{ range .Files }}@_private(sourceFile: "{{ .FileName | escapeSwiftString }}") import {{ $.ModuleName }}
{{ end }}import SwiftUI
{{ range .ExtraImports }}{{ . }}
{{ end }}
{{ range .Files }}{{ $filePath := .AbsPath }}{{ range .Types }}extension {{ .Name }} {
{{ range .Properties }}    @_dynamicReplacement(for: {{ .Name }}) private var __preview__{{ .Name }}: {{ .TypeExpr }} {
        #sourceLocation(file: "{{ $filePath | escapeSwiftString }}", line: {{ .BodyLine }})
{{ .Source }}
        #sourceLocation()
    }
{{ end }}{{ range .Methods }}    @_dynamicReplacement(for: {{ .Selector }})
    private func __preview__{{ .Name }}{{ .Signature }} {
        #sourceLocation(file: "{{ $filePath | escapeSwiftString }}", line: {{ .BodyLine }})
{{ .Source }}
        #sourceLocation()
    }
{{ end }}}
{{ end }}{{ end }}
{{ if .HasPreview }}
{{/* TODO: topLevelName may produce duplicate imports when nested views share a parent (e.g. OuterView and OuterView.InnerView both emit "import struct Module.OuterView"). Swift tolerates duplicates, but deduplication would be cleaner. */}}
{{ range .TargetTypes }}{{ if isView . }}import struct {{ $.ModuleName }}.{{ topLevelName .Name }}
{{ end }}{{ end }}
struct _AxePreviewWrapper: View {
{{ range .PreviewProps }}    {{ .Source }}
{{ end }}
    var body: some View {
{{ .PreviewBody }}
    }
}
{{ end }}
import UIKit

@_cdecl("axe_preview_refresh")
public func _axePreviewRefresh() {
{{ if .HasPreview }}
    let hc = UIHostingController(rootView: AnyView(_AxePreviewWrapper()))
    for scene in UIApplication.shared.connectedScenes {
        guard let ws = scene as? UIWindowScene else { continue }
        guard let window = ws.windows.first else { continue }
        window.rootViewController = hc
        window.makeKeyAndVisible()
        break
    }
{{ end }}
}
`))

type thunkTemplateData struct {
	Files        []fileThunkData
	ModuleName   string
	ExtraImports []string
	HasPreview   bool
	TargetTypes  []typeInfo
	PreviewProps []previewableProperty
	PreviewBody  string
}

// generateCombinedThunk generates a single thunk.swift covering multiple source files.
// The targetSourceFile is the file whose #Preview is used.
func generateCombinedThunk(
	files []fileThunkData,
	moduleName string,
	dirs previewDirs,
	previewSelector string,
	targetSourceFile string,
) (retPath string, retErr error) {
	slog.Debug("Generating combined thunk")

	if err := os.MkdirAll(dirs.Thunk, 0o755); err != nil {
		return "", fmt.Errorf("creating thunk dir: %w", err)
	}

	// Collect unique extra imports from all files.
	importSet := make(map[string]bool)
	for _, f := range files {
		for _, imp := range f.Imports {
			importSet[imp] = true
		}
	}
	var extraImports []string
	for imp := range importSet {
		extraImports = append(extraImports, imp)
	}

	// Find target types for the preview wrapper import.
	var targetTypes []typeInfo
	for _, f := range files {
		if f.AbsPath == targetSourceFile {
			targetTypes = f.Types
			break
		}
	}

	td := thunkTemplateData{
		Files:        files,
		ModuleName:   moduleName,
		ExtraImports: extraImports,
		TargetTypes:  targetTypes,
	}

	// Parse #Preview blocks from the target source file.
	previews, err := parsePreviewBlocks(targetSourceFile)
	if err != nil {
		slog.Warn("Failed to parse #Preview blocks", "err", err)
	}

	if len(previews) > 0 {
		selected, err := selectPreview(previews, previewSelector)
		if err != nil {
			return "", err
		}
		tp := transformPreviewBlock(selected)
		td.HasPreview = true
		td.PreviewProps = tp.Properties
		td.PreviewBody = tp.BodySource
	}

	thunkPath := filepath.Join(dirs.Thunk, "thunk.swift")
	f, err := os.Create(thunkPath)
	if err != nil {
		return "", fmt.Errorf("creating thunk file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("closing thunk file: %w", cerr)
		}
	}()

	if err := thunkTmpl.Execute(f, td); err != nil {
		return "", fmt.Errorf("executing thunk template: %w", err)
	}

	slog.Debug("Generated combined thunk", "path", thunkPath, "files", len(files))
	return thunkPath, nil
}
