package simulatorserver

import (
	"context"
	"fmt"
	"sync"

	"github.com/k-kohey/axe/internal/simruntime"
)

type fakeManager struct {
	mu sync.Mutex

	devices []simruntime.DeviceType
	managed []simruntime.ManagedDevice
	session *simruntime.SessionInfo

	createReqs []simruntime.CreateSessionRequest
	addReqs    []simruntime.AddManagedDeviceRequest
	inputs     []simruntime.InputEvent
	stoppedIDs []string
	removed    []string
	defaults   []string
	installed  []string
	launched   []launchCall
	terminated []string

	events chan simruntime.Event
	frames chan simruntime.VideoFrame
}

type launchCall struct {
	sessionID string
	bundleID  string
	env       map[string]string
	args      []string
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		devices: []simruntime.DeviceType{{
			Identifier: "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
			Name:       "iPhone 16 Pro",
			Runtimes: []simruntime.Runtime{{
				Identifier: "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
				Name:       "iOS 18.2",
			}},
		}},
		managed: []simruntime.ManagedDevice{{
			UDID:      "UDID-1",
			Name:      "axe iPhone 16 Pro (1)",
			Runtime:   "iOS 18.2",
			RuntimeID: "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
			State:     "Shutdown",
			IsDefault: true,
		}},
		session: &simruntime.SessionInfo{
			ID:           "session-1",
			DeviceUDID:   "UDID-1",
			DeviceType:   "device-type",
			Runtime:      "runtime",
			State:        simruntime.SessionRunning,
			BundleID:     "com.example.App",
			ScreenWidth:  390,
			ScreenHeight: 844,
		},
		events: make(chan simruntime.Event, 4),
		frames: make(chan simruntime.VideoFrame, 4),
	}
}

func (m *fakeManager) ListDevices(context.Context) ([]simruntime.DeviceType, error) {
	return m.devices, nil
}

func (m *fakeManager) ListManagedDevices(context.Context) ([]simruntime.ManagedDevice, error) {
	return m.managed, nil
}

func (m *fakeManager) AddManagedDevice(_ context.Context, req simruntime.AddManagedDeviceRequest) (*simruntime.ManagedDevice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addReqs = append(m.addReqs, req)
	device := simruntime.ManagedDevice{
		UDID:      "UDID-2",
		Name:      "axe iPhone 16 Pro (2)",
		Runtime:   "iOS 18.2",
		RuntimeID: req.Runtime,
		State:     "Shutdown",
		IsDefault: req.SetDefault,
	}
	m.managed = append(m.managed, device)
	return &device, nil
}

func (m *fakeManager) RemoveManagedDevice(_ context.Context, udid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, udid)
	return nil
}

func (m *fakeManager) SetDefaultManagedDevice(_ context.Context, udid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaults = append(m.defaults, udid)
	return nil
}

func (m *fakeManager) CreateSession(_ context.Context, req simruntime.CreateSessionRequest) (*simruntime.SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createReqs = append(m.createReqs, req)
	if m.session == nil {
		return nil, fmt.Errorf("no session")
	}
	return m.session, nil
}

func (m *fakeManager) ListSessions() []*simruntime.SessionInfo {
	if m.session == nil {
		return nil
	}
	return []*simruntime.SessionInfo{m.session}
}

func (m *fakeManager) GetSession(id string) (*simruntime.SessionInfo, error) {
	if m.session == nil || m.session.ID != id {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return m.session, nil
}

func (m *fakeManager) StopSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stoppedIDs = append(m.stoppedIDs, id)
	return nil
}

func (m *fakeManager) InstallApp(_ context.Context, id, appPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installed = append(m.installed, id+":"+appPath)
	return nil
}

func (m *fakeManager) LaunchApp(_ context.Context, id, bundleID string, env map[string]string, args []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launched = append(m.launched, launchCall{sessionID: id, bundleID: bundleID, env: env, args: args})
	return nil
}

func (m *fakeManager) TerminateApp(_ context.Context, id, bundleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminated = append(m.terminated, id+":"+bundleID)
	return nil
}

func (m *fakeManager) SendInput(_ context.Context, id string, input simruntime.InputEvent) error {
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, input)
	return nil
}

func (m *fakeManager) Screenshot(context.Context, string) ([]byte, error) {
	return []byte("fake-png"), nil
}

func (m *fakeManager) SubscribeEvents(context.Context, string) (<-chan simruntime.Event, error) {
	return m.events, nil
}

func (m *fakeManager) WatchVideo(context.Context, string, int) (<-chan simruntime.VideoFrame, error) {
	return m.frames, nil
}

func (m *fakeManager) Shutdown(context.Context) {}
