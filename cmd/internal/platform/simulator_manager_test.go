package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// managerFakeSimctlRunner is a SimctlRunner fake for testing simulator manager functions.
type managerFakeSimctlRunner struct {
	devices         []simDevice
	runtimesJSON    []byte
	deviceTypesJSON []byte
	createErr       error
	deleteErr       error
	createdUDID     string
}

func (f *managerFakeSimctlRunner) ListDevices(_ context.Context, _ string) ([]simDevice, error) {
	return f.devices, nil
}

func (f *managerFakeSimctlRunner) Clone(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}

func (f *managerFakeSimctlRunner) Create(_ context.Context, name, deviceType, runtime, _ string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	udid := f.createdUDID
	if udid == "" {
		udid = "NEW-UDID-1"
	}
	f.devices = append(f.devices, simDevice{
		Name:                 name,
		UDID:                 udid,
		State:                "Shutdown",
		DeviceTypeIdentifier: deviceType,
		RuntimeID:            runtime,
	})
	return udid, nil
}

func (f *managerFakeSimctlRunner) Shutdown(_ context.Context, _, _ string) error {
	return nil
}

func (f *managerFakeSimctlRunner) Delete(_ context.Context, udid, _ string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	var remaining []simDevice
	for _, d := range f.devices {
		if d.UDID != udid {
			remaining = append(remaining, d)
		}
	}
	f.devices = remaining
	return nil
}

func (f *managerFakeSimctlRunner) ListAllDevices(_ context.Context, _ bool) ([]byte, error) {
	result := struct {
		Devices map[string][]simDevice `json:"devices"`
	}{
		Devices: make(map[string][]simDevice),
	}
	for _, d := range f.devices {
		result.Devices[d.RuntimeID] = append(result.Devices[d.RuntimeID], d)
	}
	data, err := json.Marshal(result)
	return data, err
}

func (f *managerFakeSimctlRunner) ListRuntimes(_ context.Context) ([]byte, error) {
	if f.runtimesJSON != nil {
		return f.runtimesJSON, nil
	}
	return []byte(`{"runtimes":[]}`), nil
}

func (f *managerFakeSimctlRunner) ListDeviceTypes(_ context.Context) ([]byte, error) {
	if f.deviceTypesJSON != nil {
		return f.deviceTypesJSON, nil
	}
	return []byte(`{"devicetypes":[]}`), nil
}

func TestNextSequenceNumber(t *testing.T) {
	tests := []struct {
		name     string
		devices  []ManagedSimulator
		baseName string
		want     int
	}{
		{
			name:     "no devices",
			devices:  nil,
			baseName: "iPhone 16 Pro",
			want:     1,
		},
		{
			name: "one device exists",
			devices: []ManagedSimulator{
				{Name: "axe iPhone 16 Pro (1)"},
			},
			baseName: "iPhone 16 Pro",
			want:     2,
		},
		{
			name: "gap in sequence returns max+1",
			devices: []ManagedSimulator{
				{Name: "axe iPhone 16 Pro (1)"},
				{Name: "axe iPhone 16 Pro (3)"},
			},
			baseName: "iPhone 16 Pro",
			want:     4,
		},
		{
			name: "different device type ignored",
			devices: []ManagedSimulator{
				{Name: "axe iPad Air (1)"},
				{Name: "axe iPhone 16 Pro (2)"},
			},
			baseName: "iPhone 16 Pro",
			want:     3,
		},
		{
			name: "no matching device type",
			devices: []ManagedSimulator{
				{Name: "axe iPad Air (1)"},
				{Name: "axe iPad Air (2)"},
			},
			baseName: "iPhone 16 Pro",
			want:     1,
		},
		{
			name: "old-style name without sequence number is ignored",
			devices: []ManagedSimulator{
				{Name: "axe iPhone Air"},
			},
			baseName: "iPhone Air",
			want:     1,
		},
		{
			name: "mixed old-style and new-style names",
			devices: []ManagedSimulator{
				{Name: "axe iPhone 16 Pro"},
				{Name: "axe iPhone 16 Pro (2)"},
			},
			baseName: "iPhone 16 Pro",
			want:     3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextSequenceNumber(tt.devices, tt.baseName)
			if got != tt.want {
				t.Errorf("nextSequenceNumber() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHumanReadableRuntime(t *testing.T) {
	tests := []struct {
		runtime string
		want    string
	}{
		{"com.apple.CoreSimulator.SimRuntime.iOS-18-2", "iOS 18.2"},
		{"com.apple.CoreSimulator.SimRuntime.iOS-26-0", "iOS 26.0"},
		{"com.apple.CoreSimulator.SimRuntime.tvOS-18-0", "tvOS 18.0"},
		{"com.apple.CoreSimulator.SimRuntime.watchOS-11-0", "watchOS 11.0"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			got := humanReadableRuntime(tt.runtime)
			if got != tt.want {
				t.Errorf("humanReadableRuntime(%q) = %q, want %q", tt.runtime, got, tt.want)
			}
		})
	}
}

func TestParseAvailable_EmptyInputs(t *testing.T) {
	// Both empty → no results, no error.
	result, err := parseAvailable(
		[]byte(`{"runtimes":[]}`),
		[]byte(`{"devicetypes":[]}`),
	)
	if err != nil {
		t.Fatalf("parseAvailable with empty inputs: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 device types, got %d", len(result))
	}
}

func TestParseAvailable_MalformedJSON(t *testing.T) {
	_, err := parseAvailable([]byte(`{not json`), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error on malformed runtimes JSON, got nil")
	}

	_, err = parseAvailable([]byte(`{"runtimes":[]}`), []byte(`{not json`))
	if err == nil {
		t.Fatal("expected error on malformed devicetypes JSON, got nil")
	}
}

func TestParseAvailable(t *testing.T) {
	runtimesJSON := []byte(`{
		"runtimes": [
			{
				"identifier": "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
				"name": "iOS 18.2",
				"supportedDeviceTypes": [
					{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro", "name": "iPhone 16 Pro"},
					{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16", "name": "iPhone 16"}
				]
			},
			{
				"identifier": "com.apple.CoreSimulator.SimRuntime.tvOS-18-0",
				"name": "tvOS 18.0",
				"supportedDeviceTypes": [
					{"identifier": "com.apple.CoreSimulator.SimDeviceType.Apple-TV", "name": "Apple TV"}
				]
			}
		]
	}`)

	deviceTypesJSON := []byte(`{
		"devicetypes": [
			{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro", "name": "iPhone 16 Pro"},
			{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16", "name": "iPhone 16"},
			{"identifier": "com.apple.CoreSimulator.SimDeviceType.Apple-TV", "name": "Apple TV"},
			{"identifier": "com.apple.CoreSimulator.SimDeviceType.NoRuntime", "name": "No Runtime Device"}
		]
	}`)

	result, err := parseAvailable(runtimesJSON, deviceTypesJSON)
	if err != nil {
		t.Fatalf("parseAvailable: %v", err)
	}

	// "NoRuntime" should be excluded (no available runtimes).
	if len(result) != 3 {
		t.Fatalf("expected 3 device types, got %d", len(result))
	}

	// Build a map for easier lookup.
	byID := make(map[string]AvailableDeviceType)
	for _, dt := range result {
		byID[dt.Identifier] = dt
	}

	// iPhone 16 Pro should have iOS 18.2.
	iphone16Pro, ok := byID["com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"]
	if !ok {
		t.Fatal("iPhone 16 Pro not found")
	}
	if iphone16Pro.Name != "iPhone 16 Pro" {
		t.Errorf("name = %q, want %q", iphone16Pro.Name, "iPhone 16 Pro")
	}
	if len(iphone16Pro.Runtimes) != 1 || iphone16Pro.Runtimes[0].Identifier != "com.apple.CoreSimulator.SimRuntime.iOS-18-2" {
		t.Errorf("unexpected runtimes for iPhone 16 Pro: %v", iphone16Pro.Runtimes)
	}

	// Apple TV should have tvOS 18.0.
	appleTV, ok := byID["com.apple.CoreSimulator.SimDeviceType.Apple-TV"]
	if !ok {
		t.Fatal("Apple TV not found")
	}
	if len(appleTV.Runtimes) != 1 || appleTV.Runtimes[0].Name != "tvOS 18.0" {
		t.Errorf("unexpected runtimes for Apple TV: %v", appleTV.Runtimes)
	}
}

func TestListManaged_WithFakeRunner(t *testing.T) {
	runner := &managerFakeSimctlRunner{
		devices: []simDevice{
			{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown", RuntimeID: "com.apple.CoreSimulator.SimRuntime.iOS-18-2"},
			{Name: "axe iPad Air (1)", UDID: "BBB", State: "Booted", RuntimeID: "com.apple.CoreSimulator.SimRuntime.iOS-17-0"},
		},
	}

	store := NewConfigStoreWithPath(t.TempDir() + "/config.json")
	_ = store.SetDefault("AAA")

	managed, err := ListManaged(runner, store)
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}

	if len(managed) != 2 {
		t.Fatalf("expected 2 managed simulators, got %d", len(managed))
	}

	// Verify runtime is human-readable.
	byUDID := make(map[string]ManagedSimulator)
	for _, m := range managed {
		byUDID[m.UDID] = m
	}

	aaa := byUDID["AAA"]
	if aaa.Runtime != "iOS 18.2" {
		t.Errorf("expected runtime 'iOS 18.2', got %q", aaa.Runtime)
	}
	if !aaa.IsDefault {
		t.Error("expected AAA to be default")
	}

	bbb := byUDID["BBB"]
	if bbb.State != "Booted" {
		t.Errorf("expected state 'Booted', got %q", bbb.State)
	}
	if bbb.IsDefault {
		t.Error("expected BBB to not be default")
	}
}

func TestListManaged_EmptySet(t *testing.T) {
	runner := &managerFakeSimctlRunner{}
	store := NewConfigStoreWithPath(t.TempDir() + "/config.json")

	managed, err := ListManaged(runner, store)
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}
	if managed != nil {
		t.Errorf("expected nil for empty set, got %v", managed)
	}
}

func TestListAvailable_WithFakeRunner(t *testing.T) {
	runner := &managerFakeSimctlRunner{
		runtimesJSON: []byte(`{
			"runtimes": [{
				"identifier": "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
				"name": "iOS 18.2",
				"supportedDeviceTypes": [
					{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}
				]
			}]
		}`),
		deviceTypesJSON: []byte(`{
			"devicetypes": [
				{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro", "name": "iPhone 16 Pro"}
			]
		}`),
	}

	available, err := ListAvailable(runner)
	if err != nil {
		t.Fatalf("ListAvailable: %v", err)
	}

	if len(available) != 1 {
		t.Fatalf("expected 1 device type, got %d", len(available))
	}
	if available[0].Name != "iPhone 16 Pro" {
		t.Errorf("expected 'iPhone 16 Pro', got %q", available[0].Name)
	}
	if len(available[0].Runtimes) != 1 || available[0].Runtimes[0].Name != "iOS 18.2" {
		t.Errorf("unexpected runtimes: %v", available[0].Runtimes)
	}
}

func TestDeviceTypeBaseName_WithFakeRunner(t *testing.T) {
	t.Run("found in devicetypes", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{
			deviceTypesJSON: []byte(`{
				"devicetypes": [
					{"identifier": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro", "name": "iPhone 16 Pro"}
				]
			}`),
		}
		got := deviceTypeBaseName(runner, "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro")
		if got != "iPhone 16 Pro" {
			t.Errorf("expected 'iPhone 16 Pro', got %q", got)
		}
	})

	t.Run("fallback when not found", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{
			deviceTypesJSON: []byte(`{"devicetypes":[]}`),
		}
		got := deviceTypeBaseName(runner, "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro")
		if got != "iPhone 16 Pro" {
			t.Errorf("expected fallback 'iPhone 16 Pro', got %q", got)
		}
	})
}

func TestRemove_WithFakeRunner(t *testing.T) {
	t.Run("removes shutdown device", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{
			devices: []simDevice{
				{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown", RuntimeID: testRuntime},
			},
		}
		store := NewConfigStoreWithPath(t.TempDir() + "/config.json")
		_ = store.SetDefault("AAA")

		err := Remove(runner, "AAA", store)
		if err != nil {
			t.Fatalf("Remove: %v", err)
		}

		// Default should be cleared.
		defaultUDID, _ := store.GetDefault()
		if defaultUDID != "" {
			t.Errorf("expected default cleared, got %q", defaultUDID)
		}
	})

	t.Run("rejects booted device", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{
			devices: []simDevice{
				{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Booted", RuntimeID: testRuntime},
			},
		}
		store := NewConfigStoreWithPath(t.TempDir() + "/config.json")

		err := Remove(runner, "AAA", store)
		if err == nil {
			t.Fatal("expected error for booted device, got nil")
		}
	})

	t.Run("not found", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{}
		store := NewConfigStoreWithPath(t.TempDir() + "/config.json")

		err := Remove(runner, "MISSING", store)
		if err == nil {
			t.Fatal("expected error for missing device, got nil")
		}
	})

	t.Run("delete error propagated", func(t *testing.T) {
		runner := &managerFakeSimctlRunner{
			devices: []simDevice{
				{Name: "axe iPhone 16 Pro (1)", UDID: "AAA", State: "Shutdown", RuntimeID: testRuntime},
			},
			deleteErr: fmt.Errorf("simctl delete failed"),
		}
		store := NewConfigStoreWithPath(t.TempDir() + "/config.json")

		err := Remove(runner, "AAA", store)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
