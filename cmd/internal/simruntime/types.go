package simruntime

import (
	"context"
	"time"
)

type DeviceType struct {
	Identifier string
	Name       string
	Runtimes   []Runtime
}

type Runtime struct {
	Identifier string
	Name       string
}

type ManagedDevice struct {
	UDID      string
	Name      string
	Runtime   string
	RuntimeID string
	State     string
	IsDefault bool
}

type AddManagedDeviceRequest struct {
	DeviceType string
	Runtime    string
	SetDefault bool
}

type LaunchTarget struct {
	AppBundle    *AppBundle
	InstalledApp *InstalledApp
}

type AppBundle struct {
	Path     string
	BundleID string
}

type InstalledApp struct {
	BundleID string
}

type CreateSessionRequest struct {
	DeviceType string
	Runtime    string
	DeviceUDID string
	NoHeadless bool
	Target     LaunchTarget
}

type SessionState string

const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionStopped  SessionState = "stopped"
	SessionFailed   SessionState = "failed"
)

type SessionInfo struct {
	ID           string
	DeviceUDID   string
	DeviceType   string
	Runtime      string
	State        SessionState
	BundleID     string
	ScreenWidth  int
	ScreenHeight int
}

type InputEvent struct {
	TouchDown *Point
	TouchMove *Point
	TouchUp   *Point
	Text      string
	Tap       *Point
	Swipe     *Swipe
}

type Point struct {
	X float64
	Y float64
}

type Swipe struct {
	StartX          float64
	StartY          float64
	EndX            float64
	EndY            float64
	DurationSeconds float64
}

type Event struct {
	SessionID string
	Time      time.Time
	Status    *StatusEvent
	Stopped   *StoppedEvent
	Error     *ErrorEvent
}

type StatusEvent struct {
	Phase string
}

type StoppedEvent struct {
	Reason  string
	Message string
}

type ErrorEvent struct {
	Message string
}

type VideoFrame struct {
	SessionID string
	JPEG      []byte
	Width     int
	Height    int
	Time      time.Time
}

type Manager interface {
	ListDevices(ctx context.Context) ([]DeviceType, error)
	ListManagedDevices(ctx context.Context) ([]ManagedDevice, error)
	AddManagedDevice(ctx context.Context, req AddManagedDeviceRequest) (*ManagedDevice, error)
	RemoveManagedDevice(ctx context.Context, udid string) error
	SetDefaultManagedDevice(ctx context.Context, udid string) error
	CreateSession(ctx context.Context, req CreateSessionRequest) (*SessionInfo, error)
	ListSessions() []*SessionInfo
	GetSession(id string) (*SessionInfo, error)
	StopSession(ctx context.Context, id string) error
	InstallApp(ctx context.Context, id, appPath string) error
	LaunchApp(ctx context.Context, id, bundleID string, env map[string]string, args []string) error
	TerminateApp(ctx context.Context, id, bundleID string) error
	SendInput(ctx context.Context, id string, input InputEvent) error
	Screenshot(ctx context.Context, id string) ([]byte, error)
	SubscribeEvents(ctx context.Context, id string) (<-chan Event, error)
	WatchVideo(ctx context.Context, id string, fps int) (<-chan VideoFrame, error)
	Shutdown(ctx context.Context)
}
