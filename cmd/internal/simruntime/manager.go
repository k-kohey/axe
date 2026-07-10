package simruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/k-kohey/axe/internal/idb"
	"github.com/k-kohey/axe/internal/platform"
	"github.com/k-kohey/axe/internal/procgroup"
	"howett.net/plist"
)

const bootRetryDelay = 2 * time.Second

type Option func(*RuntimeManager)

func WithDeviceSetPath(path string) Option {
	return func(m *RuntimeManager) {
		m.deviceSetPath = path
	}
}

type RuntimeManager struct {
	mu            sync.Mutex
	deviceSetPath string
	simctl        *platform.RealSimctlRunner
	pool          *platform.DevicePool
	sessions      map[string]*Session
}

func New(opts ...Option) (*RuntimeManager, error) {
	deviceSetPath, err := platform.AxeDeviceSetPath()
	if err != nil {
		return nil, err
	}
	m := &RuntimeManager{
		deviceSetPath: deviceSetPath,
		simctl:        &platform.RealSimctlRunner{},
		sessions:      make(map[string]*Session),
	}
	for _, opt := range opts {
		opt(m)
	}
	m.pool = platform.NewDevicePool(m.simctl, m.deviceSetPath)
	if err := m.pool.CleanupOrphans(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *RuntimeManager) ListDevices(ctx context.Context) ([]DeviceType, error) {
	available, err := platform.ListAvailable(m.simctl)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceType, 0, len(available))
	for _, d := range available {
		runtimes := make([]Runtime, 0, len(d.Runtimes))
		for _, r := range d.Runtimes {
			runtimes = append(runtimes, Runtime{Identifier: r.Identifier, Name: r.Name})
		}
		out = append(out, DeviceType{Identifier: d.Identifier, Name: d.Name, Runtimes: runtimes})
	}
	return out, nil
}

func (m *RuntimeManager) CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionInfo, error) {
	if req.DeviceType == "" {
		return nil, fmt.Errorf("device type is required")
	}
	if req.Runtime == "" {
		return nil, fmt.Errorf("runtime is required")
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	s := newSession(id, req.DeviceType, req.Runtime, cancel)

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	if err := m.startSession(sessionCtx, s, req); err != nil {
		s.setState(SessionFailed)
		s.publish(Event{SessionID: id, Time: time.Now(), Error: &ErrorEvent{Message: err.Error()}})
		s.publish(Event{SessionID: id, Time: time.Now(), Stopped: &StoppedEvent{Reason: "start_failed", Message: err.Error()}})
		cancel()
		m.cleanupSession(context.Background(), s)
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		return nil, err
	}
	return s.info(), nil
}

func (m *RuntimeManager) startSession(ctx context.Context, s *Session, req CreateSessionRequest) error {
	s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "acquiring_device"}})
	udid, err := m.pool.Acquire(ctx, req.DeviceType, req.Runtime)
	if err != nil {
		return fmt.Errorf("acquiring device: %w", err)
	}
	s.deviceUDID = udid

	s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "booting"}})
	bootCompanion, err := bootHeadlessWithRetry(ctx, udid, m.deviceSetPath)
	if err != nil {
		return fmt.Errorf("booting simulator: %w", err)
	}
	s.bootCompanion = bootCompanion

	s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "starting_idb"}})
	idbCompanion, err := idb.Start(udid, m.deviceSetPath)
	if err != nil {
		return fmt.Errorf("starting idb companion: %w", err)
	}
	s.idbCompanion = idbCompanion

	client, err := idb.NewClient(idbCompanion.Address())
	if err != nil {
		return err
	}
	s.client = client

	w, h, err := client.ScreenSize(ctx)
	if err != nil {
		return fmt.Errorf("screen size: %w", err)
	}
	s.screenWidth = w
	s.screenHeight = h

	bundleID, err := m.prepareLaunchTarget(ctx, s, req.Target)
	if err != nil {
		return err
	}
	s.bundleID = bundleID
	if bundleID != "" {
		s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "launching"}})
		if err := m.launchApp(ctx, s.deviceUDID, bundleID, nil, nil); err != nil {
			return err
		}
	}

	s.setState(SessionRunning)
	s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "running"}})
	go m.watchCompanions(s)
	return nil
}

func (m *RuntimeManager) prepareLaunchTarget(ctx context.Context, s *Session, target LaunchTarget) (string, error) {
	switch {
	case target.AppBundle != nil:
		if target.AppBundle.Path == "" {
			return "", fmt.Errorf("app bundle path is required")
		}
		s.publish(Event{SessionID: s.id, Time: time.Now(), Status: &StatusEvent{Phase: "installing"}})
		if err := m.installApp(ctx, s.deviceUDID, target.AppBundle.Path); err != nil {
			return "", err
		}
		if target.AppBundle.BundleID != "" {
			return target.AppBundle.BundleID, nil
		}
		return bundleIDFromApp(target.AppBundle.Path)
	case target.InstalledApp != nil:
		return target.InstalledApp.BundleID, nil
	default:
		return "", nil
	}
}

func (m *RuntimeManager) GetSession(id string) (*SessionInfo, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	return s.info(), nil
}

func (m *RuntimeManager) StopSession(ctx context.Context, id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", id)
	}
	s.cancel()
	m.cleanupSessionWithTimeout(ctx, s)
	s.setState(SessionStopped)
	s.publish(Event{SessionID: id, Time: time.Now(), Stopped: &StoppedEvent{Reason: "stopped", Message: ""}})
	s.closeEvents()
	return nil
}

func (m *RuntimeManager) InstallApp(ctx context.Context, id, appPath string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	if appPath == "" {
		return fmt.Errorf("app path is required")
	}
	s.publish(Event{SessionID: id, Time: time.Now(), Status: &StatusEvent{Phase: "installing"}})
	return m.installApp(ctx, s.deviceUDID, appPath)
}

func (m *RuntimeManager) LaunchApp(ctx context.Context, id, bundleID string, env map[string]string, args []string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	if bundleID == "" {
		return fmt.Errorf("bundle id is required")
	}
	s.publish(Event{SessionID: id, Time: time.Now(), Status: &StatusEvent{Phase: "launching"}})
	if err := m.launchApp(ctx, s.deviceUDID, bundleID, env, args); err != nil {
		return err
	}
	s.mu.Lock()
	s.bundleID = bundleID
	s.mu.Unlock()
	s.publish(Event{SessionID: id, Time: time.Now(), Status: &StatusEvent{Phase: "running"}})
	return nil
}

func (m *RuntimeManager) TerminateApp(ctx context.Context, id, bundleID string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	if bundleID == "" {
		return fmt.Errorf("bundle id is required")
	}
	return m.terminateApp(ctx, s.deviceUDID, bundleID)
}

func (m *RuntimeManager) SendInput(ctx context.Context, id string, input InputEvent) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	return s.sendInput(ctx, input)
}

func (m *RuntimeManager) SubscribeEvents(ctx context.Context, id string) (<-chan Event, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	return s.subscribe(ctx), nil
}

func (m *RuntimeManager) WatchVideo(ctx context.Context, id string, fps int) (<-chan VideoFrame, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	if fps <= 0 {
		fps = 30
	}
	rawCh, err := s.client.VideoStream(ctx, fps)
	if err != nil {
		return nil, err
	}
	out := make(chan VideoFrame, 2)
	go func() {
		defer close(out)
		var width, height int
		var buf bytes.Buffer
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-rawCh:
				if !ok {
					return
				}
			drain:
				for {
					select {
					case newer, ok := <-rawCh:
						if !ok {
							return
						}
						data = newer
					default:
						break drain
					}
				}
				if width == 0 {
					width, height = detectFrameDimensions(len(data), s.screenWidth, s.screenHeight)
					if width == 0 {
						continue
					}
				}
				if err := validateFrameSize(data, width, height); err != nil {
					slog.Debug("skipping invalid frame", "session", id, "err", err)
					continue
				}
				jpeg, err := encodeBGRAFrame(data, width, height, &buf)
				if err != nil {
					slog.Debug("jpeg encode failed", "session", id, "err", err)
					continue
				}
				select {
				case out <- VideoFrame{SessionID: id, JPEG: jpeg, Width: width, Height: height, Time: time.Now()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (m *RuntimeManager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		if err := m.StopSession(ctx, id); err != nil {
			slog.Debug("failed to stop session during shutdown", "session", id, "err", err)
		}
	}
	m.pool.ShutdownAll(ctx)
	m.pool.GarbageCollect(ctx)
}

func (m *RuntimeManager) lookup(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return s, nil
}

func (m *RuntimeManager) cleanupSession(ctx context.Context, s *Session) {
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.idbCompanion != nil {
		_ = s.idbCompanion.Stop()
	}
	if s.bootCompanion != nil {
		_ = s.bootCompanion.Stop()
	}
	if s.deviceUDID != "" {
		if err := m.pool.Release(ctx, s.deviceUDID); err != nil {
			slog.Debug("failed to release device", "session", s.id, "udid", s.deviceUDID, "err", err)
		}
	}
}

func (m *RuntimeManager) cleanupSessionWithTimeout(parent context.Context, s *Session) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	m.cleanupSession(ctx, s)
}

func (m *RuntimeManager) watchCompanions(s *Session) {
	select {
	case <-s.bootCompanion.Done():
		if err := s.bootCompanion.Err(); err != nil {
			s.publish(Event{SessionID: s.id, Time: time.Now(), Error: &ErrorEvent{Message: err.Error()}})
		}
	case <-s.idbCompanion.Done():
		if err := s.idbCompanion.Err(); err != nil {
			s.publish(Event{SessionID: s.id, Time: time.Now(), Error: &ErrorEvent{Message: err.Error()}})
		}
	}
}

func (m *RuntimeManager) installApp(ctx context.Context, udid, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	out, err := procgroup.Command(ctx, "xcrun", "simctl", "--set", m.deviceSetPath, "install", udid, abs).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl install: %w\n%s", err, out)
	}
	return nil
}

func (m *RuntimeManager) terminateApp(ctx context.Context, udid, bundleID string) error {
	out, err := procgroup.Command(ctx, "xcrun", "simctl", "--set", m.deviceSetPath, "terminate", udid, bundleID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl terminate: %w\n%s", err, out)
	}
	return nil
}

func (m *RuntimeManager) launchApp(ctx context.Context, udid, bundleID string, env map[string]string, args []string) error {
	if bundleID == "" {
		return nil
	}
	launchArgs := append([]string{"simctl", "--set", m.deviceSetPath, "launch", udid, bundleID}, args...)
	cmd := procgroup.Command(ctx, "xcrun", launchArgs...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl launch: %w\n%s", err, out)
	}
	return nil
}

func bootHeadlessWithRetry(ctx context.Context, udid, deviceSetPath string) (*idb.Companion, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		companion, err := idb.BootHeadless(udid, deviceSetPath)
		if err == nil {
			return companion, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(bootRetryDelay):
		}
	}
	return nil, fmt.Errorf("boot failed after retries: %w", lastErr)
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func bundleIDFromApp(path string) (string, error) {
	infoPlist := filepath.Join(path, "Info.plist")
	data, err := os.ReadFile(infoPlist)
	if err != nil {
		return "", fmt.Errorf("reading app Info.plist: %w", err)
	}
	var info struct {
		BundleID string `plist:"CFBundleIdentifier"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", fmt.Errorf("parsing app Info.plist: %w", err)
	}
	if info.BundleID == "" {
		return "", fmt.Errorf("CFBundleIdentifier not found in %s", infoPlist)
	}
	return info.BundleID, nil
}
