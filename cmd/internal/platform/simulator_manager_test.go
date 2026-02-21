package platform

import (
	"testing"
)

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
