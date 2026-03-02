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
	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/runner"
	"github.com/k-kohey/axe/internal/preview/watch"
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

// defaultRunners creates the production implementations of all runner interfaces.
func defaultRunners() (BuildRunner, ToolchainRunner, AppRunner, FileCopier, SourceLister) {
	return build.NewRunner(), &runner.Toolchain{}, &runner.App{}, &runner.FileCopy{}, &runner.SourceList{}
}

func Run(opts RunOptions) error {
	// Set up signal-based context early so that long-running operations
	// (build with lock, compileThunk, etc.) can be cancelled via Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	// In serve mode, create an EventWriter to send JSON Lines to stdout.
	var ew *protocol.EventWriter
	if opts.Serve {
		ew = protocol.NewEventWriter(os.Stdout)

		// Advertise the protocol version to the extension.
		if err := ew.Send(&pb.Event{
			Payload: &pb.Event_Hello{
				Hello: &pb.Hello{ProtocolVersion: protocol.ProtocolVersion},
			},
		}); err != nil {
			return fmt.Errorf("sending hello: %w", err)
		}
	}

	br, tc, ar, fc, sl := defaultRunners()

	// Oneshot mode: delegate to PreviewSession for a single Build+Boot cycle.
	if !opts.Watch && !opts.Serve {
		return runOneshot(ctx, opts, br, tc, ar, fc)
	}

	// --- Watch/Serve mode below (unchanged) ---

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

	step := &stepper{total: 6}

	simctl := &platform.RealSimctlRunner{}
	var device, deviceSetPath string
	var isExternalDevice bool
	var done func()
	var err error
	if opts.DeviceUDID != "" {
		device = opts.DeviceUDID
		deviceSetPath = opts.DeviceSetPath
	} else {
		done = step.begin("Resolving simulator...")
		device, deviceSetPath, isExternalDevice, err = platform.ResolveAxeSimulator(simctl, opts.PreferredDevice)
		done()
		if err != nil {
			sendStopped("resource_error", err.Error(), "")
			return err
		}
	}

	var dirs previewDirs
	dirs, err = newPreviewDirs(opts.PC.PrimaryPath(), device)
	if err != nil {
		sendStopped("resource_error", err.Error(), "")
		return err
	}

	sendStatus("building")
	done = step.begin("Building...")
	var result *build.Result
	if opts.Preparer != nil {
		result, err = opts.Preparer.Prepare(ctx)
	} else {
		result, err = build.Prepare(ctx, opts.PC, dirs.ProjectDirs, opts.ReuseBuild, br)
	}
	done()
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}
	bs := result.Settings

	// Use CompileStrategy to decide between full and main-only thunk compilation.
	var depGraph *analysis.DependencyGraph
	var trackedFiles []string
	var indexCache *sharedIndexCache

	strategy := NewCompileStrategy(opts.Watch, opts.Serve, opts.FullThunk, opts.Strict)
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(ctx context.Context) (string, error) {
			// Load Index Store cache for fast in-memory dependency resolution.
			projectRoot := filepath.Dir(opts.PC.PrimaryPath())
			rawCache, cacheErr := analysis.LoadIndexStore(ctx, dirs.IndexStorePath(), projectRoot)
			if cacheErr != nil && ctx.Err() == nil {
				slog.Warn("Index store cache unavailable", "err", cacheErr)
			}
			indexCache = newSharedIndexCache(rawCache)

			dg, depFiles, err := analysis.ResolveTransitiveDependencies(ctx, opts.SourceFile, indexCache.Get())
			if err != nil && ctx.Err() == nil {
				slog.Warn("Failed to resolve dependencies, proceeding with target only", "err", err)
			}
			depGraph = dg

			tf := []string{opts.SourceFile}
			tf = append(tf, depFiles...)
			slog.Debug("Tracked files", "count", len(tf), "files", tf)

			files, tf, err := parseAndFilterTrackedFiles(opts.SourceFile, tf, indexCache.Get())
			if err != nil {
				return "", err
			}
			trackedFiles = tf

			thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, dirs.Thunk, opts.PreviewSelector, opts.SourceFile, 0)
			if err != nil {
				return "", err
			}

			return codegen.CompileThunk(ctx, thunkPaths, compileConfigFromSettings(bs), dirs.Thunk, dirs.Build, 0, opts.SourceFile, tc)
		},
		CompileModeMainOnly: func(ctx context.Context) (string, error) {
			return compileMainOnlyPipeline(ctx, opts.SourceFile, bs, dirs, opts.PreviewSelector, 0, tc)
		},
	}

	done = step.begin("Compiling thunk...")
	compileResult, err := ExecuteCompileStrategy(ctx, strategy, compilers)
	done()
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}
	dylibPath := compileResult.DylibPath

	// Boot the simulator.
	// For external (standard Xcode set) devices, use simctl boot (non-headless)
	// and skip shutdown on exit since the user may be using the device elsewhere.
	// For axe-managed devices, use idb_companion via bootWithRetry.
	sendStatus("booting")
	var bootCompanion *idb.Companion
	if isExternalDevice {
		done = step.begin("Booting simulator (standard set)...")
		bootCtx, bootCancel := context.WithTimeout(ctx, 30*time.Second)
		err = simctl.Boot(bootCtx, device)
		bootCancel()
		done()
		if err != nil {
			sendStopped("boot_error", fmt.Sprintf("booting simulator: %v", err), "")
			return fmt.Errorf("booting simulator: %w", err)
		}
	} else {
		done = step.begin("Booting simulator...")
		bootCompanion, err = bootWithRetry(ctx, device, deviceSetPath, !opts.NoHeadless)
		done()
		if err != nil {
			sendStopped("boot_error", fmt.Sprintf("booting simulator: %v", err), "")
			return fmt.Errorf("booting simulator: %w", err)
		}
	}

	// Verify the simulator didn't crash immediately after boot.
	if bootCompanion != nil {
		select {
		case <-bootCompanion.Done():
			msg := fmt.Sprintf("simulator crashed immediately after boot: %v", bootCompanion.Err())
			sendStopped("boot_error", msg, "")
			return fmt.Errorf("%s", msg)
		default:
		}
	}

	// Shared cleanup: runs on normal return, error return, and signal-triggered return.
	var idbClient idb.IDBClient
	var idbCompanion *idb.Companion
	var cancelStream func()
	defer func() {
		if cancelStream != nil {
			cancelStream()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		terminateApp(cleanupCtx, bs, device, deviceSetPath, ar)
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
		if bootCompanion != nil {
			if err := bootCompanion.Stop(); err != nil {
				slog.Debug("Failed to stop boot companion", "err", err)
			}
		}
	}()

	terminateApp(ctx, bs, device, deviceSetPath, ar)

	sendStatus("installing")
	done = step.begin("Installing app on simulator...")
	_, err = installApp(ctx, bs, dirs, device, deviceSetPath, ar, fc)
	done()
	if err != nil {
		sendStopped("install_error", err.Error(), "")
		return err
	}

	loaderPath, err := codegen.CompileLoader(ctx, dirs.Loader, bs.DeploymentTarget, tc)
	if err != nil {
		sendStopped("build_error", err.Error(), "")
		return err
	}

	sendStatus("running")
	done = step.begin("Launching app...")
	err = launchWithHotReload(ctx, bs, loaderPath, dylibPath, dirs.Socket, device, deviceSetPath, ar)
	done()
	if err != nil {
		sendStopped("runtime_error", err.Error(), "")
		return err
	}

	// Count previews for StreamStarted.
	previewCount := 0
	if blocks, parseErr := analysis.PreviewBlocks(opts.SourceFile); parseErr == nil {
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

	if opts.Serve {
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
		voc := &protocol.VideoOutputConfig{
			EW:       ew,
			StreamID: defaultStreamID,
			Device:   device,
			File:     opts.SourceFile,
		}
		go protocol.RelayVideoStreamEvents(streamCtx, idbClient, idbErrCh, voc)
	}

	if compileResult.Degraded {
		sendStatus("degraded")
		slog.Warn("Running in degraded mode: hot-reload not available")
		if err := codegen.WaitForReady(ctx, dirs.Socket); err != nil {
			sendStopped("runtime_error", err.Error(), "")
			return err
		}
		fmt.Fprintln(os.Stderr, "Preview launched in degraded mode (hot-reload disabled).")

		// Block until termination signal or fatal event.
		// Without this, the deferred cleanup would run immediately, stopping
		// the simulator and making the degraded preview useless.
		var bootDiedCh <-chan struct{}
		if bootCompanion != nil {
			bootDiedCh = bootCompanion.Done()
		}
		// nil channel blocks forever in select, which is correct for external
		// devices — the "simulator crashed" case is effectively disabled.
		select {
		case <-ctx.Done():
			return nil
		case <-bootDiedCh:
			msg := "simulator crashed unexpectedly"
			if bootCompanion != nil {
				if err := bootCompanion.Err(); err != nil {
					msg = fmt.Sprintf("simulator crashed: %v", err)
				}
			}
			sendStopped("runtime_error", msg, "")
			return fmt.Errorf("boot companion died")
		case err := <-idbErrCh:
			if err != nil {
				msg := fmt.Sprintf("idb_companion error: %v", err)
				sendStopped("runtime_error", msg, "")
				return fmt.Errorf("idb error: %w", err)
			}
			return nil
		}
	}

	// Compute initial skeleton hashes for all tracked files.
	skeletonMap := buildSkeletonMap(trackedFiles)

	wctx := watchContext{
		device:        device,
		deviceSetPath: deviceSetPath,
		loaderPath:    loaderPath,
		streamID:      defaultStreamID,
		serve:         opts.Serve,
		ew:            ew,
		build:         br,
		toolchain:     tc,
		app:           ar,
		copier:        fc,
		sources:       sl,
	}

	initialIndex := 0
	if idx, err := strconv.Atoi(opts.PreviewSelector); err == nil {
		initialIndex = idx
	}

	ws := &watchState{
		reloadCounter:   1, // 0 was used for the initial launch
		previewSelector: opts.PreviewSelector,
		previewIndex:    initialIndex,
		previewCount:    previewCount,
		skeletonMap:     skeletonMap,
		trackedFiles:    trackedFiles,
		depGraph:        depGraph,
		indexCache:      indexCache,
	}

	var hid *protocol.HIDHandler
	if idbClient != nil {
		if w, h, err := idbClient.ScreenSize(context.Background()); err == nil {
			hid = protocol.NewHIDHandler(idbClient, w, h)
		}
	}

	var watchBootDiedCh <-chan struct{}
	if bootCompanion != nil {
		watchBootDiedCh = bootCompanion.Done()
	}
	fmt.Fprintln(os.Stderr, "Preview launched with hot-reload support.")
	return runWatcher(ctx, opts.SourceFile, opts.PC, bs, dirs, wctx, ws, hid, idbErrCh, watchBootDiedCh)
}

// runOneshot handles the oneshot preview mode (no watch, no serve) using
// PreviewSession. Build and Boot run in parallel, then a single
// CapturePreview captures the preview.
func runOneshot(ctx context.Context, opts RunOptions, br BuildRunner, tc ToolchainRunner, ar AppRunner, fc FileCopier) error {
	step := &stepper{total: 3}

	simctl := &platform.RealSimctlRunner{}
	var device, deviceSetPath string
	var isExternalDevice bool
	if opts.DeviceUDID != "" {
		device = opts.DeviceUDID
		deviceSetPath = opts.DeviceSetPath
	} else {
		done := step.begin("Resolving simulator...")
		var err error
		device, deviceSetPath, isExternalDevice, err = platform.ResolveAxeSimulator(simctl, opts.PreferredDevice)
		done()
		if err != nil {
			return err
		}
	}

	done := step.begin("Preparing session...")
	sess, err := NewPreviewSession(ctx, SessionConfig{
		PC:               opts.PC,
		DeviceUDID:       device,
		DeviceSetPath:    deviceSetPath,
		IsExternalDevice: isExternalDevice,
		NoHeadless:       opts.NoHeadless,
		Preparer:         opts.Preparer,
		ReuseBuild:       opts.ReuseBuild,
		BuildRunner:      br,
		Toolchain:        tc,
		AppRunner:        ar,
		Copier:           fc,
	})
	done()
	if err != nil {
		return err
	}
	defer sess.Close()

	done = step.begin("Capturing preview...")
	err = sess.CapturePreview(ctx, CaptureRequest{
		SourceFile:      opts.SourceFile,
		PreviewSelector: opts.PreviewSelector,
		OnReady:         opts.OnReady,
	})
	done()
	return err
}

// RunServe is the multi-stream entry point for serve mode.
// It reads AddStream/RemoveStream commands from stdin and manages
// multiple preview streams concurrently via StreamManager.
func RunServe(pc ProjectConfig, strict bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	ew := protocol.NewEventWriter(os.Stdout)

	// Advertise the protocol version to the extension.
	if err := ew.Send(&pb.Event{
		Payload: &pb.Event_Hello{
			Hello: &pb.Hello{ProtocolVersion: protocol.ProtocolVersion},
		},
	}); err != nil {
		return fmt.Errorf("sending hello: %w", err)
	}

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

	br, tc, ar, fc, sl := defaultRunners()

	projDirs, err := build.NewProjectDirs(pc.PrimaryPath())
	if err != nil {
		return fmt.Errorf("resolving build directories: %w", err)
	}
	preparer := build.NewPreparer(pc, projDirs, true, br)

	sm := NewStreamManager(pool, ew, pc, deviceSetPath, preparer, br, tc, ar, fc, sl, strict)

	// Start shared file watcher for all streams.
	watcher, err := watch.NewSharedWatcher(ctx, filepath.Dir(pc.PrimaryPath()), sl)
	if err != nil {
		return fmt.Errorf("creating shared file watcher: %w", err)
	}
	sm.watcher = watcher
	defer watcher.Close()

	// Read commands from stdin. When stdin closes (extension crash/exit),
	// the loop returns and we proceed to cleanup.
	runCommandLoop(ctx, os.Stdin, ew, sm)

	sm.StopAll()
	pool.GarbageCollect(ctx)

	return nil
}

// RunBuild executes only the xcodebuild build phase.
// This builds the project with the flags required for axe preview
// (dynamic replacement and private imports) without launching a simulator
// or compiling thunks. Useful for pre-warming the build cache.
func RunBuild(pc ProjectConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	dirs, err := build.NewProjectDirs(pc.PrimaryPath())
	if err != nil {
		return fmt.Errorf("resolving build directories: %w", err)
	}

	// Only the build runner is needed; no simulator, app, or toolchain operations.
	br := build.NewRunner()

	step := &stepper{total: 1}
	done := step.begin("Building...")
	// Always build (reuse=false): the purpose of this command is to populate
	// the build cache, so skipping the build would defeat its intent.
	result, err := build.Prepare(ctx, pc, dirs, false, br)
	done()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nBuild complete.\n")
	fmt.Fprintf(os.Stderr, "  Module:    %s\n", result.Settings.ModuleName)
	fmt.Fprintf(os.Stderr, "  Build dir: %s\n", dirs.Build)
	return nil
}
