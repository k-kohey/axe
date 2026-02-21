package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestFindLatestIPhone(t *testing.T) {
	// Test the selection logic by constructing the same JSON structure
	// that simctl returns and checking the parsing.
	simctlJSON := `{
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
	}`

	var result struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(simctlJSON), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Replicate the selection logic from findLatestIPhone.
	var best simDevice
	var bestVersion [2]int
	for runtime, devices := range result.Devices {
		major, minor := parseIOSVersion(runtime)
		if major < 0 {
			continue
		}
		for _, d := range devices {
			if !strings.Contains(d.Name, "iPhone") {
				continue
			}
			v := [2]int{major, minor}
			if v[0] > bestVersion[0] || (v[0] == bestVersion[0] && v[1] > bestVersion[1]) ||
				(v == bestVersion && d.Name > best.Name) {
				best = d
				bestVersion = v
			}
		}
	}

	// Expect iPhone 16 Pro (iOS 18.2, lexicographically largest on same version).
	if best.UDID != "DDD" {
		t.Errorf("expected iPhone 16 Pro (DDD), got %s (%s)", best.Name, best.UDID)
	}
	if best.Name != "iPhone 16 Pro" {
		t.Errorf("expected name iPhone 16 Pro, got %s", best.Name)
	}
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
