package view

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/rivo/tview"
)

var spinChars = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// buildTreeNodeLabel builds the display label for a tree node.
func buildTreeNodeLabel(node rawViewNode, classmap map[string]string, demangled map[string]string) string {
	var b strings.Builder
	b.WriteString(demangleClass(node.Class, demangled))

	if isHostingView(node, classmap) {
		b.WriteString(" ★")
	}

	if frame := buildRect(node.Frame); frame != nil {
		fmt.Fprintf(&b, " [gray]%.0fx%.0f[-]", frame.Width, frame.Height)
	}

	return b.String()
}

// buildTreeNodes recursively converts rawViewNodes to tview TreeNodes.
func buildTreeNodes(nodes []rawViewNode, classmap map[string]string, demangled map[string]string) []*tview.TreeNode {
	result := make([]*tview.TreeNode, len(nodes))
	for i := range nodes {
		label := buildTreeNodeLabel(nodes[i], classmap, demangled)
		tn := tview.NewTreeNode(label).
			SetReference(&nodes[i]).
			SetExpanded(true).
			SetSelectable(true)
		for _, child := range buildTreeNodes(nodes[i].Subviews, classmap, demangled) {
			tn.AddChild(child)
		}
		result[i] = tn
	}
	return result
}

// startSpinner runs a spinner animation on the given TextView until done is closed.
func startSpinner(app *tview.Application, tv *tview.TextView, done <-chan struct{}) {
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			default:
			}
			ch := spinChars[i%len(spinChars)]
			app.QueueUpdateDraw(func() {
				tv.SetText(fmt.Sprintf("\n   %c Fetching view hierarchy...", ch))
			})
			i++
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// resolveAddress returns the class name for an address from addrMap,
// or a shortened address if not found.
func resolveAddress(addr string, addrMap map[string]string) string {
	if addrMap != nil {
		if name, ok := addrMap[addr]; ok {
			return name
		}
	}
	// Shorten long hex addresses: "0x104b1dd50" → "0x…1dd50"
	if len(addr) > 7 && strings.HasPrefix(addr, "0x") {
		return "0x…" + addr[len(addr)-5:]
	}
	return addr
}

// formatConstraint formats a single constraint with human-readable names and simplified output.
func formatConstraint(c Constraint, addrMap map[string]string) string {
	var b strings.Builder

	firstLabel := resolveAddress(c.FirstItem, addrMap)
	fmt.Fprintf(&b, "  [cyan]%s[-].%s", firstLabel, c.FirstAttribute)

	fmt.Fprintf(&b, " %s ", c.Relation)

	// Constant-only constraint: secondItem is nil/unknown and secondAttribute is notAnAttribute
	isConstant := (c.SecondItem == "0x0" || c.SecondItem == "?") && c.SecondAttribute == "notAnAttribute"

	if isConstant {
		b.WriteString(formatNumber(c.Constant))
	} else {
		secondLabel := resolveAddress(c.SecondItem, addrMap)
		fmt.Fprintf(&b, "[green]%s[-].%s", secondLabel, c.SecondAttribute)
		if c.Multiplier != 1.0 {
			fmt.Fprintf(&b, " * %s", formatNumber(c.Multiplier))
		}
		if c.Constant != 0.0 {
			if c.Constant > 0 {
				fmt.Fprintf(&b, " + %s", formatNumber(c.Constant))
			} else {
				fmt.Fprintf(&b, " - %s", formatNumber(-c.Constant))
			}
		}
	}

	if c.Priority != 1000 {
		fmt.Fprintf(&b, "  (priority: %.0f)", c.Priority)
	}

	return b.String()
}

// formatNumber formats a float64 as an integer if it has no fractional part, otherwise with one decimal.
func formatNumber(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// renderDetailText converts a UIKitView to colored key-value text for the TUI detail pane.
func renderDetailText(detail UIKitView, addrMap map[string]string) string {
	var b strings.Builder
	label := "[yellow]"
	reset := "[-]"

	fmt.Fprintf(&b, "%sClass:%s        %s\n", label, reset, detail.Class)
	fmt.Fprintf(&b, "%sAddress:%s      %s\n", label, reset, detail.Address)

	if detail.Inheritance != "" {
		fmt.Fprintf(&b, "%sInheritance:%s  %s\n", label, reset, detail.Inheritance)
	}

	if detail.Frame != nil {
		fmt.Fprintf(&b, "%sFrame:%s        (%.0f, %.0f) %.0fx%.0f\n",
			label, reset, detail.Frame.X, detail.Frame.Y, detail.Frame.Width, detail.Frame.Height)
	}

	if detail.Bounds != nil {
		fmt.Fprintf(&b, "%sBounds:%s       (%.0f, %.0f) %.0fx%.0f\n",
			label, reset, detail.Bounds.X, detail.Bounds.Y, detail.Bounds.Width, detail.Bounds.Height)
	}

	if detail.Position != nil {
		fmt.Fprintf(&b, "%sPosition:%s     (%.0f, %.0f)\n",
			label, reset, detail.Position.X, detail.Position.Y)
	}

	if detail.Hidden != nil {
		fmt.Fprintf(&b, "%sHidden:%s       %v\n", label, reset, *detail.Hidden)
	}

	if detail.LayoutMargins != nil {
		fmt.Fprintf(&b, "%sLayoutMargins:%s top=%.0f left=%.0f bottom=%.0f right=%.0f\n",
			label, reset, detail.LayoutMargins.Top, detail.LayoutMargins.Left,
			detail.LayoutMargins.Bottom, detail.LayoutMargins.Right)
	}

	if detail.HasAmbiguousLayout != nil {
		fmt.Fprintf(&b, "%sAmbiguousLayout:%s %v\n", label, reset, *detail.HasAmbiguousLayout)
	}

	if detail.Layer != nil {
		fmt.Fprintf(&b, "%sLayer:%s        %s (%s)\n", label, reset, detail.Layer.Class, detail.Layer.Address)
	}

	if detail.IsHostingView {
		fmt.Fprintf(&b, "%sHostingView:%s  true\n", label, reset)
	}

	if detail.Snapshot != "" {
		fmt.Fprintf(&b, "%sSnapshot:%s     %s\n", label, reset, detail.Snapshot)
	}

	if detail.SubviewCount != nil {
		fmt.Fprintf(&b, "%sSubviews:%s     %d\n", label, reset, *detail.SubviewCount)
	}

	if len(detail.Constraints) > 0 {
		fmt.Fprintf(&b, "%sConstraints:%s  %d\n", label, reset, len(detail.Constraints))
		for _, c := range detail.Constraints {
			b.WriteString(formatConstraint(c, addrMap))
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// renderSwiftUIText renders SwiftUINode trees as a tree with box-drawing characters.
// prefix is the inherited line prefix from parent nodes (e.g. "  │   ").
func renderSwiftUIText(nodes []SwiftUINode, prefix string) string {
	var b strings.Builder
	for i, node := range nodes {
		isLast := i == len(nodes)-1

		// Branch connector
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		b.WriteString(prefix)
		b.WriteString(connector)
		b.WriteString(node.Name)
		if node.Value != "" {
			fmt.Fprintf(&b, " %q", node.Value)
		}
		if node.Size != nil {
			fmt.Fprintf(&b, "  %.0fx%.0f", node.Size.Width, node.Size.Height)
		}
		b.WriteString("\n")

		if len(node.Children) > 0 {
			childPrefix := prefix + "│   "
			if isLast {
				childPrefix = prefix + "    "
			}
			b.WriteString(renderSwiftUIText(node.Children, childPrefix))
		}
	}
	return b.String()
}

var yamlKeyRe = regexp.MustCompile(`^(\s*(?:- )?)(\w+)(:)`)

// highlightYAML adds tview color tags to YAML text for syntax highlighting.
func highlightYAML(yamlText string) string {
	lines := strings.Split(yamlText, "\n")
	var b strings.Builder
	for _, line := range lines {
		highlighted := yamlKeyRe.ReplaceAllString(line, "$1[yellow]$2[-]$3")
		b.WriteString(highlighted)
		b.WriteByte('\n')
	}
	return b.String()
}

// flattenTreeNodes recursively flattens TreeNodes into lines for fzf.
// Each line is "address\tindent+label" where indent uses tree-drawing characters.
func flattenTreeNodes(node *tview.TreeNode, depth int) []string {
	var result []string
	if ref, ok := node.GetReference().(*rawViewNode); ok && ref != nil {
		indent := strings.Repeat("  ", depth)
		label := node.GetText()
		line := fmt.Sprintf("%s\t%s%s", ref.Address, indent, label)
		result = append(result, line)
	}
	for _, child := range node.GetChildren() {
		result = append(result, flattenTreeNodes(child, depth+1)...)
	}
	return result
}

// runFzfFilter launches fzf with the given lines and returns the selected address.
func runFzfFilter(lines []string) (string, error) {
	cmd := exec.Command("fzf", "--ansi", "--no-sort", "--header=Select a view", "--with-nth=2..")
	cmd.Stderr = os.Stderr
	cmd.Stdout = nil

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	go func() {
		defer func() { _ = stdin.Close() }()
		for _, line := range lines {
			_, _ = fmt.Fprintln(stdin, line)
		}
	}()

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	selected := strings.TrimSpace(string(out))
	if selected == "" {
		return "", nil
	}
	// Extract address (first field before tab)
	if idx := strings.Index(selected, "\t"); idx > 0 {
		return selected[:idx], nil
	}
	return selected, nil
}

// findTreeNodeByAddress searches the tree for a node whose rawViewNode address matches.
func findTreeNodeByAddress(root *tview.TreeNode, address string) *tview.TreeNode {
	if ref, ok := root.GetReference().(*rawViewNode); ok && ref != nil && ref.Address == address {
		return root
	}
	for _, child := range root.GetChildren() {
		if found := findTreeNodeByAddress(child, address); found != nil {
			return found
		}
	}
	return nil
}

// supportsInlineImage checks if the terminal supports the Kitty graphics protocol.
func supportsInlineImage() bool {
	term := os.Getenv("TERM_PROGRAM")
	return term == "ghostty" || term == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// loadSnapshotImage loads a PNG file and returns the decoded image.
// Returns nil if the file cannot be read or decoded.
func loadSnapshotImage(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	img, err := png.Decode(f)
	if err != nil {
		return nil
	}
	return img
}
