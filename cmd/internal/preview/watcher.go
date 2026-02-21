package preview

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchState holds mutable state for the watch loop, protected by a mutex.
// Immutable configuration (device, loaderPath, etc.) lives in watchContext.
type watchState struct {
	mu              sync.Mutex
	reloadCounter   int
	previewSelector string
	previewIndex    int               // current 0-based preview index
	previewCount    int               // total number of #Preview blocks (0 = unknown)
	building        bool              // true while rebuildAndRelaunch is running
	skeletonMap     map[string]string // file path → skeleton hash
	trackedFiles    []string          // target + 1-level dependency file paths
}

func runWatcher(ctx context.Context, sourceFile string, pc ProjectConfig,
	bs *buildSettings, dirs previewDirs, wctx watchContext,
	ws *watchState, hid *hidHandler, events watchEvents) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating file watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Watch directories containing Swift source files.
	// Use git to discover them (fast, respects .gitignore), falling back to WalkDir.
	watchRoot := filepath.Dir(pc.primaryPath())
	watchDirs, err := gitSwiftDirs(watchRoot)
	if err != nil {
		slog.Debug("git ls-files unavailable, falling back to WalkDir", "err", err)
		watchDirs, err = walkSwiftDirs(watchRoot)
		if err != nil {
			return fmt.Errorf("setting up directory watch: %w", err)
		}
	}
	for _, d := range watchDirs {
		if err := watcher.Add(d); err != nil {
			slog.Debug("Cannot watch directory", "path", d, "err", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Watching %s for changes (Enter to cycle previews, Ctrl+C to stop)...\n", watchRoot)

	// Read commands from stdin (preview cycling or file switching in serve mode).
	cmdCh := make(chan stdinCommand, 1)
	go readStdinCommands(cmdCh, wctx.serve)

	var trackedDebounce *time.Timer // fast path: tracked file change → hot reload
	var depDebounce *time.Timer     // full path: untracked dependency change → rebuild + relaunch

	// Channels for debounce timer signals. The actual work runs in the select
	// loop to avoid data races on local variables (depDebounce, trackedDebounce).
	trackedDebounceCh := make(chan string, 1) // carries the changed file path
	depDebounceCh := make(chan struct{}, 1)

	// Build a set of tracked file paths for efficient lookup.
	ws.mu.Lock()
	trackedFiles := ws.trackedFiles
	ws.mu.Unlock()
	trackedSet := make(map[string]bool, len(trackedFiles))
	for _, tf := range trackedFiles {
		trackedSet[filepath.Clean(tf)] = true
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "\nStopping watcher...")
			return nil // cleanup handled by defer in Run

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Only react to .swift files
			if !strings.HasSuffix(event.Name, ".swift") {
				continue
			}

			// Accept Write and Create (atomic save = rename creates new file)
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			cleanEvent := filepath.Clean(event.Name)

			if trackedSet[cleanEvent] {
				// Tracked file changed (target or 1-level dependency)
				// → decide hot-reload vs full rebuild via skeleton comparison.
				if depDebounce != nil {
					// A dependency rebuild is already pending; it will
					// include the tracked change too, so skip fast path.
					continue
				}
				if trackedDebounce != nil {
					trackedDebounce.Stop()
				}
				changedFile := cleanEvent
				trackedDebounce = time.AfterFunc(200*time.Millisecond, func() {
					select {
					case trackedDebounceCh <- changedFile:
					default:
					}
				})
			} else {
				// Untracked .swift file changed → full rebuild path
				if trackedDebounce != nil {
					trackedDebounce.Stop()
					trackedDebounce = nil
				}
				if depDebounce != nil {
					depDebounce.Stop()
				}
				depDebounce = time.AfterFunc(500*time.Millisecond, func() {
					select {
					case depDebounceCh <- struct{}{}:
					default:
					}
				})
			}

		case changedFile := <-trackedDebounceCh:
			ws.mu.Lock()
			prev := ws.skeletonMap[changedFile]
			ws.mu.Unlock()

			strategy, newSkeleton := classifyChange(changedFile, prev)

			switch strategy {
			case strategyHotReload:
				ws.mu.Lock()
				ws.skeletonMap[changedFile] = newSkeleton
				ws.mu.Unlock()
				if err := reloadMultiFile(ctx, sourceFile, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "Reload error: %v\n", err)
				}
			case strategyRebuild:
				if err := rebuildAndRelaunch(ctx, sourceFile, pc, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "Rebuild error: %v\n", err)
				}
				// Recompute skeletons for all tracked files after rebuild.
				ws.mu.Lock()
				for _, tf := range ws.trackedFiles {
					if s, _ := computeSkeleton(tf); s != "" {
						ws.skeletonMap[filepath.Clean(tf)] = s
					}
				}
				ws.mu.Unlock()
			}

		case <-depDebounceCh:
			depDebounce = nil
			if err := rebuildAndRelaunch(ctx, sourceFile, pc, bs, dirs, wctx, ws); err != nil {
				fmt.Fprintf(os.Stderr, "Rebuild error: %v\n", err)
			}

		case cmd := <-cmdCh:
			switch cmd.Type {
			case "switchFile":
				if cmd.Path == "" {
					continue
				}
				// File switch request from IDE
				fmt.Fprintf(os.Stderr, "\nSwitching file to %s...\n", cmd.Path)
				if err := switchFile(ctx, cmd.Path, pc, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "File switch error: %v\n", err)
				} else {
					sourceFile = cmd.Path
					// Rebuild trackedSet from updated watchState.
					ws.mu.Lock()
					trackedSet = make(map[string]bool, len(ws.trackedFiles))
					for _, tf := range ws.trackedFiles {
						trackedSet[filepath.Clean(tf)] = true
					}
					ws.mu.Unlock()
				}
			case "nextPreview":
				// Preview cycle
				ws.mu.Lock()
				count := ws.previewCount
				ws.mu.Unlock()
				if count <= 1 {
					continue
				}
				ws.mu.Lock()
				ws.previewIndex = (ws.previewIndex + 1) % count
				ws.previewSelector = strconv.Itoa(ws.previewIndex)
				ws.mu.Unlock()
				fmt.Fprintf(os.Stderr, "\nSwitching to preview %d/%d...\n", ws.previewIndex+1, count)
				if err := reloadMultiFile(ctx, sourceFile, bs, dirs, wctx, ws); err != nil {
					fmt.Fprintf(os.Stderr, "Reload error: %v\n", err)
				}
			case "tap", "swipe", "text", "touchDown", "touchMove", "touchUp":
				hid.Handle(cmd)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("Watcher error", "err", err)

		case err, ok := <-events.idbErr:
			if ok && err != nil {
				return fmt.Errorf("idb_companion error: %w", err)
			}
		}
	}
}

// reloadMultiFile generates a combined thunk for all tracked files and hot-reloads.
func reloadMultiFile(ctx context.Context, sourceFile string, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build in progress, skipping hot-reload")
		return nil
	}
	counter := ws.reloadCounter
	tracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "\nFile changed, reloading...")

	updatePreviewCount(sourceFile, ws)

	// Read selector AFTER updatePreviewCount so that a reset (e.g. preview
	// removed) is reflected in this compile cycle.
	ws.mu.Lock()
	selector := ws.previewSelector
	ws.mu.Unlock()

	dylibPath, err := compilePipeline(ctx, sourceFile, tracked, bs, dirs, selector, counter)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	if err := deploy(dylibPath, dirs, bs, wctx); err != nil {
		return err
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	return nil
}

// switchFile handles switching to a new source file in serve mode.
// It resolves dependencies, generates a thunk, and attempts a hot-reload.
// Falls back to rebuild+relaunch if compile fails, and full restart as last resort.
func switchFile(ctx context.Context, newSourceFile string, pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	if _, err := os.Stat(newSourceFile); err != nil {
		return fmt.Errorf("source file not found: %s", newSourceFile)
	}

	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build in progress, skipping file switch")
		return nil
	}
	ws.mu.Unlock()

	// 1. Resolve dependencies for the new file.
	projectRoot := filepath.Dir(pc.primaryPath())
	depFiles, err := resolveDependencies(newSourceFile, projectRoot)
	if err != nil {
		slog.Warn("Failed to resolve dependencies for new file", "err", err)
	}

	trackedFiles := []string{newSourceFile}
	trackedFiles = append(trackedFiles, depFiles...)

	// 2. Parse source and dependency files, filter private type collisions.
	files, trackedFiles, err := parseAndFilterTrackedFiles(newSourceFile, trackedFiles)
	if err != nil {
		return err
	}

	// Determine preview count/index for the new file.
	previewCount := 0
	if blocks, err := parsePreviewBlocks(newSourceFile); err == nil {
		previewCount = len(blocks)
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	ws.previewSelector = "0"
	ws.mu.Unlock()

	// 3. Fast path: generate thunk → compile → hot-reload.
	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, dirs, "0", newSourceFile)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := compileThunk(ctx, thunkPath, bs, dirs, counter, newSourceFile)
	if err != nil {
		// If context was cancelled (e.g. Ctrl+C), skip retries.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 4. Retry: rebuild project then try compile again.
		slog.Info("Thunk compile failed, attempting rebuild", "err", err)
		if buildErr := buildProject(pc, dirs); buildErr != nil {
			return fmt.Errorf("rebuild: %w", buildErr)
		}
		dylibPath, err = compileThunk(ctx, thunkPath, bs, dirs, counter, newSourceFile)
		if err != nil {
			// 5. Full restart as last resort.
			slog.Warn("Compile still failing after rebuild, performing full restart", "err", err)
			return rebuildAndRelaunch(ctx, newSourceFile, pc, bs, dirs, wctx, ws)
		}
	}

	if err := deploy(dylibPath, dirs, bs, wctx); err != nil {
		return err
	}

	// 6. Update watch state.
	newSkeletonMap := make(map[string]string, len(trackedFiles))
	for _, tf := range trackedFiles {
		if s, err := computeSkeleton(tf); err == nil {
			newSkeletonMap[filepath.Clean(tf)] = s
		}
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.trackedFiles = trackedFiles
	ws.skeletonMap = newSkeletonMap
	ws.previewIndex = 0
	ws.previewCount = previewCount
	ws.mu.Unlock()

	// Update the trackedSet used by the watcher loop.
	// Note: The caller should update sourceFile after this returns.
	return nil
}

// rebuildAndRelaunch performs an incremental build, regenerates the thunk,
// and restarts the app. Used when an untracked dependency .swift file changes.
//
// This function does NOT use compilePipeline because it has a unique fallback:
// when parseTrackedFiles returns empty, it retries with sourceFile only.
// It also uses terminate → install → launch (not the hot-reload deploy path).
func rebuildAndRelaunch(ctx context.Context, sourceFile string, pc ProjectConfig, bs *buildSettings, dirs previewDirs, wctx watchContext, ws *watchState) error {
	ws.mu.Lock()
	if ws.building {
		ws.mu.Unlock()
		slog.Info("Build already in progress, skipping")
		return nil
	}
	ws.building = true
	tracked := append([]string{}, ws.trackedFiles...)
	ws.mu.Unlock()

	defer func() {
		ws.mu.Lock()
		ws.building = false
		ws.mu.Unlock()
	}()

	fmt.Fprintln(os.Stderr, "\nDependency changed, rebuilding...")

	if err := buildProject(pc, dirs); err != nil {
		return fmt.Errorf("incremental build: %w", err)
	}

	files := parseTrackedFiles(sourceFile, tracked)

	// Fallback: if no tracked dependency files have types, use target only.
	if len(files) == 0 {
		types, imports, err := parseSourceFile(sourceFile)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		files = append(files, fileThunkData{
			FileName: filepath.Base(sourceFile),
			AbsPath:  sourceFile,
			Types:    types,
			Imports:  imports,
		})
	}

	ws.mu.Lock()
	counter := ws.reloadCounter
	selector := ws.previewSelector
	ws.mu.Unlock()

	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, dirs, selector, sourceFile)
	if err != nil {
		return fmt.Errorf("thunk: %w", err)
	}

	dylibPath, err := compileThunk(ctx, thunkPath, bs, dirs, counter, sourceFile)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("compile: %w", err)
	}

	terminateApp(bs, wctx.device, wctx.deviceSetPath)

	if _, err := installApp(bs, dirs, wctx.device, wctx.deviceSetPath); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := launchWithHotReload(bs, wctx.loaderPath, dylibPath, dirs.Socket, wctx.device, wctx.deviceSetPath); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	ws.mu.Lock()
	ws.reloadCounter++
	cleanOldDylibs(dirs.Thunk, counter-1)
	ws.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Preview rebuilt and relaunched.")
	return nil
}

// reloadStrategy describes whether a source file change can be handled via
// hot-reload or requires a full rebuild.
type reloadStrategy int

const (
	strategyHotReload reloadStrategy = iota
	strategyRebuild
)

// classifyChange compares the current source skeleton against prevSkeleton and
// returns the appropriate reload strategy plus the new skeleton hash.
// If the skeleton cannot be computed, strategyRebuild is returned with an empty
// skeleton (the caller should recompute after rebuilding).
func classifyChange(sourceFile string, prevSkeleton string) (reloadStrategy, string) {
	newSkeleton, err := computeSkeleton(sourceFile)
	if err != nil {
		slog.Warn("Skeleton computation failed, falling back to rebuild", "err", err)
		return strategyRebuild, ""
	}
	if newSkeleton == prevSkeleton {
		return strategyHotReload, newSkeleton
	}
	slog.Info("Structural change detected, performing full rebuild")
	return strategyRebuild, newSkeleton
}

// stdinCommand represents a command received from stdin (JSON Lines protocol).
type stdinCommand struct {
	Type     string  `json:"type"`
	Path     string  `json:"path,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	StartX   float64 `json:"startX,omitempty"`
	StartY   float64 `json:"startY,omitempty"`
	EndX     float64 `json:"endX,omitempty"`
	EndY     float64 `json:"endY,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Value    string  `json:"value,omitempty"`
}

// readStdinCommands reads JSON Lines from stdin and sends commands on ch.
// In serve mode, JSON objects are parsed; non-JSON lines are treated as legacy
// file path commands for backwards compatibility.
// In non-serve mode, any input triggers a preview cycle.
func readStdinCommands(ch chan<- stdinCommand, serve bool) {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		var cmd stdinCommand

		if serve && line != "" {
			// Try JSON parse first.
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				// Legacy: treat non-JSON as file path.
				cmd = stdinCommand{Type: "switchFile", Path: line}
			}
		} else {
			// Empty line or any input in non-serve mode → preview cycle.
			cmd = stdinCommand{Type: "nextPreview"}
		}

		select {
		case ch <- cmd:
		default: // don't block if previous command hasn't been processed
		}
	}
}

// gitSwiftDirs returns unique directories containing Swift files tracked by git
// (or untracked but not ignored). This is fast and automatically respects .gitignore.
func gitSwiftDirs(root string) ([]string, error) {
	// --cached: tracked files, --others --exclude-standard: new files not yet gitignored
	out, err := exec.Command(
		"git", "-C", root, "ls-files",
		"--cached", "--others", "--exclude-standard",
		"*.swift",
	).Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		dir := filepath.Dir(filepath.Join(root, line))
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no Swift files found")
	}
	return dirs, nil
}

// walkSwiftDirs is the fallback for non-git projects. It walks the directory tree
// skipping hidden directories and common dependency/build artifact directories.
func walkSwiftDirs(root string) ([]string, error) {
	seen := make(map[string]bool)
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "build" || name == "DerivedData" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".swift") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	return dirs, err
}

// cleanOldDylibs removes thunk dylib and object files older than keepAfter.
func cleanOldDylibs(thunkDir string, keepAfter int) {
	for i := 0; i < keepAfter; i++ {
		for _, ext := range []string{".dylib", ".o"} {
			p := filepath.Join(thunkDir, fmt.Sprintf("thunk_%d%s", i, ext))
			if err := os.Remove(p); err == nil {
				slog.Debug("Cleaned old thunk artifact", "path", p)
			}
		}
	}
}
