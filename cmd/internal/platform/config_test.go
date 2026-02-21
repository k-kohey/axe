package platform

import (
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

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
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("APP_NAME=HogeApp\nPROJECT=./My.xcodeproj\nSCHEME=MyScheme\n"), 0644); err != nil {
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
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("# comment\n\nAPP_NAME=Test\n"), 0644); err != nil {
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
		if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte("APP_NAME=SampleApp\n"), 0644); err != nil {
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
		if err := os.MkdirAll(appDir, 0755); err != nil {
			t.Fatal(err)
		}

		infoPlist := map[string]string{"CFBundleIdentifier": "com.example.fake"}
		plistData, _ := plist.Marshal(infoPlist, plist.BinaryFormat)
		if err := os.WriteFile(filepath.Join(appDir, "Info.plist"), plistData, 0644); err != nil {
			t.Fatal(err)
		}

		// appPathRe needs CoreSimulator path, so construct a full path
		psOutput := "  PID ARGS\n12345 " + dir + "/CoreSimulator/Devices/F31DE05D-0E6E-4DC5-B949-FB5736AB5E75/data/Containers/Bundle/Application/ABC123/Fake.app/Fake\n"

		// Create the nested .app dir at the expected path
		nestedAppDir := filepath.Join(dir, "CoreSimulator", "Devices", "F31DE05D-0E6E-4DC5-B949-FB5736AB5E75", "data", "Containers", "Bundle", "Application", "ABC123", "Fake.app")
		if err := os.MkdirAll(nestedAppDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nestedAppDir, "Info.plist"), plistData, 0644); err != nil {
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
