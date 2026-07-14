package preview

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k-kohey/axe/internal/preview/build"
	pb "github.com/k-kohey/axe/internal/preview/previewproto"
	"github.com/k-kohey/axe/internal/preview/protocol"
	"github.com/k-kohey/axe/internal/simruntime"
)

type fakeSimRuntimeManager struct {
	mu sync.Mutex

	createReqs []simruntime.CreateSessionRequest
	install    []string
	launches   []runtimeLaunchCall
	inputs     []simruntime.InputEvent
	stopped    []string
	shutdown   bool
	screenshot []byte

	frames        chan simruntime.VideoFrame
	events        chan simruntime.Event
	watchVideoErr error
	createErr     error
	installErr    error
}

type runtimeLaunchCall struct {
	sessionID string
	bundleID  string
	env       map[string]string
	args      []string
}

func newFakeSimRuntimeManager() *fakeSimRuntimeManager {
	return &fakeSimRuntimeManager{
		frames: make(chan simruntime.VideoFrame, 1),
		events: make(chan simruntime.Event, 1),
	}
}

func (m *fakeSimRuntimeManager) ListDevices(context.Context) ([]simruntime.DeviceType, error) {
	return nil, nil
}

func (m *fakeSimRuntimeManager) ListManagedDevices(context.Context) ([]simruntime.ManagedDevice, error) {
	return nil, nil
}

func (m *fakeSimRuntimeManager) AddManagedDevice(context.Context, simruntime.AddManagedDeviceRequest) (*simruntime.ManagedDevice, error) {
	return nil, nil
}

func (m *fakeSimRuntimeManager) RemoveManagedDevice(context.Context, string) error {
	return nil
}

func (m *fakeSimRuntimeManager) SetDefaultManagedDevice(context.Context, string) error {
	return nil
}

func (m *fakeSimRuntimeManager) CreateSession(_ context.Context, req simruntime.CreateSessionRequest) (*simruntime.SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.createReqs = append(m.createReqs, req)
	udid := req.DeviceUDID
	if udid == "" {
		udid = "SIMRUNTIME-UDID"
	}
	return &simruntime.SessionInfo{
		ID:           "session-1",
		DeviceUDID:   udid,
		DeviceType:   req.DeviceType,
		Runtime:      req.Runtime,
		State:        simruntime.SessionRunning,
		ScreenWidth:  390,
		ScreenHeight: 844,
	}, nil
}

func (m *fakeSimRuntimeManager) ListSessions() []*simruntime.SessionInfo {
	return nil
}

func (m *fakeSimRuntimeManager) GetSession(string) (*simruntime.SessionInfo, error) {
	return nil, nil
}

func (m *fakeSimRuntimeManager) StopSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, id)
	return nil
}

func (m *fakeSimRuntimeManager) InstallApp(_ context.Context, _ string, appPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.install = append(m.install, appPath)
	return m.installErr
}

func (m *fakeSimRuntimeManager) LaunchApp(_ context.Context, id, bundleID string, env map[string]string, args []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launches = append(m.launches, runtimeLaunchCall{
		sessionID: id,
		bundleID:  bundleID,
		env:       env,
		args:      args,
	})
	return nil
}

func (m *fakeSimRuntimeManager) TerminateApp(context.Context, string, string) error {
	return nil
}

func (m *fakeSimRuntimeManager) SendInput(_ context.Context, _ string, input simruntime.InputEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, input)
	return nil
}

func (m *fakeSimRuntimeManager) Screenshot(context.Context, string) ([]byte, error) {
	if m.screenshot != nil {
		return m.screenshot, nil
	}
	return []byte("fake-png"), nil
}

func (m *fakeSimRuntimeManager) SubscribeEvents(context.Context, string) (<-chan simruntime.Event, error) {
	return m.events, nil
}

func (m *fakeSimRuntimeManager) WatchVideo(ctx context.Context, id string, _ int) (<-chan simruntime.VideoFrame, error) {
	if m.watchVideoErr != nil {
		return nil, m.watchVideoErr
	}
	out := make(chan simruntime.VideoFrame)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-m.frames:
				if !ok {
					return
				}
				frame.SessionID = id
				out <- frame
			}
		}
	}()
	return out, nil
}

func (m *fakeSimRuntimeManager) Shutdown(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdown = true
}

func TestStreamManagerWithSimRuntimeRoutesLifecycleVideoInputAndStop(t *testing.T) {
	tmpDir := t.TempDir()
	pc, bs, _ := setupSimRuntimeStreamTestProject(t, tmpDir)
	br := &fakeBuildRunner{}
	preparer := sessionPreparer(t, pc, filepath.Join(tmpDir, "build"), bs)
	runtime := newFakeSimRuntimeManager()

	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := NewRuntimeStreamManager(runtime, ew, pc, filepath.Join(tmpDir, "device-set"),
		preparer, br, &sessionToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false, 32, 0)
	useFastSimRuntimeLauncher(t, sm, bs)

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       filepath.Join(tmpDir, "ContentView.swift"),
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, &buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStarted != nil
	})

	runtime.frames <- simruntime.VideoFrame{JPEG: []byte("jpeg-bytes"), Width: 390, Height: 844}
	waitForParsedEvent(t, &buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.Frame != nil
	})

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_Input{Input: &pb.Input{
			Event: &pb.Input_Text{Text: &pb.TextEvent{Value: "hello"}},
		}},
	})

	waitForCondition(t, 2*time.Second, func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return len(runtime.inputs) == 1
	})

	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload:  &pb.Command_RemoveStream{RemoveStream: &pb.RemoveStream{}},
	})

	waitForParsedEvent(t, &buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.createReqs) != 1 {
		t.Fatalf("CreateSession calls = %d, want 1", len(runtime.createReqs))
	}
	if runtime.createReqs[0].DeviceType != "iPhone-16-Pro" || runtime.createReqs[0].Runtime != "iOS-18-2" {
		t.Fatalf("CreateSession request = %+v", runtime.createReqs[0])
	}
	if len(runtime.install) != 1 || runtime.install[0] == "" {
		t.Fatalf("InstallApp calls = %+v", runtime.install)
	}
	if len(runtime.launches) != 1 {
		t.Fatalf("LaunchApp calls = %d, want 1", len(runtime.launches))
	}
	wantBundleID := "axe." + bs.BundleID
	if runtime.launches[0].bundleID != wantBundleID {
		t.Fatalf("launched bundle id = %q, want %q", runtime.launches[0].bundleID, wantBundleID)
	}
	if runtime.launches[0].env["SIMCTL_CHILD_AXE_PREVIEW_SOCKET_PATH"] == "" {
		t.Fatalf("launch env missing hot-reload socket: %+v", runtime.launches[0].env)
	}
	if runtime.inputs[0].Text != "hello" {
		t.Fatalf("input = %+v, want text hello", runtime.inputs[0])
	}
	if len(runtime.stopped) != 1 || runtime.stopped[0] != "session-1" {
		t.Fatalf("StopSession calls = %+v", runtime.stopped)
	}

	events := filterEvents(collectEvents(t, &buf), "stream-a")
	var foundFrame bool
	for _, event := range events {
		if event.Frame != nil && event.Frame["data"] == base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")) {
			foundFrame = true
		}
	}
	if !foundFrame {
		t.Fatalf("expected relayed frame event, got %+v", events)
	}
}

func TestSimRuntimeInputHandlerTapAndSwipeUseRuntimeEvents(t *testing.T) {
	runtime := newFakeSimRuntimeManager()
	handler := newSimRuntimeInputHandler(runtime, "session-1")

	handler.HandleTap(context.Background(), 0.5, 0.25)
	handler.HandleSwipe(context.Background(), 0.1, 0.2, 0.8, 0.9, 0.5)

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.inputs) != 5 {
		t.Fatalf("runtime inputs = %d, want 5", len(runtime.inputs))
	}
	if runtime.inputs[0].TouchDown == nil || runtime.inputs[1].TouchUp == nil {
		t.Fatalf("tap inputs = %+v, %+v; want touch down/up", runtime.inputs[0], runtime.inputs[1])
	}
	if runtime.inputs[2].TouchDown == nil || runtime.inputs[3].TouchMove == nil || runtime.inputs[4].TouchUp == nil {
		t.Fatalf("swipe inputs = %+v, %+v, %+v; want touch down/move/up", runtime.inputs[2], runtime.inputs[3], runtime.inputs[4])
	}
}

func TestStreamManagerWithSimRuntimeStopsOnRuntimeErrorEvent(t *testing.T) {
	runtime, buf, sm, sourceFile := newSimRuntimeStreamManagerFixture(t)

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       sourceFile,
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStarted != nil
	})
	runtime.events <- simruntime.Event{SessionID: "session-1", Error: &simruntime.ErrorEvent{Message: "idb died"}}
	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})
	waitForCondition(t, 2*time.Second, func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return len(runtime.stopped) == 1
	})

	events := filterEvents(collectEvents(t, buf), "stream-a")
	var foundStopped bool
	for _, event := range events {
		if event.StreamStopped != nil {
			foundStopped = true
			if msg, _ := event.StreamStopped["message"].(string); msg == "" {
				t.Fatalf("StreamStopped missing message: %+v", event.StreamStopped)
			}
		}
	}
	if !foundStopped {
		t.Fatalf("expected StreamStopped after runtime error event, got %+v", events)
	}
}

func TestStreamManagerWithSimRuntimeStopsWhenWatchVideoFails(t *testing.T) {
	runtime, buf, sm, sourceFile := newSimRuntimeStreamManagerFixture(t)
	runtime.watchVideoErr = errors.New("video unavailable")

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       sourceFile,
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})
	waitForCondition(t, 2*time.Second, func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return len(runtime.stopped) == 1
	})

	events := filterEvents(collectEvents(t, buf), "stream-a")
	var foundStopped bool
	for _, event := range events {
		if event.StreamStopped != nil {
			foundStopped = true
		}
	}
	if !foundStopped {
		t.Fatalf("expected StreamStopped after WatchVideo failure, got %+v", events)
	}
}

func TestStreamManagerWithSimRuntimeStopsWhenVideoStreamEndsUnexpectedly(t *testing.T) {
	runtime, buf, sm, sourceFile := newSimRuntimeStreamManagerFixture(t)

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       sourceFile,
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStarted != nil
	})
	close(runtime.frames)
	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})

	assertRuntimeStoppedOnce(t, runtime)
	assertStoppedMessageContains(t, buf, "video stream ended unexpectedly")
}

func TestStreamManagerWithSimRuntimeStopsWhenEventStreamEndsUnexpectedly(t *testing.T) {
	runtime, buf, sm, sourceFile := newSimRuntimeStreamManagerFixture(t)

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       sourceFile,
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStarted != nil
	})
	close(runtime.events)
	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})

	assertRuntimeStoppedOnce(t, runtime)
	assertStoppedMessageContains(t, buf, "event stream ended unexpectedly")
}

func TestStreamManagerWithSimRuntimeStopsSessionAfterInstallFailure(t *testing.T) {
	runtime, buf, sm, sourceFile := newSimRuntimeStreamManagerFixture(t)
	runtime.installErr = errors.New("install failed")

	ctx := t.Context()
	sm.HandleCommand(ctx, &pb.Command{
		StreamId: "stream-a",
		Payload: &pb.Command_AddStream{AddStream: &pb.AddStream{
			File:       sourceFile,
			DeviceType: "iPhone-16-Pro",
			Runtime:    "iOS-18-2",
		}},
	})

	waitForParsedEvent(t, buf, 2*time.Second, func(e parsedEvent) bool {
		return e.StreamID == "stream-a" && e.StreamStopped != nil
	})
	waitForCondition(t, 2*time.Second, func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return len(runtime.stopped) == 1
	})

	events := filterEvents(collectEvents(t, buf), "stream-a")
	var foundInstallError bool
	for _, event := range events {
		if event.StreamStopped != nil {
			reason, _ := event.StreamStopped["reason"].(string)
			foundInstallError = reason == "install_error"
		}
	}
	if !foundInstallError {
		t.Fatalf("expected install_error StreamStopped, got %+v", events)
	}
}

func TestStreamManagerWithSimRuntimeStopAllShutsDownRuntime(t *testing.T) {
	runtime := newFakeSimRuntimeManager()
	var buf syncBuffer
	br, tc, ar, fc, sl := nopRunners()
	preparer := build.NewPreparer(ProjectConfig{}, build.ProjectDirs{}, false, br)
	sm := NewRuntimeStreamManager(runtime, protocol.NewEventWriter(&buf), ProjectConfig{}, "", preparer, br, tc, ar, fc, sl, false, 32, 0)

	sm.StopAll()

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.shutdown {
		t.Fatal("expected runtime.Shutdown to be called")
	}
}

func TestInputToRuntimeEventConvertsTouchAndText(t *testing.T) {
	tests := []struct {
		name  string
		input *pb.Input
		check func(t *testing.T, event simruntime.InputEvent)
	}{
		{
			name:  "touch down",
			input: &pb.Input{Event: &pb.Input_TouchDown{TouchDown: &pb.TouchEvent{X: 0.1, Y: 0.2}}},
			check: func(t *testing.T, event simruntime.InputEvent) {
				t.Helper()
				if event.TouchDown == nil || event.TouchDown.X != 0.1 || event.TouchDown.Y != 0.2 {
					t.Fatalf("event = %+v", event)
				}
			},
		},
		{
			name:  "touch move",
			input: &pb.Input{Event: &pb.Input_TouchMove{TouchMove: &pb.TouchEvent{X: 0.3, Y: 0.4}}},
			check: func(t *testing.T, event simruntime.InputEvent) {
				t.Helper()
				if event.TouchMove == nil || event.TouchMove.X != 0.3 || event.TouchMove.Y != 0.4 {
					t.Fatalf("event = %+v", event)
				}
			},
		},
		{
			name:  "touch up",
			input: &pb.Input{Event: &pb.Input_TouchUp{TouchUp: &pb.TouchEvent{X: 0.5, Y: 0.6}}},
			check: func(t *testing.T, event simruntime.InputEvent) {
				t.Helper()
				if event.TouchUp == nil || event.TouchUp.X != 0.5 || event.TouchUp.Y != 0.6 {
					t.Fatalf("event = %+v", event)
				}
			},
		},
		{
			name:  "text",
			input: &pb.Input{Event: &pb.Input_Text{Text: &pb.TextEvent{Value: "hello"}}},
			check: func(t *testing.T, event simruntime.InputEvent) {
				t.Helper()
				if event.Text != "hello" {
					t.Fatalf("event = %+v", event)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, inputToRuntimeEvent(tt.input))
		})
	}
}

func newSimRuntimeStreamManagerFixture(t *testing.T) (*fakeSimRuntimeManager, *syncBuffer, *StreamManager, string) {
	t.Helper()

	tmpDir := t.TempDir()
	pc, bs, copier := setupSimRuntimeStreamTestProject(t, tmpDir)
	br := &fakeBuildRunner{}
	preparer := sessionPreparer(t, pc, filepath.Join(tmpDir, "build"), bs)
	runtime := newFakeSimRuntimeManager()

	var buf syncBuffer
	ew := protocol.NewEventWriter(&buf)
	sm := NewRuntimeStreamManager(runtime, ew, pc, filepath.Join(tmpDir, "device-set"),
		preparer, br, &sessionToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, copier, &errSourceLister{}, false, 32, 0)
	useFastSimRuntimeLauncher(t, sm, bs)

	return runtime, &buf, sm, filepath.Join(tmpDir, "ContentView.swift")
}

func useFastSimRuntimeLauncher(t *testing.T, sm *StreamManager, bs *build.Settings) {
	t.Helper()
	sm.StreamLauncher = func(ctx context.Context, sm *StreamManager, s *stream) {
		if err := sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStatus{StreamStatus: &pb.StreamStatus{Phase: "booting"}},
		}); err != nil {
			return
		}

		info, err := sm.runtime.CreateSession(ctx, simruntime.CreateSessionRequest{
			DeviceType: s.deviceType,
			Runtime:    s.runtime,
		})
		if err != nil {
			s.sendStopped(sm.ew, "resource_error", err.Error(), "")
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

		if err := sm.runtime.InstallApp(ctx, info.ID, filepath.Join(dirs.Staging, "TestModule.app")); err != nil {
			s.sendStopped(sm.ew, "install_error", err.Error(), "")
			return
		}
		env := map[string]string{"SIMCTL_CHILD_AXE_PREVIEW_SOCKET_PATH": dirs.Socket}
		if err := sm.runtime.LaunchApp(ctx, info.ID, "axe."+bs.BundleID, env, nil); err != nil {
			s.sendStopped(sm.ew, "runtime_error", err.Error(), "")
			return
		}

		idbErrCh := make(chan error, 1)
		go relaySimRuntimeVideo(ctx, sm.runtime, info.ID, sm.ew, s.id, info.DeviceUDID, s.file, idbErrCh)
		go relaySimRuntimeEvents(ctx, sm.runtime, info.ID, idbErrCh)
		if err := sm.ew.Send(&pb.Event{
			StreamId: s.id,
			Payload:  &pb.Event_StreamStarted{StreamStarted: &pb.StreamStarted{PreviewCount: 1}},
		}); err != nil {
			return
		}

		if err := runDegradedStreamLoop(ctx, s, sm, idbErrCh); err != nil {
			return
		}
	}
}

func setupSimRuntimeStreamTestProject(t *testing.T, tmpDir string) (ProjectConfig, *build.Settings, FileCopier) {
	t.Helper()

	buildDir := filepath.Join(tmpDir, "build")
	builtProducts := filepath.Join(buildDir, "Build", "Products", "Debug-iphonesimulator")
	appDir := filepath.Join(builtProducts, "TestModule.app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.TestModule</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), []byte(plistContent), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(tmpDir, "ContentView.swift")
	if err := os.WriteFile(sourceFile, []byte("import SwiftUI\nstruct ContentView: View { var body: some View { Text(\"Hi\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projPath := filepath.Join(tmpDir, "TestModule.xcodeproj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}
	pc, err := build.NewProjectConfig(projPath, "", "TestModule", "Debug")
	if err != nil {
		t.Fatal(err)
	}
	bs := &build.Settings{
		ModuleName:       "TestModule",
		BundleID:         "com.example.TestModule",
		OriginalBundleID: "com.example.TestModule",
		BuiltProductsDir: builtProducts,
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.9",
	}
	return pc, bs, &sessionFileCopier{bs: bs, src: appDir}
}

func waitForCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if ok() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for condition")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func waitForParsedEvent(t *testing.T, buf *syncBuffer, timeout time.Duration, match func(parsedEvent) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if slices.ContainsFunc(collectEvents(t, buf), match) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for matching event; got %+v", collectEvents(t, buf))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func assertRuntimeStoppedOnce(t *testing.T, runtime *fakeSimRuntimeManager) {
	t.Helper()
	waitForCondition(t, 2*time.Second, func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return len(runtime.stopped) == 1
	})
}

func assertStoppedMessageContains(t *testing.T, buf *syncBuffer, want string) {
	t.Helper()
	events := filterEvents(collectEvents(t, buf), "stream-a")
	for _, event := range events {
		if event.StreamStopped == nil {
			continue
		}
		msg, _ := event.StreamStopped["message"].(string)
		if strings.Contains(msg, want) {
			return
		}
		t.Fatalf("StreamStopped message = %q, want containing %q", msg, want)
	}
	t.Fatalf("expected StreamStopped, got %+v", events)
}
