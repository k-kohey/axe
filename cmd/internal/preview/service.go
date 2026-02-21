package preview

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	"github.com/k-kohey/axe/internal/platform"
)

// stepper tracks the current step number and total for progress output.
type stepper struct {
	n     int
	total int
}

// begin prints "[n/total] label" and returns a function that prints the elapsed time.
func (s *stepper) begin(label string) func() {
	s.n++
	fmt.Fprintf(os.Stderr, "[%d/%d] %s", s.n, s.total, label)
	start := time.Now()
	return func() {
		fmt.Fprintf(os.Stderr, " (%.1fs)\n", time.Since(start).Seconds())
	}
}

func Run(sourceFile string, pc ProjectConfig, watch bool, previewSelector string, serve bool) error {
	step := &stepper{total: 9}

	done := step.begin("Resolving simulator...")
	device, deviceSetPath, err := platform.ResolveAxeSimulator()
	done()
	if err != nil {
		return err
	}

	dirs := newPreviewDirs(pc.primaryPath())

	done = step.begin("Fetching build settings...")
	bs, err := fetchBuildSettings(pc, dirs)
	done()
	if err != nil {
		return err
	}

	done = step.begin("Building project...")
	err = buildProject(pc, dirs)
	done()
	if err != nil {
		return err
	}

	extractCompilerPaths(bs, dirs)

	// Resolve 1-level dependencies from the target file.
	projectRoot := filepath.Dir(pc.primaryPath())
	depFiles, err := resolveDependencies(sourceFile, projectRoot)
	if err != nil {
		slog.Warn("Failed to resolve dependencies, proceeding with target only", "err", err)
	}

	// Build tracked file list: target + dependencies.
	trackedFiles := []string{sourceFile}
	trackedFiles = append(trackedFiles, depFiles...)
	slog.Debug("Tracked files (before collision check)", "count", len(trackedFiles), "files", trackedFiles)

	done = step.begin("Parsing source file...")
	files, trackedFiles, err := parseAndFilterTrackedFiles(sourceFile, trackedFiles)
	done()
	if err != nil {
		return err
	}
	slog.Debug("Tracked files (after collision filter)", "count", len(trackedFiles), "files", trackedFiles)

	done = step.begin("Generating combined thunk...")
	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, dirs, previewSelector, sourceFile)
	done()
	if err != nil {
		return err
	}

	// Set up signal-based context so that Ctrl+C cancels in-flight external
	// commands (compileThunk, etc.) via context propagation.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	done = step.begin("Compiling thunk dylib...")
	dylibPath, err := compileThunk(ctx, thunkPath, bs, dirs, 0, sourceFile)
	done()
	if err != nil {
		return err
	}

	// Boot the simulator headlessly via idb_companion.
	// Stopping bootCompanion will terminate the process and shut down the simulator.
	done = step.begin("Booting simulator...")
	bootCompanion, err := idb.BootHeadless(device, deviceSetPath)
	done()
	if err != nil {
		return fmt.Errorf("booting simulator: %w", err)
	}

	// Shared cleanup: runs on normal return, error return, and signal-triggered return.
	var idbClient idb.IDBClient
	var idbCompanion *idb.Companion
	var cancelStream func()
	defer func() {
		if cancelStream != nil {
			cancelStream()
		}
		terminateApp(bs, device, deviceSetPath)
		if err := os.Remove(dirs.Socket); err != nil && !os.IsNotExist(err) {
			slog.Debug("Failed to remove socket", "path", dirs.Socket, "err", err)
		}
		if idbClient != nil {
			if err := idbClient.Close(); err != nil {
				slog.Debug("Failed to close idb client", "err", err)
			}
		}
		if idbCompanion != nil {
			if err := idbCompanion.Stop(); err != nil {
				slog.Debug("Failed to stop idb companion", "err", err)
			}
		}
		if err := bootCompanion.Stop(); err != nil {
			slog.Debug("Failed to stop boot companion", "err", err)
		}
	}()

	terminateApp(bs, device, deviceSetPath)

	done = step.begin("Installing app on simulator...")
	_, err = installApp(bs, dirs, device, deviceSetPath)
	done()
	if err != nil {
		return err
	}

	loaderPath, err := compileLoader(dirs, bs.DeploymentTarget)
	if err != nil {
		return err
	}

	done = step.begin("Launching app...")
	err = launchWithHotReload(bs, loaderPath, dylibPath, dirs.Socket, device, deviceSetPath)
	done()
	if err != nil {
		return err
	}

	// Set up idb client and companion for serve mode.
	var idbErrCh chan error

	if serve {
		companion, err := idb.Start(device, deviceSetPath)
		if err != nil {
			return fmt.Errorf("starting idb_companion: %w", err)
		}
		idbCompanion = companion

		client, err := idb.NewClient(companion.Address())
		if err != nil {
			return fmt.Errorf("connecting to idb_companion: %w", err)
		}
		idbClient = client

		streamCtx, cancel := context.WithCancel(context.Background())
		cancelStream = cancel
		idbErrCh = make(chan error, 1)
		go relayVideoStream(streamCtx, idbClient, idbErrCh)
	}

	if watch {
		// Compute initial skeleton hashes for all tracked files.
		skeletonMap := make(map[string]string, len(trackedFiles))
		for _, tf := range trackedFiles {
			if s, err := computeSkeleton(tf); err == nil {
				skeletonMap[filepath.Clean(tf)] = s
			}
		}

		wctx := watchContext{
			device:        device,
			deviceSetPath: deviceSetPath,
			loaderPath:    loaderPath,
			serve:         serve,
		}

		initialIndex := 0
		if idx, err := strconv.Atoi(previewSelector); err == nil {
			initialIndex = idx
		}
		previewCount := 0
		if blocks, err := parsePreviewBlocks(sourceFile); err == nil {
			previewCount = len(blocks)
		}

		ws := &watchState{
			reloadCounter:   1, // 0 was used for the initial launch
			previewSelector: previewSelector,
			previewIndex:    initialIndex,
			previewCount:    previewCount,
			skeletonMap:     skeletonMap,
			trackedFiles:    trackedFiles,
		}

		var hid *hidHandler
		if idbClient != nil {
			if w, h, err := idbClient.ScreenSize(context.Background()); err == nil {
				hid = newHIDHandler(idbClient, w, h)
			}
		}

		events := watchEvents{idbErr: idbErrCh}

		fmt.Fprintln(os.Stderr, "Preview launched with hot-reload support.")
		return runWatcher(ctx, sourceFile, pc, bs, dirs, wctx, ws, hid, events)
	}

	// Non-watch mode: wait for signal to exit.
	fmt.Fprintln(os.Stderr, "Preview launched successfully.")
	<-ctx.Done()
	return nil
}
