package preview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	"github.com/k-kohey/axe/internal/preview/runner"
	"github.com/k-kohey/axe/internal/simruntime"
	"golang.org/x/sync/errgroup"
)

// SessionConfig holds parameters for creating a PreviewSession.
type SessionConfig struct {
	PC               build.ProjectConfig
	DeviceUDID       string
	DeviceType       string
	Runtime          string
	DeviceSetPath    string
	IsExternalDevice bool
	NoHeadless       bool
	Preparer         *build.Preparer
	ReuseBuild       bool

	BuildRunner    BuildRunner
	Toolchain      ToolchainRunner
	AppRunner      AppRunner
	Copier         FileCopier
	RuntimeManager simruntime.Manager
}

// CaptureRequest describes a single preview capture within an existing session.
type CaptureRequest struct {
	SourceFile      string
	PreviewSelector string
	OnReady         func(ctx context.Context, device, setPath string) error
}

// PreviewSession manages the Boot/Install lifecycle for a simulator,
// allowing multiple previews to be captured without repeating the full cycle.
//
// PreviewSession is NOT goroutine-safe. Callers must not call CapturePreview
// concurrently on the same session. Each parallel worker should create its
// own session.
type PreviewSession struct {
	cfg         SessionConfig
	dirs        previewDirs
	bs          *build.Settings
	loaderPath  string
	runtime     simruntime.Manager
	sessionID   string
	ownsRuntime bool

	// Hot-reload state (mutable, not goroutine-safe).
	reloadCounter int  // incremented after each successful reload/launch
	appLaunched   bool // true after first successful cold start
}

// NewPreviewSession creates a PreviewSession by running Build and Boot in parallel,
// then installing the app and compiling the loader.
func NewPreviewSession(ctx context.Context, cfg SessionConfig) (*PreviewSession, error) {
	runtime := cfg.RuntimeManager
	ownsRuntime := false
	if runtime == nil {
		var err error
		runtime, err = simruntime.New(
			simruntime.WithDeviceSetPath(cfg.DeviceSetPath),
			simruntime.WithSkipOrphanCleanup(cfg.DeviceSetPath == ""),
		)
		if err != nil {
			return nil, fmt.Errorf("creating simulator runtime: %w", err)
		}
		ownsRuntime = true
	}

	projDirs, err := build.NewProjectDirs(cfg.PC.PrimaryPath())
	if err != nil {
		return nil, fmt.Errorf("preview project dirs: %w", err)
	}

	// Parallel: Build + runtime session creation.
	g, gctx := errgroup.WithContext(ctx)

	var bs *build.Settings
	g.Go(func() error {
		var result *build.Result
		var bErr error
		if cfg.Preparer != nil {
			result, bErr = cfg.Preparer.Prepare(gctx)
		} else {
			result, bErr = build.Prepare(gctx, cfg.PC, projDirs, cfg.ReuseBuild, cfg.BuildRunner)
		}
		if bErr != nil {
			return fmt.Errorf("build: %w", bErr)
		}
		bs = result.Settings
		return nil
	})

	var info *simruntime.SessionInfo
	g.Go(func() error {
		req := simruntime.CreateSessionRequest{
			DeviceType: cfg.DeviceType,
			Runtime:    cfg.Runtime,
			DeviceUDID: cfg.DeviceUDID,
			NoHeadless: cfg.NoHeadless || cfg.IsExternalDevice,
		}
		var createErr error
		info, createErr = runtime.CreateSession(gctx, req)
		if createErr != nil {
			return fmt.Errorf("creating simulator session: %w", createErr)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		if info != nil {
			_ = runtime.StopSession(context.Background(), info.ID)
		}
		if ownsRuntime {
			runtime.Shutdown(context.Background())
		}
		return nil, err
	}

	dirs, err := newPreviewDirs(cfg.PC.PrimaryPath(), info.DeviceUDID)
	if err != nil {
		_ = runtime.StopSession(context.Background(), info.ID)
		if ownsRuntime {
			runtime.Shutdown(context.Background())
		}
		return nil, fmt.Errorf("preview dirs: %w", err)
	}

	// Sequential: Install + Loader (requires both Build result and Boot completion)
	appRunner := newSimRuntimeAppRunner(runtime, info.ID)
	terminateApp(ctx, bs, info.DeviceUDID, cfg.DeviceSetPath, appRunner)

	if _, err := installApp(ctx, bs, dirs, info.DeviceUDID, cfg.DeviceSetPath, appRunner, cfg.Copier); err != nil {
		_ = runtime.StopSession(context.Background(), info.ID)
		if ownsRuntime {
			runtime.Shutdown(context.Background())
		}
		return nil, fmt.Errorf("install: %w", err)
	}

	loaderPath, err := codegen.CompileLoader(ctx, dirs.Loader, bs.DeploymentTarget, cfg.Toolchain)
	if err != nil {
		_ = runtime.StopSession(context.Background(), info.ID)
		if ownsRuntime {
			runtime.Shutdown(context.Background())
		}
		return nil, fmt.Errorf("compile loader: %w", err)
	}

	cfg.DeviceUDID = info.DeviceUDID
	return &PreviewSession{
		cfg:         cfg,
		dirs:        dirs,
		bs:          bs,
		loaderPath:  loaderPath,
		runtime:     runtime,
		sessionID:   info.ID,
		ownsRuntime: ownsRuntime,
	}, nil
}

// CapturePreview compiles a main-only thunk for the given source file and
// delivers it to the running app. On the first call, a cold start (terminate →
// launch → WaitForReady → SendReloadCommand) is performed. The explicit reload
// after launch makes the initial preview mount deterministic instead of relying
// on the loader's best-effort startup hook. Subsequent calls use hot-reload via
// SendReloadCommand, falling back to cold start on failure.
func (s *PreviewSession) CapturePreview(ctx context.Context, req CaptureRequest) error {
	counter := s.reloadCounter
	dylibPath, err := compileMainOnlyPipeline(ctx, req.SourceFile, s.bs, s.dirs, req.PreviewSelector, counter, s.cfg.Toolchain)
	if err != nil {
		return fmt.Errorf("compile thunk: %w", err)
	}

	if s.appLaunched {
		if err := codegen.SendReloadCommand(ctx, s.dirs.Socket, dylibPath); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("hot-reload canceled: %w", err)
			}
			slog.Warn("Hot-reload failed, falling back to cold start", "err", err)
			s.appLaunched = false
		}
	}

	if !s.appLaunched {
		if err := s.coldStart(ctx, dylibPath); err != nil {
			return err
		}
	}

	// Increment counter AFTER successful reload/launch, before OnReady.
	// This ensures dlopen sees a unique path on retry (avoids cache hit).
	s.reloadCounter++
	cleanOldDylibs(s.dirs.Thunk, counter-1)

	if req.OnReady != nil {
		if err := req.OnReady(ctx, s.cfg.DeviceUDID, s.cfg.DeviceSetPath); err != nil {
			return fmt.Errorf("on-ready: %w", err)
		}
	}

	return nil
}

// coldStart terminates any running app, launches fresh, waits for the loader
// socket, then explicitly asks the loader to mount the preview dylib.
func (s *PreviewSession) coldStart(ctx context.Context, dylibPath string) error {
	appRunner := newSimRuntimeAppRunner(s.runtime, s.sessionID)
	terminateApp(ctx, s.bs, s.cfg.DeviceUDID, s.cfg.DeviceSetPath, appRunner)

	if err := launchWithHotReload(ctx, s.bs, s.loaderPath, dylibPath, s.dirs.Socket, s.cfg.DeviceUDID, s.cfg.DeviceSetPath, appRunner); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	if err := codegen.WaitForReady(ctx, s.dirs.Socket); err != nil {
		return fmt.Errorf("wait for ready: %w", err)
	}
	if err := codegen.SendReloadCommand(ctx, s.dirs.Socket, dylibPath); err != nil {
		return fmt.Errorf("initial reload: %w", err)
	}

	s.appLaunched = true
	return nil
}

func (s *PreviewSession) Screenshot(ctx context.Context) ([]byte, error) {
	return s.runtime.Screenshot(ctx, s.sessionID)
}

// Close terminates the app, removes the socket, and stops the runtime session.
func (s *PreviewSession) Close() {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if s.runtime != nil && s.sessionID != "" {
		if err := s.runtime.StopSession(cleanupCtx, s.sessionID); err != nil {
			slog.Debug("Failed to stop preview runtime session", "sessionId", s.sessionID, "err", err)
		}
	}

	if err := os.Remove(s.dirs.Socket); err != nil && !os.IsNotExist(err) {
		slog.Debug("Failed to remove socket", "path", s.dirs.Socket, "err", err)
	}

	if s.ownsRuntime && s.runtime != nil {
		s.runtime.Shutdown(cleanupCtx)
	}
}

// DefaultSessionRunners returns the production implementations of runner interfaces
// for use with SessionConfig. This keeps the report package from directly
// depending on the runner package.
func DefaultSessionRunners() (BuildRunner, ToolchainRunner, AppRunner, FileCopier) {
	return build.NewRunner(), &runner.Toolchain{}, &runner.App{}, &runner.FileCopy{}
}
