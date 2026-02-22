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
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
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

// defaultStreamID is used for single-stream mode (before multi-stream support).
const defaultStreamID = "default"

func Run(sourceFile string, pc ProjectConfig, watch bool, previewSelector string, serve bool, preferredDevice string, reuseBuild bool) error {
	// Set up signal-based context early so that long-running operations
	// (build with lock, compileThunk, etc.) can be cancelled via Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// In serve mode, create an EventWriter to send JSON Lines to stdout.
	var ew *EventWriter
	if serve {
		ew = NewEventWriter(os.Stdout)
	}

	// sendStatus sends a StreamStatus event in serve mode (no-op otherwise).
	sendStatus := func(phase string) {
		if ew != nil {
			if err := ew.Send(&pb.Event{StreamId: defaultStreamID, Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: phase}}}); err != nil {
				slog.Warn("Failed to send StreamStatus", "phase", phase, "err", err)
			}
		}
	}

	// sendStopped sends a StreamStopped event in serve mode (no-op otherwise).
	sendStopped := func(reason, message, diagnostic string) {
		if ew != nil {
			if err := ew.Send(&pb.Event{StreamId: defaultStreamID, Payload: &pb.Event_StreamStopped{StreamStopped: &pb.StreamStopped{Reason: reason, Message: message, Diagnostic: diagnostic}}}); err != nil {
				slog.Warn("Failed to send StreamStopped", "reason", reason, "err", err)
			}
		}
	}

	step := &stepper{total: 9}

	done := step.begin("Resolving simulator...")
	device, deviceSetPath, err := platform.ResolveAxeSimulator(preferredDevice)
	done()
	if err != nil {
		sendStopped("resource_error", err.Error(), "")
		return err
	}

	dirs := newPreviewDirs(pc.primaryPath(), device)

	done = step.begin("Fetching build settings...")
	bs, err := fetchBuildSettings(pc, dirs)
	done()
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}

	sendStatus("building")
	if reuseBuild && hasPreviousBuild(bs, dirs) {
		done = step.begin(fmt.Sprintf("Reusing previous build (%s)...", dirs.Build))
		done()
	} else {
		label := "Building project..."
		if reuseBuild {
			label = "No previous build found, building project..."
		}
		done = step.begin(label)
		err = buildProject(ctx, pc, dirs)
		done()
		if err != nil {
			sendStopped("build_error", "Build failed", err.Error())
			return err
		}
	}

	extractCompilerPaths(ctx, bs, dirs)

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
		sendStopped("build_error", err.Error(), "")
		return err
	}
	slog.Debug("Tracked files (after collision filter)", "count", len(trackedFiles), "files", trackedFiles)

	done = step.begin("Generating combined thunk...")
	thunkPath, err := generateCombinedThunk(files, bs.ModuleName, dirs, previewSelector, sourceFile)
	done()
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}

	done = step.begin("Compiling thunk dylib...")
	dylibPath, err := compileThunk(ctx, thunkPath, bs, dirs, 0, sourceFile)
	done()
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}

	// Boot the simulator headlessly via idb_companion.
	// Stopping bootCompanion will terminate the process and shut down the simulator.
	sendStatus("booting")
	done = step.begin("Booting simulator...")
	bootCompanion, err := idb.BootHeadless(device, deviceSetPath)
	done()
	if err != nil {
		sendStopped("boot_error", fmt.Sprintf("booting simulator: %v", err), "")
		return fmt.Errorf("booting simulator: %w", err)
	}

	// Verify the simulator didn't crash immediately after boot.
	select {
	case <-bootCompanion.Done():
		msg := fmt.Sprintf("simulator crashed immediately after boot: %v", bootCompanion.Err())
		sendStopped("boot_error", msg, "")
		return fmt.Errorf("%s", msg)
	default:
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

	sendStatus("installing")
	done = step.begin("Installing app on simulator...")
	_, err = installApp(ctx, bs, dirs, device, deviceSetPath)
	done()
	if err != nil {
		sendStopped("install_error", err.Error(), "")
		return err
	}

	loaderPath, err := compileLoader(dirs, bs.DeploymentTarget)
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}

	sendStatus("running")
	done = step.begin("Launching app...")
	err = launchWithHotReload(bs, loaderPath, dylibPath, dirs.Socket, device, deviceSetPath)
	done()
	if err != nil {
		sendStopped("runtime_error", err.Error(), "")
		return err
	}

	// Count previews for StreamStarted.
	previewCount := 0
	if blocks, parseErr := parsePreviewBlocks(sourceFile); parseErr == nil {
		previewCount = len(blocks)
	}

	// Send StreamStarted event in serve mode.
	if ew != nil {
		if err := ew.Send(&pb.Event{StreamId: defaultStreamID, Payload: &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: int32(previewCount)}}}); err != nil {
			slog.Warn("Failed to send StreamStarted", "err", err)
		}
	}

	// Set up idb client and companion for serve mode.
	var idbErrCh chan error

	if serve {
		companion, err := idb.Start(device, deviceSetPath)
		if err != nil {
			sendStopped("runtime_error", fmt.Sprintf("starting idb_companion: %v", err), "")
			return fmt.Errorf("starting idb_companion: %w", err)
		}
		idbCompanion = companion

		client, err := idb.NewClient(companion.Address())
		if err != nil {
			sendStopped("runtime_error", fmt.Sprintf("connecting to idb_companion: %v", err), "")
			return fmt.Errorf("connecting to idb_companion: %w", err)
		}
		idbClient = client

		streamCtx, cancel := context.WithCancel(context.Background())
		cancelStream = cancel
		idbErrCh = make(chan error, 1)
		voc := &videoOutputConfig{
			ew:       ew,
			streamID: defaultStreamID,
			device:   device,
			file:     sourceFile,
		}
		go relayVideoStreamEvents(streamCtx, idbClient, idbErrCh, voc)
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

		events := watchEvents{idbErr: idbErrCh, bootDied: bootCompanion.Done()}

		fmt.Fprintln(os.Stderr, "Preview launched with hot-reload support.")
		return runWatcher(ctx, sourceFile, pc, bs, dirs, wctx, ws, hid, events)
	}

	// Non-watch mode: wait for signal or boot companion crash.
	fmt.Fprintln(os.Stderr, "Preview launched successfully.")
	select {
	case <-ctx.Done():
		return nil
	case <-bootCompanion.Done():
		msg := fmt.Sprintf("simulator crashed unexpectedly: %v", bootCompanion.Err())
		sendStopped("runtime_error", msg, "")
		return fmt.Errorf("%s", msg)
	}
}

// hasPreviousBuild checks whether a .app bundle exists in the build products directory.
func hasPreviousBuild(bs *buildSettings, dirs previewDirs) bool {
	_, err := resolveAppBundle(bs, dirs)
	return err == nil
}

// RunServe is the multi-stream entry point for --serve mode.
// It reads AddStream/RemoveStream commands from stdin and manages
// multiple preview streams concurrently via StreamManager.
func RunServe(pc ProjectConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	ew := NewEventWriter(os.Stdout)

	deviceSetPath, err := platform.AxeDeviceSetPath()
	if err != nil {
		return fmt.Errorf("resolving device set path: %w", err)
	}
	if err := os.MkdirAll(deviceSetPath, 0o755); err != nil {
		return fmt.Errorf("creating device set directory: %w", err)
	}

	pool := platform.NewDevicePool(&platform.RealSimctlRunner{}, deviceSetPath)

	if err := pool.CleanupOrphans(ctx); err != nil {
		slog.Warn("Failed to clean up orphaned devices", "err", err)
	}

	sm := NewStreamManager(pool, ew, pc, deviceSetPath)

	// Start shared file watcher for all streams.
	watcher, err := newSharedWatcher(ctx, pc)
	if err != nil {
		return fmt.Errorf("creating shared file watcher: %w", err)
	}
	sm.watcher = watcher
	defer watcher.close()

	// Read commands from stdin. When stdin closes (extension crash/exit),
	// the loop returns and we proceed to cleanup.
	runCommandLoop(ctx, os.Stdin, sm)

	sm.StopAll()
	pool.GarbageCollect(ctx)

	return nil
}
