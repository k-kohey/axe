package view

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
)

// swiftUIMode represents the SwiftUI tree display mode.
type swiftUIMode int

const (
	swiftUIOff swiftUIMode = iota
	swiftUICompact
	swiftUIFull
)

func RunInteractive(appName string, device string) error {
	// Suppress all logs during TUI to avoid corrupting the terminal output.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.Level(math.MaxInt),
	})))
	defer slog.SetDefault(prev)

	app := tview.NewApplication()

	pages := tview.NewPages()

	// Loading screen
	loadingView := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetDynamicColors(true)
	loadingView.SetBorder(false)

	// Tree screen
	treeView := tview.NewTreeView()
	treeView.SetBorder(true).SetTitle(" View Hierarchy ")

	// Detail screen
	detailView := tview.NewTextView().
		SetDynamicColors(true)
	detailView.SetBorder(true)
	detailView.SetScrollable(true)

	// Footer for tree screen
	treeFooter := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetText(" ↑↓ navigate  → expand  ← collapse  Enter detail  f filter  q quit")
	treeWithFooter := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(treeView, 0, 1, true).
		AddItem(treeFooter, 1, 0, false)

	// Snapshot preview panel (right side, only for inline-image-capable terminals)
	canShowInline := supportsInlineImage()
	var snapshotView *tview.Image
	if canShowInline {
		snapshotView = tview.NewImage()
		snapshotView.SetBorder(true).SetTitle(" Snapshot ")
	}

	// Footer for detail screen
	detailFooter := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetText(" Esc back  q quit")

	var detailWithFooter *tview.Flex
	if canShowInline {
		detailContentFlex := tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(detailView, 0, 7, true).
			AddItem(snapshotView, 0, 3, false)
		detailWithFooter = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(detailContentFlex, 0, 1, true).
			AddItem(detailFooter, 1, 0, false)
	} else {
		detailWithFooter = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(detailView, 0, 1, true).
			AddItem(detailFooter, 1, 0, false)
	}

	pages.AddPage("loading", loadingView, true, true)
	pages.AddPage("tree", treeWithFooter, true, false)
	pages.AddPage("detail", detailWithFooter, true, false)

	// Start spinner
	done := make(chan struct{})
	startSpinner(app, loadingView, done)

	// classmap, demangled, and addressClassMap are captured by the closures below after fetch completes.
	var classmap map[string]string
	var demangled map[string]string
	var addressClassMap map[string]string

	// Detail view state
	var currentUIKit UIKitView
	var swiftMode swiftUIMode
	var swiftRawJSON []byte // cached raw JSON from LLDB
	var swiftFetching bool  // true while goroutine is fetching
	var swiftMu sync.Mutex

	// updateDetailFooter rebuilds the detail footer based on current state.
	updateDetailFooter := func() {
		var parts []string
		parts = append(parts, " Esc back", " j/k scroll")
		if currentUIKit.IsHostingView {
			swiftMu.Lock()
			mode := swiftMode
			swiftMu.Unlock()
			switch mode {
			case swiftUIOff:
				parts = append(parts, " s swiftui")
			case swiftUICompact:
				parts = append(parts, " s swiftui:compact")
			case swiftUIFull:
				parts = append(parts, " s swiftui:full")
			}
		}
		if currentUIKit.Snapshot != "" {
			parts = append(parts, " o open snapshot")
		}
		parts = append(parts, " q quit")
		detailFooter.SetText(strings.Join(parts, " "))
	}

	// updateDetailView rebuilds the detail text with optional SwiftUI tree appended.
	updateDetailView := func() {
		text := renderDetailText(currentUIKit, addressClassMap)
		swiftMu.Lock()
		mode := swiftMode
		raw := swiftRawJSON
		swiftMu.Unlock()

		if mode != swiftUIOff && raw != nil {
			compact := mode == swiftUICompact
			nodes, err := ParseSwiftUIJSON(raw, compact)
			if err == nil && len(nodes) > 0 {
				modeLabel := "compact"
				if !compact {
					modeLabel = "full"
				}
				text += fmt.Sprintf("\n[cyan]SwiftUI Tree (%s):[white]\n", modeLabel)
				yamlBytes, yamlErr := yaml.Marshal(SwiftUIOutput{Tree: nodes})
				if yamlErr == nil {
					text += highlightYAML(string(yamlBytes))
				}
			}
		}
		detailView.SetText(text)

		if canShowInline && snapshotView != nil {
			if currentUIKit.Snapshot != "" {
				snapshotView.SetImage(loadSnapshotImage(currentUIKit.Snapshot))
				snapshotView.SetTitle(" Snapshot (o: open) ")
			} else {
				snapshotView.SetImage(nil)
				snapshotView.SetTitle(" Snapshot ")
			}
		}
	}

	// Key bindings for tree view
	treeView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			node := treeView.GetCurrentNode()
			if node == nil {
				return event
			}
			ref, ok := node.GetReference().(*rawViewNode)
			if !ok || ref == nil {
				return event
			}
			currentUIKit = buildDetailWithSnapshot(*ref, classmap, demangled)
			swiftMu.Lock()
			swiftMode = swiftUIOff
			swiftRawJSON = nil
			swiftFetching = false
			swiftMu.Unlock()

			updateDetailView()
			detailView.SetTitle(fmt.Sprintf(" Detail: %s ", demangleClass(ref.Class, demangled)))
			detailView.ScrollToBeginning()
			updateDetailFooter()
			pages.SwitchToPage("detail")
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				app.Stop()
				return nil
			case 'f':
				if _, err := exec.LookPath("fzf"); err != nil {
					return nil
				}
				root := treeView.GetRoot()
				if root == nil {
					return nil
				}
				lines := flattenTreeNodes(root, 0)
				if len(lines) == 0 {
					return nil
				}
				app.Suspend(func() {
					addr, err := runFzfFilter(lines)
					if err != nil || addr == "" {
						return
					}
					if target := findTreeNodeByAddress(root, addr); target != nil {
						treeView.SetCurrentNode(target)
					}
				})
				return nil
			}
		}
		return event
	})

	// Key bindings for detail view
	detailView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			pages.SwitchToPage("tree")
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				app.Stop()
				return nil
			case 'j':
				row, col := detailView.GetScrollOffset()
				detailView.ScrollTo(row+1, col)
				return nil
			case 'k':
				row, col := detailView.GetScrollOffset()
				if row > 0 {
					detailView.ScrollTo(row-1, col)
				}
				return nil
			case 's':
				if !currentUIKit.IsHostingView {
					return nil
				}
				swiftMu.Lock()
				fetching := swiftFetching
				hasCache := swiftRawJSON != nil
				swiftMu.Unlock()

				if fetching {
					return nil
				}

				if hasCache {
					// Cycle mode: compact -> full -> off
					swiftMu.Lock()
					switch swiftMode {
					case swiftUIOff:
						swiftMode = swiftUICompact
					case swiftUICompact:
						swiftMode = swiftUIFull
					case swiftUIFull:
						swiftMode = swiftUIOff
					}
					swiftMu.Unlock()
					updateDetailView()
					updateDetailFooter()
					return nil
				}

				// First time: fetch SwiftUI tree
				swiftMu.Lock()
				swiftFetching = true
				swiftMu.Unlock()

				address := currentUIKit.Address

				// Show spinner in footer
				fetchDone := make(chan struct{})
				go func() {
					i := 0
					for {
						select {
						case <-fetchDone:
							return
						default:
						}
						ch := spinChars[i%len(spinChars)]
						app.QueueUpdateDraw(func() {
							detailFooter.SetText(fmt.Sprintf(" %c Fetching SwiftUI tree...", ch))
						})
						i++
						time.Sleep(100 * time.Millisecond)
					}
				}()

				go func() {
					_, rawJSON, err := fetchSwiftUITree(appName, address, true, device)
					close(fetchDone)

					swiftMu.Lock()
					swiftFetching = false
					if err == nil && rawJSON != nil {
						swiftRawJSON = rawJSON
						swiftMode = swiftUICompact
					}
					swiftMu.Unlock()

					app.QueueUpdateDraw(func() {
						if err != nil {
							detailFooter.SetText(fmt.Sprintf(" Error: %v", err))
							go func() {
								time.Sleep(3 * time.Second)
								app.QueueUpdateDraw(func() {
									updateDetailFooter()
								})
							}()
							return
						}
						updateDetailView()
						updateDetailFooter()
					})
				}()
				return nil
			case 'o':
				if currentUIKit.Snapshot == "" {
					return nil
				}
				if err := exec.Command("open", currentUIKit.Snapshot).Start(); err != nil {
					slog.Debug("Failed to open snapshot", "path", currentUIKit.Snapshot, "err", err)
				}
				return nil
			}
		}
		return event
	})

	// Fetch data in background
	go func() {
		data, err := fetchHierarchy(appName, device)
		close(done)
		if err != nil {
			app.QueueUpdateDraw(func() {
				loadingView.SetText(fmt.Sprintf("\n   Error: %v", err))
			})
			return
		}

		classmap = data.Classmap
		demangled = demangleNames(collectClassNames(data))
		addressClassMap = buildAddressClassMap(data.Views, demangled)

		root := tview.NewTreeNode("Root").SetSelectable(false)
		for _, child := range buildTreeNodes(data.Views, data.Classmap, demangled) {
			root.AddChild(child)
		}

		app.QueueUpdateDraw(func() {
			treeView.SetRoot(root).SetCurrentNode(root.GetChildren()[0])
			pages.SwitchToPage("tree")
		})
	}()

	app.SetRoot(pages, true)
	return app.Run()
}
