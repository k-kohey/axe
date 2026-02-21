package view

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/k-kohey/axe/internal/platform"
)

// fetchHierarchy runs LLDB to fetch the view hierarchy bplist and parses it.
func fetchHierarchy(appName string, device string) (*rawBplistData, error) {
	pythonDir, cleanup, err := platform.ExtractScripts()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	bplistPath := filepath.Join(os.TempDir(), "axe_fetchViewHierarchy.bplist")

	name, err := platform.ResolveAppName(appName)
	if err != nil {
		return nil, err
	}

	pid, err := platform.FindProcess(name, device)
	if err != nil {
		return nil, err
	}

	slog.Info("Attaching to process", "name", name, "pid", pid)

	_, err = platform.RunLLDB(pid, []string{
		fmt.Sprintf("command script import %s/fetch_hierarchy.py", pythonDir),
		fmt.Sprintf("fetch_hierarchy %s", bplistPath),
	})
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(bplistPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to fetch view hierarchy (bplist not created)")
	}

	return parseBplistFile(bplistPath)
}

// extractSnapshot saves imageData as a PNG temp file if valid, and returns the path.
// Returns empty string if imageData is empty, not a valid PNG, or on save error.
func extractSnapshot(node rawViewNode) string {
	if len(node.ImageData) == 0 || !validatePNG(node.ImageData) {
		return ""
	}
	path, err := saveSnapshotToTemp(node.ImageData, node.Address)
	if err != nil {
		slog.Warn("Failed to save snapshot", "error", err)
		return ""
	}
	return path
}

// runSwiftUITreeLLDB runs LLDB to fetch SwiftUI tree JSON for the given address.
// It writes the result to swiftuiJSONPath and returns the LLDB output for error inspection.
func runSwiftUITreeLLDB(appName string, address string, swiftuiJSONPath string, device string) error {
	pythonDir, cleanup, err := platform.ExtractScripts()
	if err != nil {
		return err
	}
	defer cleanup()

	name, err := platform.ResolveAppName(appName)
	if err != nil {
		return err
	}

	pid, err := platform.FindProcess(name, device)
	if err != nil {
		return err
	}

	slog.Info("Fetching SwiftUI tree", "name", name, "pid", pid)
	lldbOut, err := platform.RunLLDB(pid, []string{
		fmt.Sprintf("command script import %s/fetch_swiftui_tree.py", pythonDir),
		fmt.Sprintf("fetch_swiftui_tree %s %s", address, swiftuiJSONPath),
	})
	if err != nil {
		if strings.Contains(lldbOut, "SWIFTUI_VIEW_DEBUG_NOT_SET") {
			return fmt.Errorf("SWIFTUI_VIEW_DEBUG=287 is not set in the target process.\n\n" +
				"Launch the app with the environment variable:\n\n" +
				"  export SIMCTL_CHILD_SWIFTUI_VIEW_DEBUG=287\n" +
				"  xcrun simctl terminate booted <BUNDLE_ID>\n" +
				"  xcrun simctl launch booted <BUNDLE_ID>")
		}
		slog.Debug("LLDB output for SwiftUI tree", "output", lldbOut)
		return fmt.Errorf("failed to fetch SwiftUI tree: %w", err)
	}
	return nil
}

// readSwiftUITreeJSON reads the SwiftUI tree JSON file and parses it into nodes.
// Returns parsed nodes, raw JSON bytes, and any error.
func readSwiftUITreeJSON(jsonPath string, compact bool) ([]SwiftUINode, []byte, error) {
	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read SwiftUI JSON: %w", err)
	}

	if errMsg := extractJSONError(jsonData); errMsg != "" {
		return nil, jsonData, fmt.Errorf("SwiftUI tree error: %s", errMsg)
	}

	nodes, err := ParseSwiftUIJSON(jsonData, compact)
	if err != nil {
		return nil, jsonData, fmt.Errorf("failed to parse SwiftUI JSON: %w", err)
	}

	return nodes, jsonData, nil
}

// fetchSwiftUITree runs LLDB to fetch the SwiftUI tree JSON for the given view address
// and parses it into SwiftUINode trees.
func fetchSwiftUITree(appName string, address string, compact bool, device string) ([]SwiftUINode, []byte, error) {
	swiftuiJSON := filepath.Join(os.TempDir(), "axe_swiftui_tree.json")

	if err := runSwiftUITreeLLDB(appName, address, swiftuiJSON, device); err != nil {
		return nil, nil, err
	}

	return readSwiftUITreeJSON(swiftuiJSON, compact)
}

// buildDetailWithSnapshot builds a detailed UIKitView and extracts a snapshot if available.
func buildDetailWithSnapshot(node rawViewNode, classmap map[string]string, demangled map[string]string) UIKitView {
	detail := buildDetailNode(node, classmap, demangled)
	detail.Snapshot = extractSnapshot(node)
	return detail
}

// RunTree fetches the view hierarchy and returns a TreeOutput.
// If frontmost is true, only the frontmost view controller's subtree is returned.
func RunTree(appName string, maxDepth int, frontmost bool, device string) (TreeOutput, error) {
	var data *rawBplistData

	if frontmost {
		pythonDir, cleanup, err := platform.ExtractScripts()
		if err != nil {
			return TreeOutput{}, err
		}
		defer cleanup()

		bplistPath := filepath.Join(os.TempDir(), "axe_fetchViewHierarchy.bplist")
		frontmostPath := filepath.Join(os.TempDir(), "axe_frontmost_view.txt")

		name, err := platform.ResolveAppName(appName)
		if err != nil {
			return TreeOutput{}, err
		}

		pid, err := platform.FindProcess(name, device)
		if err != nil {
			return TreeOutput{}, err
		}

		slog.Info("Attaching to process", "name", name, "pid", pid)

		_ = os.Remove(frontmostPath)
		_, err = platform.RunLLDB(pid, []string{
			fmt.Sprintf("command script import %s/fetch_hierarchy.py", pythonDir),
			fmt.Sprintf("command script import %s/fetch_frontmost_view.py", pythonDir),
			fmt.Sprintf("fetch_hierarchy %s", bplistPath),
			fmt.Sprintf("fetch_frontmost_view %s", frontmostPath),
		})
		if err != nil {
			return TreeOutput{}, err
		}

		if _, err := os.Stat(bplistPath); os.IsNotExist(err) {
			return TreeOutput{}, fmt.Errorf("failed to fetch view hierarchy (bplist not created)")
		}

		data, err = parseBplistFile(bplistPath)
		if err != nil {
			return TreeOutput{}, err
		}

		if raw, readErr := os.ReadFile(frontmostPath); readErr == nil {
			addr := trimSpace(string(raw))
			if addr != "" {
				node := findNodeByAddress(data.Views, addr)
				if node != nil {
					data.Views = []rawViewNode{*node}
				}
			}
		}
	} else {
		var err error
		data, err = fetchHierarchy(appName, device)
		if err != nil {
			return TreeOutput{}, err
		}
	}

	depth := -1
	if maxDepth > 0 {
		depth = maxDepth
	}

	demangled := demangleNames(collectClassNames(data))
	return buildTree(data, depth, demangled), nil
}

// RunDetail fetches the detail for a specific view address.
// swiftUI should be "none", "compact", or "full".
func RunDetail(appName string, address string, swiftUI string, device string) (DetailOutput, error) {
	bplistPath := filepath.Join(os.TempDir(), "axe_fetchViewHierarchy.bplist")
	cacheMaxAgeMin := 3

	// Check cache: use existing bplist if updated within cacheMaxAgeMin minutes
	needFetch := true
	if _, err := os.Stat(bplistPath); err == nil {
		out, err := exec.Command("find", bplistPath, "-mmin", fmt.Sprintf("-%d", cacheMaxAgeMin)).Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			slog.Debug("Using cached bplist...")
			needFetch = false
		}
	}

	if needFetch {
		pythonDir, cleanup, err := platform.ExtractScripts()
		if err != nil {
			return DetailOutput{}, err
		}
		defer cleanup()

		name, err := platform.ResolveAppName(appName)
		if err != nil {
			return DetailOutput{}, err
		}

		pid, err := platform.FindProcess(name, device)
		if err != nil {
			return DetailOutput{}, err
		}

		slog.Info("Attaching to process", "name", name, "pid", pid)

		_, err = platform.RunLLDB(pid, []string{
			fmt.Sprintf("command script import %s/fetch_hierarchy.py", pythonDir),
			fmt.Sprintf("fetch_hierarchy %s", bplistPath),
		})
		if err != nil {
			return DetailOutput{}, err
		}

		if _, err := os.Stat(bplistPath); os.IsNotExist(err) {
			return DetailOutput{}, fmt.Errorf("failed to fetch view hierarchy (bplist not created)")
		}
	}

	bplistData, err := parseBplistFile(bplistPath)
	if err != nil {
		return DetailOutput{}, err
	}

	node := findNodeByAddress(bplistData.Views, address)
	if node == nil {
		return DetailOutput{}, fmt.Errorf("no view found at address %s", address)
	}

	demangled := demangleNames(collectClassNames(bplistData))
	uikit := buildDetailWithSnapshot(*node, bplistData.Classmap, demangled)
	detail := DetailOutput{UIKit: uikit}

	// Fetch SwiftUI tree in a separate LLDB session to avoid
	// ObjC/Swift language-switch issues within a single session.
	if uikit.IsHostingView && swiftUI != "none" {
		swiftuiJSON := filepath.Join(os.TempDir(), "axe_swiftui_tree.json")
		if err := runSwiftUITreeLLDB(appName, address, swiftuiJSON, device); err != nil {
			slog.Warn("Failed to fetch SwiftUI tree", "error", err)
			fmt.Fprintln(os.Stderr, "\nNote: SwiftUI tree could not be retrieved. _viewDebugData() may not be supported for this view.")
		} else {
			compact := swiftUI == "compact"
			nodes, _, parseErr := readSwiftUITreeJSON(swiftuiJSON, compact)
			if parseErr != nil {
				slog.Warn("SwiftUI tree is not available", "error", parseErr)
				fmt.Fprintln(os.Stderr, "\nNote: SwiftUI tree could not be retrieved. _viewDebugData() may not be supported for this view.")
			} else if len(nodes) > 0 {
				detail.SwiftUI = &SwiftUIOutput{Tree: nodes}
			}
		}
	}

	return detail, nil
}

// extractJSONError checks if data is a JSON object with an "error" key and returns the message.
func extractJSONError(data []byte) string {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	if msg, ok := obj["error"].(string); ok {
		return msg
	}
	return ""
}

func trimSpace(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' {
			result = append(result, s[i])
		}
	}
	return string(result)
}
