package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

// configFakeSimctlRunner is a SimctlRunner fake for config tests.
type configFakeSimctlRunner struct {
	allDevicesJSON []byte
}

func (f *configFakeSimctlRunner) ListDevices(_ context.Context, _ string) ([]simDevice, error) {
	return nil, nil
}
func (f *configFakeSimctlRunner) Clone(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (f *configFakeSimctlRunner) Create(_ context.Context, _, _, _, _ string) (string, error) {
	return "", nil
}
func (f *configFakeSimctlRunner) Shutdown(_ context.Context, _, _ string) error { return nil }
func (f *configFakeSimctlRunner) Delete(_ context.Context, _, _ string) error   { return nil }
func (f *configFakeSimctlRunner) ListAllDevices(_ context.Context, _ bool) ([]byte, error) {
	if f.allDevicesJSON != nil {
		return f.allDevicesJSON, nil
	}
	return []byte(`{"devices":{}}`), nil
}
func (f *configFakeSimctlRunner) ListRuntimes(_ context.Context) ([]byte, error) {
	return []byte(`{"runtimes":[]}`), nil
}
func (f *configFakeSimctlRunner) ListDeviceTypes(_ context.Context) ([]byte, error) {
	return []byte(`{"devicetypes":[]}`), nil
}

// fakeProcessLister returns a canned ps output.
type fakeProcessLister struct {
	output string
	err    error
}

func (f *fakeProcessLister) ListProcesses() (string, error) {
	return f.output, f.err
}

// chdir changes the working directory to dir and registers a cleanup
// to restore the original directory when the test finishes.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

func TestReadRC(t *testing.T) {
	t.Run("parses key-value pairs", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("APP_NAME=HogeApp\nPROJECT=./My.xcodeproj\nSCHEME=MyScheme\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chdir(t, dir)

		rc := ReadRC()
		if rc["APP_NAME"] != "HogeApp" {
			t.Errorf("APP_NAME = %q, want HogeApp", rc["APP_NAME"])
		}
		if rc["PROJECT"] != "./My.xcodeproj" {
			t.Errorf("PROJECT = %q, want ./My.xcodeproj", rc["PROJECT"])
		}
		if rc["SCHEME"] != "MyScheme" {
			t.Errorf("SCHEME = %q, want MyScheme", rc["SCHEME"])
		}
	})

	t.Run("skips comments and blank lines", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("# comment\n\nAPP_NAME=Test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chdir(t, dir)

		rc := ReadRC()
		if len(rc) != 1 {
			t.Errorf("expected 1 key, got %d: %v", len(rc), rc)
		}
		if rc["APP_NAME"] != "Test" {
			t.Errorf("APP_NAME = %q, want Test", rc["APP_NAME"])
		}
	})

	t.Run("returns nil when no .axerc", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)

		rc := ReadRC()
		if rc != nil {
			t.Errorf("expected nil, got %v", rc)
		}
	})
}

func TestResolveAppName(t *testing.T) {
	t.Run("flag value takes priority", func(t *testing.T) {
		name, err := ResolveAppName("MyApp")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "MyApp" {
			t.Fatalf("expected MyApp, got %s", name)
		}
	})

	t.Run("reads from .axerc when flag is empty", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("APP_NAME=SampleApp\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chdir(t, dir)

		name, err := ResolveAppName("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "SampleApp" {
			t.Fatalf("expected SampleApp, got %s", name)
		}
	})

	t.Run("returns error when no flag and no .axerc", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)

		_, err := ResolveAppName("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestMatchProcesses(t *testing.T) {
	procs := []SimProcess{
		{PID: 100, App: "MyApp", DeviceUDID: "AAAA", DeviceName: "iPhone 17"},
		{PID: 200, App: "MyApp", DeviceUDID: "BBBB", DeviceName: "iPhone 16"},
		{PID: 300, App: "Other", DeviceUDID: "AAAA", DeviceName: "iPhone 17"},
	}

	t.Run("matches by name only when device is empty", func(t *testing.T) {
		got := matchProcesses(procs, "MyApp", "")
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("matches by name only when device is booted", func(t *testing.T) {
		got := matchProcesses(procs, "MyApp", "booted")
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filters by device UDID", func(t *testing.T) {
		got := matchProcesses(procs, "MyApp", "AAAA")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].PID != 100 {
			t.Fatalf("expected PID 100, got %d", got[0].PID)
		}
	})

	t.Run("returns nil when no match", func(t *testing.T) {
		got := matchProcesses(procs, "MyApp", "CCCC")
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("filters by device name", func(t *testing.T) {
		got := matchProcesses(procs, "MyApp", "iPhone 17")
		if len(got) != 1 {
			t.Fatalf("expected 1, got %d", len(got))
		}
		if got[0].PID != 100 {
			t.Fatalf("expected PID 100, got %d", got[0].PID)
		}
	})

	t.Run("different app name returns nil", func(t *testing.T) {
		got := matchProcesses(procs, "NoSuchApp", "")
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})
}

func TestParseSimulatorProcesses(t *testing.T) {
	deviceMap := map[string]string{
		"F31DE05D-0E6E-4DC5-B949-FB5736AB5E75": "iPhone 17 Pro Max",
		"602CEFC3-52DB-4866-AD97-10B960004C42": "iPhone 16e",
	}

	t.Run("extracts app processes from ps output", func(t *testing.T) {
		psOutput := `  PID ARGS
  100 /usr/sbin/syslogd
56662 /Users/user/Library/Developer/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/Containers/Bundle/Application/ABC123/HogeApp.app/HogeApp
96522 /Users/user/Library/Developer/CoreSimulator/Devices/602CEFC3-52DB-4866-AD97-10B960004C42/data/Containers/Bundle/Application/DEF456/HogeApp.app/HogeApp`

		got := parseSimulatorProcesses(psOutput, deviceMap)
		if len(got) != 2 {
			t.Fatalf("expected 2 processes, got %d", len(got))
		}
		if got[0].PID != 56662 || got[0].App != "HogeApp" || got[0].DeviceName != "iPhone 17 Pro Max" {
			t.Fatalf("unexpected first process: %+v", got[0])
		}
		if got[1].PID != 96522 || got[1].App != "HogeApp" || got[1].DeviceName != "iPhone 16e" {
			t.Fatalf("unexpected second process: %+v", got[1])
		}
	})

	t.Run("excludes launchd_sim", func(t *testing.T) {
		psOutput := `  PID ARGS
53175 /Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 18.4.simruntime/Contents/Resources/RuntimeRoot/sbin/launchd_sim /Users/user/Library/Developer/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data
56662 /Users/user/Library/Developer/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/Containers/Bundle/Application/ABC123/HogeApp.app/HogeApp`

		got := parseSimulatorProcesses(psOutput, deviceMap)
		if len(got) != 1 {
			t.Fatalf("expected 1 process, got %d", len(got))
		}
		if got[0].App != "HogeApp" {
			t.Fatalf("expected HogeApp, got %s", got[0].App)
		}
	})

	t.Run("unknown device when UDID not in map", func(t *testing.T) {
		psOutput := `  PID ARGS
12345 /Users/user/Library/Developer/CoreSimulator/Devices/AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/data/Containers/Bundle/Application/XYZ/MyApp.app/MyApp`

		got := parseSimulatorProcesses(psOutput, deviceMap)
		if len(got) != 1 {
			t.Fatalf("expected 1 process, got %d", len(got))
		}
		if got[0].DeviceName != "unknown" {
			t.Fatalf("expected unknown, got %s", got[0].DeviceName)
		}
	})

	t.Run("returns nil for empty ps output", func(t *testing.T) {
		got := parseSimulatorProcesses("  PID ARGS\n", deviceMap)
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("skips non-app CoreSimulator lines", func(t *testing.T) {
		psOutput := `  PID ARGS
99999 /Users/user/Library/Developer/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/some_daemon`

		got := parseSimulatorProcesses(psOutput, deviceMap)
		if got != nil {
			t.Fatalf("expected nil for non-.app path, got %+v", got)
		}
	})

	t.Run("extracts BundleID from Info.plist", func(t *testing.T) {
		// Create a fake .app with Info.plist
		dir := t.TempDir()
		appDir := filepath.Join(dir, "Fake.app")
		if err := os.MkdirAll(appDir, 0o750); err != nil {
			t.Fatal(err)
		}

		infoPlist := map[string]string{"CFBundleIdentifier": "com.example.fake"}
		plistData, _ := plist.Marshal(infoPlist, plist.BinaryFormat)
		if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), plistData, 0o600); err != nil {
			t.Fatal(err)
		}

		// appPathRe needs CoreSimulator path, so construct a full path
		psOutput := "  PID ARGS\n12345 " + dir + "/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/Containers/Bundle/Application/ABC123/Fake.app/Fake\n"

		// Create the nested .app dir at the expected path
		nestedAppDir := filepath.Join(dir, "CoreSimulator", "Devices", "F31DE05D-0E6E-4DC5-B949-FB5736AB5E75", "data", "Containers", "Bundle", "Application", "ABC123", "Fake.app")
		if err := os.MkdirAll(nestedAppDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nestedAppDir, "Info.plist"), plistData, 0o600); err != nil {
			t.Fatal(err)
		}

		got := parseSimulatorProcesses(psOutput, deviceMap)
		if len(got) != 1 {
			t.Fatalf("expected 1 process, got %d", len(got))
		}
		if got[0].BundleID != "com.example.fake" {
			t.Fatalf("expected BundleID com.example.fake, got %q", got[0].BundleID)
		}
	})
}

func TestListSimulatorProcesses_WithFakes(t *testing.T) {
	deviceMapJSON, _ := json.Marshal(struct {
		Devices map[string][]struct {
			Name string `json:"name"`
			UDID string `json:"udid"`
		} `json:"devices"`
	}{
		Devices: map[string][]struct {
			Name string `json:"name"`
			UDID string `json:"udid"`
		}{
			"iOS": {
				{Name: "iPhone 17 Pro Max", UDID: "F31DE05D-0E6E-4DC5-B949-FB5736AB5E75"},
			},
		},
	})

	simctl := &configFakeSimctlRunner{allDevicesJSON: deviceMapJSON}
	pl := &fakeProcessLister{
		output: `  PID ARGS
56662 /Users/user/Library/Developer/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/Containers/Bundle/Application/ABC123/HogeApp.app/HogeApp`,
	}

	procs, err := ListSimulatorProcesses(simctl, pl)
	if err != nil {
		t.Fatalf("ListSimulatorProcesses: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 process, got %d", len(procs))
	}
	if procs[0].App != "HogeApp" {
		t.Errorf("expected app HogeApp, got %s", procs[0].App)
	}
	if procs[0].DeviceName != "iPhone 17 Pro Max" {
		t.Errorf("expected device name 'iPhone 17 Pro Max', got %q", procs[0].DeviceName)
	}
}

func TestListSimulatorProcesses_PSError(t *testing.T) {
	simctl := &configFakeSimctlRunner{}
	pl := &fakeProcessLister{err: fmt.Errorf("ps failed")}

	_, err := ListSimulatorProcesses(simctl, pl)
	if err == nil {
		t.Fatal("expected error when ps fails, got nil")
	}
}

func TestFindProcess_WithFakes(t *testing.T) {
	deviceMapJSON, _ := json.Marshal(struct {
		Devices map[string][]struct {
			Name string `json:"name"`
			UDID string `json:"udid"`
		} `json:"devices"`
	}{
		Devices: map[string][]struct {
			Name string `json:"name"`
			UDID string `json:"udid"`
		}{
			"iOS": {
				{Name: "iPhone 17", UDID: "AAAA-BBBB"},
			},
		},
	})

	simctl := &configFakeSimctlRunner{allDevicesJSON: deviceMapJSON}

	t.Run("finds process", func(t *testing.T) {
		pl := &fakeProcessLister{
			output: `  PID ARGS
12345 /Users/user/Library/Developer/CoreSimulator/Devices/AAAA-BBBB/data/Containers/Bundle/Application/XYZ/MyApp.app/MyApp`,
		}

		pid, err := FindProcess(simctl, pl, "MyApp", "")
		if err != nil {
			t.Fatalf("FindProcess: %v", err)
		}
		if pid != 12345 {
			t.Errorf("expected PID 12345, got %d", pid)
		}
	})

	t.Run("process not found", func(t *testing.T) {
		pl := &fakeProcessLister{
			output: "  PID ARGS\n",
		}

		_, err := FindProcess(simctl, pl, "NoSuchApp", "")
		if err == nil {
			t.Fatal("expected error for missing process, got nil")
		}
	})

	t.Run("filters by device", func(t *testing.T) {
		pl := &fakeProcessLister{
			output: `  PID ARGS
12345 /Users/user/Library/Developer/CoreSimulator/Devices/AAAA-BBBB/data/Containers/Bundle/Application/XYZ/MyApp.app/MyApp
67890 /Users/user/Library/Developer/CoreSimulator/Devices/CCCC-DDDD/data/Containers/Bundle/Application/ABC/MyApp.app/MyApp`,
		}

		pid, err := FindProcess(simctl, pl, "MyApp", "AAAA-BBBB")
		if err != nil {
			t.Fatalf("FindProcess: %v", err)
		}
		if pid != 12345 {
			t.Errorf("expected PID 12345, got %d", pid)
		}
	})
}

func TestBuildDeviceMap_WithFake(t *testing.T) {
	deviceMapJSON := []byte(`{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
				{"name": "iPhone 16", "udid": "AAA"},
				{"name": "iPhone 16 Pro", "udid": "BBB"}
			]
		}
	}`)

	simctl := &configFakeSimctlRunner{allDevicesJSON: deviceMapJSON}

	m, err := buildDeviceMap(simctl)
	if err != nil {
		t.Fatalf("buildDeviceMap: %v", err)
	}

	if m["AAA"] != "iPhone 16" {
		t.Errorf("expected 'iPhone 16' for AAA, got %q", m["AAA"])
	}
	if m["BBB"] != "iPhone 16 Pro" {
		t.Errorf("expected 'iPhone 16 Pro' for BBB, got %q", m["BBB"])
	}
}

func TestRealProcessLister_ImplementsInterface(t *testing.T) {
	var _ ProcessLister = &RealProcessLister{}
}
