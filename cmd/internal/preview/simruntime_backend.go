package preview

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/k-kohey/axe/internal/preview/analysis"
	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/codegen"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/simruntime"
)

type simRuntimeAppRunner struct {
	manager   simruntime.Manager
	sessionID string
}

func newSimRuntimeAppRunner(manager simruntime.Manager, sessionID string) *simRuntimeAppRunner {
	return &simRuntimeAppRunner{manager: manager, sessionID: sessionID}
}

func (r *simRuntimeAppRunner) Terminate(ctx context.Context, _ string, bundleID, _ string) error {
	return r.manager.TerminateApp(ctx, r.sessionID, bundleID)
}

func (r *simRuntimeAppRunner) Install(ctx context.Context, _ string, appPath, _ string) error {
	return r.manager.InstallApp(ctx, r.sessionID, appPath)
}

func (r *simRuntimeAppRunner) Launch(ctx context.Context, _ string, bundleID, _ string, env map[string]string, args []string) error {
	return r.manager.LaunchApp(ctx, r.sessionID, bundleID, env, args)
}

type simRuntimeInputHandler struct {
	manager   simruntime.Manager
	sessionID string
}

func newSimRuntimeInputHandler(manager simruntime.Manager, sessionID string) *simRuntimeInputHandler {
	return &simRuntimeInputHandler{manager: manager, sessionID: sessionID}
}

func (h *simRuntimeInputHandler) HandleInput(ctx context.Context, input *pb.Input) {
	if h == nil || input == nil {
		return
	}
	if err := h.manager.SendInput(ctx, h.sessionID, inputToRuntimeEvent(input)); err != nil {
		slog.Warn("simruntime input failed", "sessionId", h.sessionID, "err", err)
	}
}

func (h *simRuntimeInputHandler) HandleTap(ctx context.Context, x, y float64) {
	if h == nil {
		return
	}
	h.sendRuntimeInput(ctx, simruntime.InputEvent{TouchDown: &simruntime.Point{X: x, Y: y}})
	h.sendRuntimeInput(ctx, simruntime.InputEvent{TouchUp: &simruntime.Point{X: x, Y: y}})
}

func (h *simRuntimeInputHandler) HandleSwipe(ctx context.Context, startX, startY, endX, endY, _ float64) {
	if h == nil {
		return
	}
	h.sendRuntimeInput(ctx, simruntime.InputEvent{TouchDown: &simruntime.Point{X: startX, Y: startY}})
	h.sendRuntimeInput(ctx, simruntime.InputEvent{TouchMove: &simruntime.Point{X: endX, Y: endY}})
	h.sendRuntimeInput(ctx, simruntime.InputEvent{TouchUp: &simruntime.Point{X: endX, Y: endY}})
}

func (h *simRuntimeInputHandler) sendRuntimeInput(ctx context.Context, input simruntime.InputEvent) {
	if err := h.manager.SendInput(ctx, h.sessionID, input); err != nil {
		slog.Warn("simruntime input failed", "sessionId", h.sessionID, "err", err)
	}
}

func inputToRuntimeEvent(input *pb.Input) simruntime.InputEvent {
	switch {
	case input.GetTouchDown() != nil:
		p := input.GetTouchDown()
		return simruntime.InputEvent{TouchDown: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetTouchMove() != nil:
		p := input.GetTouchMove()
		return simruntime.InputEvent{TouchMove: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetTouchUp() != nil:
		p := input.GetTouchUp()
		return simruntime.InputEvent{TouchUp: &simruntime.Point{X: p.GetX(), Y: p.GetY()}}
	case input.GetText() != nil:
		return simruntime.InputEvent{Text: input.GetText().GetValue()}
	default:
		return simruntime.InputEvent{}
	}
}

func (sm *StreamManager) simruntimeStreamLauncher(ctx context.Context, s *stream) {
	sendStatus := func(phase string) {
		if err := sm.ew.Send(&pb.Event{StreamId: s.id, Payload: &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: phase}}}); err != nil {
			slog.Warn("Failed to send StreamStatus", "streamId", s.id, "phase", phase, "err", err)
		}
	}

	sendStatus("booting")
	info, err := sm.runtime.CreateSession(ctx, simruntime.CreateSessionRequest{
		DeviceType: s.deviceType,
		Runtime:    s.runtime,
	})
	if err != nil {
		s.sendStopped(sm.ew, "resource_error", fmt.Sprintf("creating simruntime session: %v", err), "")
		return
	}
	s.runtimeSessionID = info.ID
	s.deviceUDID = info.DeviceUDID
	s.appRunner = newSimRuntimeAppRunner(sm.runtime, info.ID)
	s.hid = newSimRuntimeInputHandler(sm.runtime, info.ID)

	dirs, err := newPreviewDirs(sm.pc.PrimaryPath(), info.DeviceUDID)
	if err != nil {
		s.sendStopped(sm.ew, "resource_error", err.Error(), "")
		return
	}
	s.dirs = dirs

	prepared, err := sm.preparer.Prepare(ctx)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", "Build failed", err.Error())
		return
	}
	bs := prepared.Settings.Clone()
	if prepared.Built {
		sendStatus("building")
	} else {
		sendStatus("reusing_build")
	}

	depGraph, trackedFiles, dylibPath, degraded, err := sm.compileInitialThunk(ctx, s, bs, prepared.Built, sendStatus)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}
	s.degraded = degraded

	sendStatus("installing")
	terminateApp(ctx, bs, info.DeviceUDID, sm.deviceSetPath, s.appRunner)
	if _, err := installApp(ctx, bs, s.dirs, info.DeviceUDID, sm.deviceSetPath, s.appRunner, sm.copier); err != nil {
		s.sendStopped(sm.ew, "install_error", err.Error(), "")
		return
	}

	loaderPath, err := codegen.CompileLoader(ctx, s.dirs.Loader, bs.DeploymentTarget, sm.toolchain)
	if err != nil {
		s.sendStopped(sm.ew, "build_error", err.Error(), "")
		return
	}
	s.loaderPath = loaderPath

	sendStatus("running")
	if err := launchWithHotReload(ctx, bs, loaderPath, dylibPath, s.dirs.Socket, info.DeviceUDID, sm.deviceSetPath, s.appRunner); err != nil {
		s.sendStopped(sm.ew, "runtime_error", err.Error(), "")
		return
	}

	previewCount := 0
	if blocks, parseErr := analysis.PreviewBlocks(s.file); parseErr == nil {
		previewCount = len(blocks)
	}
	if err := sm.ew.Send(&pb.Event{
		StreamId: s.id,
		Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: int32(previewCount)}},
	}); err != nil {
		slog.Warn("Failed to send StreamStarted", "streamId", s.id, "err", err)
	}

	idbErrCh := make(chan error, 1)
	go relaySimRuntimeVideo(ctx, sm.runtime, info.ID, sm.ew, s.id, info.DeviceUDID, s.file, idbErrCh)
	go relaySimRuntimeEvents(ctx, sm.runtime, info.ID, idbErrCh)

	if s.degraded {
		sendStatus("degraded")
		if err := runDegradedStreamLoop(ctx, s, sm, idbErrCh); err != nil {
			slog.Info("Degraded stream loop exited", "streamId", s.id, "err", err)
		}
		return
	}

	initialLastUsed := make(map[string]int64, len(trackedFiles))
	for i, f := range trackedFiles {
		initialLastUsed[filepath.Clean(f)] = int64(i + 1)
	}
	s.ws = &watchState{
		reloadCounter:   1,
		previewSelector: "0",
		previewIndex:    0,
		previewCount:    previewCount,
		skeletonMap:     buildSkeletonMap(trackedFiles),
		trackedFiles:    trackedFiles,
		depGraph:        depGraph,
		indexCache:      sm.indexCache,
		maxThunkFiles:   sm.maxThunkFiles,
		preThunkDepth:   sm.preThunkDepth,
		usageTick:       int64(len(trackedFiles)),
		lastUsed:        initialLastUsed,
	}

	if sm.watcher != nil {
		sm.watcher.AddListener(s.id, s.fileChangeCh)
	}

	if err := runStreamLoop(ctx, s, sm, bs, idbErrCh); err != nil {
		slog.Info("Stream loop exited", "streamId", s.id, "err", err)
	}
}

func (sm *StreamManager) compileInitialThunk(ctx context.Context, s *stream, bs *build.Settings, builtThisLaunch bool, sendStatus func(string)) (*analysis.DependencyGraph, []string, string, bool, error) {
	var depGraph *analysis.DependencyGraph
	var trackedFiles []string

	compileAttempt := func() (string, error) {
		sendStatus("compiling_thunk")
		projectRoot := filepath.Dir(sm.pc.PrimaryPath())
		cache, cacheErr := analysis.LoadIndexStore(ctx, s.dirs.IndexStorePath(), projectRoot)
		if cacheErr != nil && ctx.Err() == nil {
			slog.Warn("Index store cache unavailable for stream", "streamId", s.id, "err", cacheErr)
		}
		sm.indexCache.Set(cache)

		dg, _, err := analysis.ResolveTransitiveDependencies(ctx, s.file, sm.indexCache.Get())
		if err != nil && ctx.Err() == nil {
			slog.Warn("Failed to resolve dependencies, proceeding with target only", "streamId", s.id, "err", err)
		}
		tf := []string{s.file}
		if dg != nil {
			tf = append(tf, dg.DepsUpTo(sm.preThunkDepth)...)
		}

		files, filtered, err := parseAndFilterTrackedFiles(s.file, tf, sm.indexCache.Get())
		if err != nil {
			return "", err
		}
		thunkPaths, err := codegen.GenerateThunks(files, bs.ModuleName, s.dirs.Thunk, "0", s.file, 0)
		if err != nil {
			return "", err
		}
		dylibPath, err := codegen.CompileThunk(ctx, thunkPaths, compileConfigFromSettings(bs), s.dirs.Thunk, s.dirs.Build, 0, s.file, sm.toolchain)
		if err != nil {
			return "", err
		}
		depGraph = dg
		trackedFiles = filtered
		return dylibPath, nil
	}

	strategy := NewCompileStrategy(true, true, false, sm.strict)
	result, err := ExecuteCompileStrategy(ctx, strategy, map[CompileMode]CompileFunc{
		CompileModeFull: func(context.Context) (string, error) {
			dylibPath, err := compileAttempt()
			if err != nil && !builtThisLaunch {
				slog.Info("Optimistic launch failed; rebuilding and retrying once", "streamId", s.id, "err", err)
				sendStatus("building")
				if buildErr := build.Run(ctx, sm.pc, s.dirs.ProjectDirs, sm.build); buildErr != nil {
					return "", fmt.Errorf("build failed: %w", buildErr)
				}
				build.ExtractCompilerPaths(ctx, bs, s.dirs.ProjectDirs)
				dylibPath, err = compileAttempt()
			}
			return dylibPath, err
		},
		CompileModeMainOnly: func(context.Context) (string, error) {
			return compileMainOnlyPipeline(ctx, s.file, bs, s.dirs, "0", 0, sm.toolchain)
		},
	})
	if err != nil {
		return nil, nil, "", false, err
	}
	if trackedFiles == nil {
		trackedFiles = []string{s.file}
	}
	return depGraph, trackedFiles, result.DylibPath, result.Degraded, nil
}

func relaySimRuntimeVideo(ctx context.Context, manager simruntime.Manager, sessionID string, ew eventSender, streamID, device, file string, errCh chan<- error) {
	frames, err := manager.WatchVideo(ctx, sessionID, 30)
	if err != nil {
		reportSimRuntimeRelayError(ctx, errCh, err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				reportSimRuntimeRelayError(ctx, errCh, fmt.Errorf("runtime video stream ended unexpectedly"))
				return
			}
			if ew == nil {
				continue
			}
			encoded := base64.StdEncoding.EncodeToString(frame.JPEG)
			if err := ew.Send(&pb.Event{
				StreamId: streamID,
				Payload:  &pb.Event_Frame{Frame: &pb.Frame{Device: device, File: file, Data: encoded}},
			}); err != nil {
				reportSimRuntimeRelayError(ctx, errCh, fmt.Errorf("frame send: %w", err))
				return
			}
		}
	}
}

func relaySimRuntimeEvents(ctx context.Context, manager simruntime.Manager, sessionID string, errCh chan<- error) {
	events, err := manager.SubscribeEvents(ctx, sessionID)
	if err != nil {
		reportSimRuntimeRelayError(ctx, errCh, err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				reportSimRuntimeRelayError(ctx, errCh, fmt.Errorf("runtime event stream ended unexpectedly"))
				return
			}
			if event.Error != nil {
				reportSimRuntimeRelayError(ctx, errCh, fmt.Errorf("runtime event error: %s", event.Error.Message))
				return
			}
			if event.Stopped != nil {
				msg := event.Stopped.Message
				if msg == "" {
					msg = event.Stopped.Reason
				}
				reportSimRuntimeRelayError(ctx, errCh, fmt.Errorf("runtime stopped: %s", msg))
				return
			}
		}
	}
}

func reportSimRuntimeRelayError(ctx context.Context, errCh chan<- error, err error) {
	select {
	case errCh <- err:
	case <-ctx.Done():
	}
}

type eventSender interface {
	Send(*pb.Event) error
}
