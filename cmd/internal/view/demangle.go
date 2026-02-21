package view

import (
	"bytes"
	"os/exec"
	"strings"
)

// demangleNames pipes the given names through `swift demangle` and returns
// a map from mangled name to demangled name. Only entries that actually
// changed are included. Returns an empty map on any error (graceful degradation).
func demangleNames(names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}

	path, err := exec.LookPath("swift")
	if err != nil {
		return nil
	}

	input := strings.Join(names, "\n") + "\n"
	cmd := exec.Command(path, "demangle")
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	result := make(map[string]string)
	for i, name := range names {
		if i >= len(lines) {
			break
		}
		demangled := strings.TrimSpace(lines[i])
		if demangled != "" && demangled != name {
			result[name] = demangled
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// collectClassNames extracts all unique class names from rawBplistData
// that may need demangling. This includes:
//   - Class field of every view node (recursively)
//   - Each "/" separated component of classmap keys and values
func collectClassNames(data *rawBplistData) []string {
	seen := make(map[string]struct{})

	var walkNodes func(nodes []rawViewNode)
	walkNodes = func(nodes []rawViewNode) {
		for _, n := range nodes {
			if n.Class != "" {
				seen[n.Class] = struct{}{}
			}
			if n.Layer != nil {
				if cls := n.Layer["class"]; cls != "" {
					seen[cls] = struct{}{}
				}
			}
			walkNodes(n.Subviews)
		}
	}
	walkNodes(data.Views)

	for k, v := range data.Classmap {
		seen[k] = struct{}{}
		for _, part := range strings.Split(v, "/") {
			if part != "" {
				seen[part] = struct{}{}
			}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}
