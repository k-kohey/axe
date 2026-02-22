package preview

import (
	"strings"
	"testing"
)

// mustNewPreviewDirs is a test helper that calls newPreviewDirs and fails on error.
func mustNewPreviewDirs(t *testing.T, projectPath, deviceUDID string) previewDirs {
	t.Helper()
	dirs, err := newPreviewDirs(projectPath, deviceUDID)
	if err != nil {
		t.Fatalf("newPreviewDirs(%q, %q): %v", projectPath, deviceUDID, err)
	}
	return dirs
}

func TestNewPreviewDirs_SessionIsolation(t *testing.T) {
	a := mustNewPreviewDirs(t, "/path/to/project", "AAAA-1111")
	b := mustNewPreviewDirs(t, "/path/to/project", "BBBB-2222")

	// Build directory must be shared (same project).
	if a.Build != b.Build {
		t.Errorf("Build dirs differ for same project: %s vs %s", a.Build, b.Build)
	}
	if a.Root != b.Root {
		t.Errorf("Root dirs differ for same project: %s vs %s", a.Root, b.Root)
	}

	// Session-specific resources must differ.
	if a.Session == b.Session {
		t.Error("Session dirs should differ for different devices")
	}
	if a.Thunk == b.Thunk {
		t.Error("Thunk dirs should differ for different devices")
	}
	if a.Loader == b.Loader {
		t.Error("Loader dirs should differ for different devices")
	}
	if a.Staging == b.Staging {
		t.Error("Staging dirs should differ for different devices")
	}
	if a.Socket == b.Socket {
		t.Error("Socket paths should differ for different devices")
	}
}

func TestNewPreviewDirs_BuildSharedAcrossDevices(t *testing.T) {
	d1 := mustNewPreviewDirs(t, "/workspace/MyApp.xcodeproj", "device-1")
	d2 := mustNewPreviewDirs(t, "/workspace/MyApp.xcodeproj", "device-2")

	if d1.Build != d2.Build {
		t.Errorf("Build should be shared: %s vs %s", d1.Build, d2.Build)
	}
}

func TestNewPreviewDirs_SameDeviceSamePaths(t *testing.T) {
	a := mustNewPreviewDirs(t, "/path/to/project", "AAAA-1111")
	b := mustNewPreviewDirs(t, "/path/to/project", "AAAA-1111")

	if a != b {
		t.Errorf("same project + same device should return identical dirs:\n  a=%+v\n  b=%+v", a, b)
	}
}

func TestNewPreviewDirs_SessionUnderDevices(t *testing.T) {
	dirs := mustNewPreviewDirs(t, "/some/project", "UDID-1234")

	if !strings.Contains(dirs.Session, "devices/UDID-1234") {
		t.Errorf("Session should contain devices/<udid>, got %s", dirs.Session)
	}
	if !strings.HasPrefix(dirs.Thunk, dirs.Session) {
		t.Errorf("Thunk should be under Session: Thunk=%s Session=%s", dirs.Thunk, dirs.Session)
	}
	if !strings.HasPrefix(dirs.Loader, dirs.Session) {
		t.Errorf("Loader should be under Session: Loader=%s Session=%s", dirs.Loader, dirs.Session)
	}
	if !strings.HasPrefix(dirs.Staging, dirs.Session) {
		t.Errorf("Staging should be under Session: Staging=%s Session=%s", dirs.Staging, dirs.Session)
	}
	// Socket is under Root (not Session) to keep the path within macOS
	// sun_path limit (104 bytes).
	if !strings.HasPrefix(dirs.Socket, dirs.Root) {
		t.Errorf("Socket should be under Root: Socket=%s Root=%s", dirs.Socket, dirs.Root)
	}
	if len(dirs.Socket) >= maxSunPathLen {
		t.Errorf("Socket path too long for Unix domain socket: len=%d limit=%d path=%s",
			len(dirs.Socket), maxSunPathLen, dirs.Socket)
	}
}

func TestNewPreviewDirs_DifferentProjectsDifferentBuild(t *testing.T) {
	a := mustNewPreviewDirs(t, "/project-a", "same-device")
	b := mustNewPreviewDirs(t, "/project-b", "same-device")

	if a.Build == b.Build {
		t.Error("Different projects should have different Build dirs")
	}
}

func TestNewPreviewDirs_SocketPathTooLong(t *testing.T) {
	// The socket path is <cacheDir>/axe/preview-<hash>/<hash>.sock.
	// Since project/device inputs are hashed, only a long HOME (cache dir
	// fallback) can push the path over 104 bytes. Set HOME to a long path
	// to trigger the error.
	longHome := "/" + strings.Repeat("a", 120)
	t.Setenv("HOME", longHome)

	_, err := newPreviewDirs("/project", "device-1")
	if err == nil {
		t.Fatal("expected error for overly long socket path, got nil")
	}
	if !strings.Contains(err.Error(), "socket path exceeds") {
		t.Errorf("expected socket path error message, got: %v", err)
	}
}
