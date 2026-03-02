package preview

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	"github.com/k-kohey/axe/internal/preview/runner"
	"golang.org/x/sync/errgroup"
)

// SessionConfig holds parameters for creating a PreviewSession.
type SessionConfig struct {
	PC               build.ProjectConfig
	DeviceUDID       string
	DeviceSetPath    string
	IsExternalDevice bool
	NoHeadless       bool
	Preparer         *build.Preparer
	ReuseBuild       bool

	BuildRunner BuildRunner
	Toolchain   ToolchainRunner
	AppRunner   AppRunner
	Copier      FileCopier

	// BootFunc overrides the default boot function for testing.
	// When nil, bootWithRetry is used for axe-managed devices,
	// or simctl.Boot for external devices.
	BootFunc func(ctx context.Context, udid, setPath string, headless bool) (companionProcess, error)
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
	cfg           SessionConfig
	dirs          previewDirs
	bs            *build.Settings
	bootCompanion companionProcess // nil for external devices
	loaderPath    string

	// Hot-reload state (mutable, not goroutine-safe).
	reloadCounter int  // incremented after each successful reload/launch
	appLaunched   bool // true after first successful cold start
}

// NewPreviewSession creates a PreviewSession by running Build and Boot in parallel,
// then installing the app and compiling the loader.
func NewPreviewSession(ctx context.Context, cfg SessionConfig) (*PreviewSession, error) {
	dirs, err := newPreviewDirs(cfg.PC.PrimaryPath(), cfg.DeviceUDID)
	if err != nil {
		return nil, fmt.Errorf("preview dirs: %w", err)
	}

	// Parallel: Build + Boot
	g, gctx := errgroup.WithContext(ctx)

	var bs *build.Settings
	g.Go(func() error {
		var result *build.Result
		var bErr error
		if cfg.Preparer != nil {
			result, bErr = cfg.Preparer.Prepare(gctx)
		} else {
			result, bErr = build.Prepare(gctx, cfg.PC, dirs.ProjectDirs, cfg.ReuseBuild, cfg.BuildRunner)
		}
		if bErr != nil {
			return fmt.Errorf("build: %w", bErr)
		}
		bs = result.Settings
		return nil
	})

	var bootComp companionProcess
	g.Go(func() error {
		if cfg.IsExternalDevice {
			simctl := &platform.RealSimctlRunner{}
			bootCtx, bootCancel := context.WithTimeout(gctx, 30*time.Second)
			defer bootCancel()
			if bErr := simctl.Boot(bootCtx, cfg.DeviceUDID); bErr != nil {
				return fmt.Errorf("booting simulator (external): %w", bErr)
			}
			return nil
		}
		if cfg.BootFunc != nil {
			var bErr error
			bootComp, bErr = cfg.BootFunc(gctx, cfg.DeviceUDID, cfg.DeviceSetPath, !cfg.NoHeadless)
			if bErr != nil {
				return fmt.Errorf("booting simulator: %w", bErr)
			}
			return nil
		}
		comp, bErr := bootWithRetry(gctx, cfg.DeviceUDID, cfg.DeviceSetPath, !cfg.NoHeadless)
		if bErr != nil {
			return fmt.Errorf("booting simulator: %w", bErr)
		}
		bootComp = comp
		return nil
	})

	if err := g.Wait(); err != nil {
		// Prevent companion leak on partial failure.
		if bootComp != nil {
			if stopErr := bootComp.Stop(); stopErr != nil {
				slog.Debug("Failed to stop boot companion after session init failure", "err", stopErr)
			}
		}
		return nil, err
	}

	// Verify the simulator didn't crash immediately after boot.
	if bootComp != nil {
		select {
		case <-bootComp.Done():
			return nil, fmt.Errorf("simulator crashed immediately after boot: %w", bootComp.Err())
		default:
		}
	}

	// Sequential: Install + Loader (requires both Build result and Boot completion)
	terminateApp(ctx, bs, cfg.DeviceUDID, cfg.DeviceSetPath, cfg.AppRunner)

	if _, err := installApp(ctx, bs, dirs, cfg.DeviceUDID, cfg.DeviceSetPath, cfg.AppRunner, cfg.Copier); err != nil {
		if bootComp != nil {
			if stopErr := bootComp.Stop(); stopErr != nil {
				slog.Debug("Failed to stop boot companion after install failure", "err", stopErr)
			}
		}
		return nil, fmt.Errorf("install: %w", err)
	}

	loaderPath, err := codegen.CompileLoader(ctx, dirs.Loader, bs.DeploymentTarget, cfg.Toolchain)
	if err != nil {
		if bootComp != nil {
			if stopErr := bootComp.Stop(); stopErr != nil {
				slog.Debug("Failed to stop boot companion after loader compile failure", "err", stopErr)
			}
		}
		return nil, fmt.Errorf("compile loader: %w", err)
	}

	return &PreviewSession{
		cfg:           cfg,
		dirs:          dirs,
		bs:            bs,
		bootCompanion: bootComp,
		loaderPath:    loaderPath,
	}, nil
}

// CapturePreview compiles a main-only thunk for the given source file and
// delivers it to the running app. On the first call, a cold start (terminate →
// launch → WaitForReady) is performed. Subsequent calls use hot-reload via
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

// coldStart terminates any running app, launches fresh with the given dylib,
// and waits for the loader socket to become ready.
func (s *PreviewSession) coldStart(ctx context.Context, dylibPath string) error {
	terminateApp(ctx, s.bs, s.cfg.DeviceUDID, s.cfg.DeviceSetPath, s.cfg.AppRunner)

	if err := launchWithHotReload(ctx, s.bs, s.loaderPath, dylibPath, s.dirs.Socket, s.cfg.DeviceUDID, s.cfg.DeviceSetPath, s.cfg.AppRunner); err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	if err := codegen.WaitForReady(ctx, s.dirs.Socket); err != nil {
		return fmt.Errorf("wait for ready: %w", err)
	}

	s.appLaunched = true
	return nil
}

// Close terminates the app, removes the socket, and stops the boot companion.
func (s *PreviewSession) Close() {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	terminateApp(cleanupCtx, s.bs, s.cfg.DeviceUDID, s.cfg.DeviceSetPath, s.cfg.AppRunner)

	if err := os.Remove(s.dirs.Socket); err != nil && !os.IsNotExist(err) {
		slog.Debug("Failed to remove socket", "path", s.dirs.Socket, "err", err)
	}

	if s.bootCompanion != nil {
		if err := s.bootCompanion.Stop(); err != nil {
			slog.Debug("Failed to stop boot companion", "err", err)
		}
	}
}

// DefaultSessionRunners returns the production implementations of runner interfaces
// for use with SessionConfig. This keeps the report package from directly
// depending on the runner package.
func DefaultSessionRunners() (BuildRunner, ToolchainRunner, AppRunner, FileCopier) {
	return build.NewRunner(), &runner.Toolchain{}, &runner.App{}, &runner.FileCopy{}
}
