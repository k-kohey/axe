package preview

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/preview/runner"
	"github.com/k-kohey/axe/internal/preview/watch"
	"github.com/k-kohey/axe/internal/simruntime"
)

type serveDeps struct {
	in         io.Reader
	out        io.Writer
	runners    func() (BuildRunner, ToolchainRunner, AppRunner, FileCopier, SourceLister)
	newRuntime func(deviceSetPath string) (simruntime.Manager, error)
	newWatcher func(context.Context, string, SourceLister) (*watch.SharedWatcher, error)
}

func defaultServeDeps() serveDeps {
	return serveDeps{
		in:      os.Stdin,
		out:     os.Stdout,
		runners: defaultRunners,
		newRuntime: func(deviceSetPath string) (simruntime.Manager, error) {
			return simruntime.New(simruntime.WithDeviceSetPath(deviceSetPath))
		},
		newWatcher: func(ctx context.Context, watchRoot string, sl SourceLister) (*watch.SharedWatcher, error) {
			return watch.NewSharedWatcher(ctx, watchRoot, sl)
		},
	}
}

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

			dg, _, err := analysis.ResolveTransitiveDependencies(ctx, opts.SourceFile, indexCache.Get())
			if err != nil && ctx.Err() == nil {
				slog.Warn("Failed to resolve dependencies, proceeding with target only", "err", err)
			}
			depGraph = dg

			tf := []string{opts.SourceFile}
			if dg != nil {
				tf = append(tf, dg.DepsUpTo(opts.PreThunkDepth)...)
			}
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

	// Create a runtime session.
	sendStatus("booting")
	done = step.begin("Creating simulator session...")
	runtime, err := simruntime.New(
		simruntime.WithDeviceSetPath(deviceSetPath),
		simruntime.WithSkipOrphanCleanup(deviceSetPath == ""),
	)
	if err != nil {
		done()
		sendStopped("resource_error", fmt.Sprintf("creating simulator runtime: %v", err), "")
		return fmt.Errorf("creating simulator runtime: %w", err)
	}
	sessionInfo, err := runtime.CreateSession(ctx, simruntime.CreateSessionRequest{
		DeviceUDID: device,
		NoHeadless: opts.NoHeadless || isExternalDevice,
	})
	done()
	if err != nil {
		runtime.Shutdown(context.Background())
		sendStopped("boot_error", fmt.Sprintf("creating simulator session: %v", err), "")
		return fmt.Errorf("creating simulator session: %w", err)
	}
	device = sessionInfo.DeviceUDID
	runtimeApp := newSimRuntimeAppRunner(runtime, sessionInfo.ID)
	runtimeHID := newSimRuntimeInputHandler(runtime, sessionInfo.ID)

	// Shared cleanup: runs on normal return, error return, and signal-triggered return.
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
		if err := runtime.StopSession(cleanupCtx, sessionInfo.ID); err != nil {
			slog.Debug("Failed to stop simulator runtime session", "sessionId", sessionInfo.ID, "err", err)
		}
		runtime.Shutdown(cleanupCtx)
	}()

	terminateApp(ctx, bs, device, deviceSetPath, runtimeApp)

	sendStatus("installing")
	done = step.begin("Installing app on simulator...")
	_, err = installApp(ctx, bs, dirs, device, deviceSetPath, runtimeApp, fc)
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
	err = launchWithHotReload(ctx, bs, loaderPath, dylibPath, dirs.Socket, device, deviceSetPath, runtimeApp)
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

	var idbErrCh chan error
	if opts.Serve {
		streamCtx, cancel := context.WithCancel(context.Background())
		cancelStream = cancel
		idbErrCh = make(chan error, 1)
		go relaySimRuntimeVideo(streamCtx, runtime, sessionInfo.ID, ew, defaultStreamID, device, opts.SourceFile, idbErrCh)
		go relaySimRuntimeEvents(streamCtx, runtime, sessionInfo.ID, idbErrCh)
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
		select {
		case <-ctx.Done():
			return nil
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
		app:           runtimeApp,
		copier:        fc,
		sources:       sl,
	}

	initialIndex := 0
	if idx, err := strconv.Atoi(opts.PreviewSelector); err == nil {
		initialIndex = idx
	}

	// Initialize LRU state for all tracked files.
	initialLastUsed := make(map[string]int64, len(trackedFiles))
	for i, f := range trackedFiles {
		initialLastUsed[filepath.Clean(f)] = int64(i + 1)
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
		maxThunkFiles:   opts.MaxThunkFiles,
		preThunkDepth:   opts.PreThunkDepth,
		usageTick:       int64(len(trackedFiles)),
		lastUsed:        initialLastUsed,
	}

	fmt.Fprintln(os.Stderr, "Preview launched with hot-reload support.")
	return runWatcher(ctx, opts.SourceFile, opts.PC, bs, dirs, wctx, ws, runtimeHID, idbErrCh, nil)
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
	if err == nil && opts.OnScreenshot != nil {
		var data []byte
		data, err = sess.Screenshot(ctx)
		if err == nil {
			err = opts.OnScreenshot(ctx, data)
		}
	}
	done()
	return err
}

// RunServe is the multi-stream entry point for serve mode.
// It reads AddStream/RemoveStream commands from stdin and manages
// multiple preview streams concurrently via StreamManager.
func RunServe(pc ProjectConfig, strict bool, maxThunkFiles, preThunkDepth int) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	return runServeWithDeps(ctx, pc, strict, maxThunkFiles, preThunkDepth, defaultServeDeps())
}

func runServeWithDeps(ctx context.Context, pc ProjectConfig, strict bool, maxThunkFiles, preThunkDepth int, deps serveDeps) error {
	ew := protocol.NewEventWriter(deps.out)

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

	runtime, err := deps.newRuntime(deviceSetPath)
	if err != nil {
		return fmt.Errorf("creating simulator runtime: %w", err)
	}

	br, tc, ar, fc, sl := deps.runners()

	projDirs, err := build.NewProjectDirs(pc.PrimaryPath())
	if err != nil {
		return fmt.Errorf("resolving build directories: %w", err)
	}
	preparer := build.NewPreparer(pc, projDirs, true, br)

	sm := NewRuntimeStreamManager(runtime, ew, pc, deviceSetPath, preparer, br, tc, ar, fc, sl, strict, maxThunkFiles, preThunkDepth)

	// Start shared file watcher for all streams.
	watcher, err := deps.newWatcher(ctx, filepath.Dir(pc.PrimaryPath()), sl)
	if err != nil {
		return fmt.Errorf("creating shared file watcher: %w", err)
	}
	sm.watcher = watcher
	defer watcher.Close()

	// Read commands from stdin. When stdin closes (extension crash/exit),
	// the loop returns and we proceed to cleanup.
	runCommandLoop(ctx, deps.in, ew, sm)

	sm.StopAll()

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
