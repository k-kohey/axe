package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// simFakeSimctlRunner is a SimctlRunner fake for testing ResolveAxeSimulator.
type simFakeSimctlRunner struct {
	devices        []simDevice
	allDevicesJSON []byte
	createErr      error
	createdUDID    string
}

func (f *simFakeSimctlRunner) ListDevices(_ context.Context, _ string) ([]simDevice, error) {
	return f.devices, nil
}

func (f *simFakeSimctlRunner) Clone(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func (f *simFakeSimctlRunner) Create(_ context.Context, name, deviceType, runtime, _ string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	udid := f.createdUDID
	if udid == "" {
		udid = "CREATED-1"
	}
	return udid, nil
}

func (f *simFakeSimctlRunner) Shutdown(_ context.Context, _, _ string) error { return nil }
func (f *simFakeSimctlRunner) Delete(_ context.Context, _, _ string) error   { return nil }
func (f *simFakeSimctlRunner) Boot(_ context.Context, _ string) error        { return nil }

func (f *simFakeSimctlRunner) ListAllDevices(_ context.Context, _ bool) ([]byte, error) {
	if f.allDevicesJSON != nil {
		return f.allDevicesJSON, nil
	}
	result := struct {
		Devices map[string][]simDevice `json:"devices"`
	}{
		Devices: make(map[string][]simDevice),
	}
	for _, d := range f.devices {
		result.Devices[d.RuntimeID] = append(result.Devices[d.RuntimeID], d)
	}
	data, _ := json.Marshal(result)
	return data, nil
}

func (f *simFakeSimctlRunner) ListRuntimes(_ context.Context) ([]byte, error) {
	return []byte(`{"runtimes":[]}`), nil
}

func (f *simFakeSimctlRunner) ListDeviceTypes(_ context.Context) ([]byte, error) {
	return []byte(`{"devicetypes":[]}`), nil
}

func TestResolveSimulator(t *testing.T) {
	t.Run("returns flag value when provided", func(t *testing.T) {
		got := ResolveSimulator("ABCD-1234")
		if got != "ABCD-1234" {
			t.Errorf("expected ABCD-1234, got %s", got)
		}
	})

	t.Run("returns booted when flag is empty", func(t *testing.T) {
		got := ResolveSimulator("")
		if got != "booted" {
			t.Errorf("expected booted, got %s", got)
		}
	})
}

func TestAxeDeviceSetPath(t *testing.T) {
	path, err := AxeDeviceSetPath()
	if err != nil {
		t.Fatalf("AxeDeviceSetPath: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	expected := filepath.Join(home, "Library", "Developer", "axe", "Simulator Devices")
	if path != expected {
		t.Errorf("AxeDeviceSetPath() = %q, want %q", path, expected)
	}

	if !strings.HasPrefix(path, home) {
		t.Errorf("expected path to be under home dir %s, got %s", home, path)
	}
}

func TestSelectLatestIPhone(t *testing.T) {
	simctlJSON := []byte(`{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-17-0": [
				{"name": "iPhone 15", "udid": "AAA", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-15"},
				{"name": "iPad Air", "udid": "BBB", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPad-Air"}
			],
			"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
				{"name": "iPhone 16", "udid": "CCC", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16"},
				{"name": "iPhone 16 Pro", "udid": "DDD", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
			],
			"com.apple.CoreSimulator.SimRuntime.tvOS-18-0": [
				{"name": "Apple TV", "udid": "EEE", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.Apple-TV"}
			]
		}
	}`)

	best, runtime, err := selectLatestIPhone(simctlJSON)
	if err != nil {
		t.Fatalf("selectLatestIPhone: %v", err)
	}

	// Expect iPhone 16 Pro (iOS 18.2, lexicographically largest on same version).
	if best.UDID != "DDD" {
		t.Errorf("expected iPhone 16 Pro (DDD), got %s (%s)", best.Name, best.UDID)
	}
	if best.Name != "iPhone 16 Pro" {
		t.Errorf("expected name iPhone 16 Pro, got %s", best.Name)
	}
	if runtime != "com.apple.CoreSimulator.SimRuntime.iOS-18-2" {
		t.Errorf("expected iOS 18.2 runtime, got %s", runtime)
	}
}

func TestSelectLatestIPhone_NoIPhone(t *testing.T) {
	simctlJSON := []byte(`{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
				{"name": "iPad Air", "udid": "AAA", "state": "Shutdown", "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPad-Air"}
			]
		}
	}`)

	_, _, err := selectLatestIPhone(simctlJSON)
	if err == nil {
		t.Fatal("expected error when no iPhone found, got nil")
	}
}

func TestSelectLatestIPhone_MalformedJSON(t *testing.T) {
	_, _, err := selectLatestIPhone([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestSelectAvailableSimulator(t *testing.T) {
	t.Run("all booted returns empty", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Booted"},
			{UDID: "B", State: "Booted"},
		}
		udid, ok := selectAvailableSimulator(devices, "")
		if ok || udid != "" {
			t.Errorf("expected (\"\", false), got (%q, %v)", udid, ok)
		}
	})

	t.Run("empty devices returns empty", func(t *testing.T) {
		udid, ok := selectAvailableSimulator(nil, "")
		if ok || udid != "" {
			t.Errorf("expected (\"\", false), got (%q, %v)", udid, ok)
		}
	})

	t.Run("default is Shutdown and preferred", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Shutdown"},
			{UDID: "B", State: "Shutdown"},
		}
		udid, ok := selectAvailableSimulator(devices, "B")
		if !ok || udid != "B" {
			t.Errorf("expected (\"B\", true), got (%q, %v)", udid, ok)
		}
	})

	t.Run("default is Booted skips to other Shutdown", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Booted"},
			{UDID: "B", State: "Shutdown"},
		}
		udid, ok := selectAvailableSimulator(devices, "A")
		if !ok || udid != "B" {
			t.Errorf("expected (\"B\", true), got (%q, %v)", udid, ok)
		}
	})

	t.Run("default absent falls back to first Shutdown", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Booted"},
			{UDID: "B", State: "Shutdown"},
			{UDID: "C", State: "Shutdown"},
		}
		udid, ok := selectAvailableSimulator(devices, "MISSING")
		if !ok || udid != "B" {
			t.Errorf("expected (\"B\", true), got (%q, %v)", udid, ok)
		}
	})

	t.Run("no default picks first Shutdown", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Booted"},
			{UDID: "B", State: "Shutdown"},
		}
		udid, ok := selectAvailableSimulator(devices, "")
		if !ok || udid != "B" {
			t.Errorf("expected (\"B\", true), got (%q, %v)", udid, ok)
		}
	})

	t.Run("all Booted with Booted default returns empty", func(t *testing.T) {
		devices := []simDevice{
			{UDID: "A", State: "Booted"},
			{UDID: "B", State: "Booted"},
		}
		udid, ok := selectAvailableSimulator(devices, "A")
		if ok || udid != "" {
			t.Errorf("expected (\"\", false), got (%q, %v)", udid, ok)
		}
	})
}

func TestParseIOSVersion(t *testing.T) {
	tests := []struct {
		runtime   string
		wantMajor int
		wantMinor int
	}{
		{"com.apple.CoreSimulator.SimRuntime.iOS-18-2", 18, 2},
		{"com.apple.CoreSimulator.SimRuntime.iOS-17-0", 17, 0},
		{"com.apple.CoreSimulator.SimRuntime.iOS-9-0", 9, 0},
		{"com.apple.CoreSimulator.SimRuntime.tvOS-18-0", -1, -1},
		{"com.apple.CoreSimulator.SimRuntime.watchOS-11-0", -1, -1},
		{"not-a-runtime", -1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			major, minor := parseIOSVersion(tt.runtime)
			if major != tt.wantMajor || minor != tt.wantMinor {
				t.Errorf("parseIOSVersion(%q) = (%d, %d), want (%d, %d)",
					tt.runtime, major, minor, tt.wantMajor, tt.wantMinor)
			}
		})
	}
}

func TestResolveAxeSimulator_PreferredUDID(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown"},
			{Name: "axe iPhone 16 Pro (2)", UDID: "BBB", State: "Booted"},
		},
	}

	udid, _, isExternal, err := ResolveAxeSimulator(runner, "BBB")
	if err != nil {
		t.Fatalf("ResolveAxeSimulator: %v", err)
	}
	if udid != "BBB" {
		t.Errorf("expected BBB, got %s", udid)
	}
	if isExternal {
		t.Error("expected isExternal=false for axe set device")
	}
}

func TestResolveAxeSimulator_PreferredUDID_NotFound(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown"},
		},
	}

	_, _, _, err := ResolveAxeSimulator(runner, "MISSING")
	if err == nil {
		t.Fatal("expected error for missing UDID, got nil")
	}
}

func TestResolveAxeSimulator_AutoSelectShutdown(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Booted"},
			{Name: "axe iPhone 16 Pro (2)", UDID: "BBB", State: "Shutdown"},
		},
	}

	udid, _, isExternal, err := ResolveAxeSimulator(runner, "")
	if err != nil {
		t.Fatalf("ResolveAxeSimulator: %v", err)
	}
	if udid != "BBB" {
		t.Errorf("expected BBB (shutdown), got %s", udid)
	}
	if isExternal {
		t.Error("expected isExternal=false for auto-selected axe device")
	}
}

func TestResolveAxeSimulator_AutoCreate(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{},
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPhone 16 Pro", "udid": "SRC-1", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
				]
			}
		}`),
		createdUDID: "NEW-1",
	}

	udid, _, isExternal, err := ResolveAxeSimulator(runner, "")
	if err != nil {
		t.Fatalf("ResolveAxeSimulator: %v", err)
	}
	if udid != "NEW-1" {
		t.Errorf("expected NEW-1, got %s", udid)
	}
	if isExternal {
		t.Error("expected isExternal=false for auto-created device")
	}
}

func TestResolveAxeSimulator_CreateError(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{},
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPhone 16 Pro", "udid": "SRC-1", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
				]
			}
		}`),
		createErr: fmt.Errorf("simctl create failed"),
	}

	_, _, _, err := ResolveAxeSimulator(runner, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseDevicesJSON(t *testing.T) {
	data := []byte(`{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
				{"name": "iPhone 16", "udid": "AAA", "state": "Shutdown"},
				{"name": "iPhone 16 Pro", "udid": "BBB", "state": "Booted"}
			],
			"com.apple.CoreSimulator.SimRuntime.iOS-17-0": [
				{"name": "iPhone 15", "udid": "CCC", "state": "Shutdown"}
			]
		}
	}`)

	devices, err := parseDevicesJSON(data)
	if err != nil {
		t.Fatalf("parseDevicesJSON: %v", err)
	}

	if len(devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devices))
	}

	// Should be sorted by name.
	if devices[0].Name != "iPhone 15" {
		t.Errorf("expected first device iPhone 15, got %s", devices[0].Name)
	}

	// RuntimeID should be populated.
	for _, d := range devices {
		if d.RuntimeID == "" {
			t.Errorf("RuntimeID not populated for %s", d.Name)
		}
	}
}

func TestFindDefaultDeviceSpec(t *testing.T) {
	runner := &simFakeSimctlRunner{
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPhone 16 Pro", "udid": "DDD", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"},
					{"name": "iPhone 16", "udid": "CCC", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16"}
				]
			}
		}`),
	}

	deviceType, runtime, err := FindDefaultDeviceSpec(runner)
	if err != nil {
		t.Fatalf("FindDefaultDeviceSpec: %v", err)
	}
	if deviceType != "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro" {
		t.Errorf("expected iPhone 16 Pro device type, got %s", deviceType)
	}
	if runtime != "com.apple.CoreSimulator.SimRuntime.iOS-18-2" {
		t.Errorf("expected iOS 18.2 runtime, got %s", runtime)
	}
}

func TestFindDefaultDeviceSpec_NoIPhone(t *testing.T) {
	runner := &simFakeSimctlRunner{
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPad Air", "udid": "AAA", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPad-Air"}
				]
			}
		}`),
	}

	_, _, err := FindDefaultDeviceSpec(runner)
	if err == nil {
		t.Fatal("expected error when no iPhone found, got nil")
	}
}

func TestParseDevicesJSON_MalformedJSON(t *testing.T) {
	_, err := parseDevicesJSON([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestResolveAxeSimulator_PreferredUDID_FallbackToStandardSet(t *testing.T) {
	runner := &simFakeSimctlRunner{
		// axe set has no matching device
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown"},
		},
		// standard set has the requested device
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPhone 16 Pro", "udid": "STD-UUID", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
				]
			}
		}`),
	}

	udid, deviceSetPath, isExternal, err := ResolveAxeSimulator(runner, "STD-UUID")
	if err != nil {
		t.Fatalf("ResolveAxeSimulator: %v", err)
	}
	if udid != "STD-UUID" {
		t.Errorf("expected STD-UUID, got %s", udid)
	}
	if deviceSetPath != "" {
		t.Errorf("expected empty deviceSetPath for standard set device, got %q", deviceSetPath)
	}
	if !isExternal {
		t.Error("expected isExternal=true for standard set device")
	}
}

func TestResolveAxeSimulator_PreferredUDID_NotFoundAnywhere(t *testing.T) {
	runner := &simFakeSimctlRunner{
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown"},
		},
		allDevicesJSON: []byte(`{
			"devices": {
				"com.apple.CoreSimulator.SimRuntime.iOS-18-2": [
					{"name": "iPhone 16 Pro", "udid": "OTHER-UUID", "state": "Shutdown",
					 "deviceTypeIdentifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
				]
			}
		}`),
	}

	_, _, _, err := ResolveAxeSimulator(runner, "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error when UDID not found in either set, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %s", err.Error())
	}
}
