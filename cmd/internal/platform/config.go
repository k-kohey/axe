package platform

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"howett.net/plist"
)

// ReadRC parses the .axerc file in the current directory and returns
// all key-value pairs as a map. The file format is KEY=VALUE, one per line.
// Lines starting with '#' are treated as comments. Returns an empty map
// if the file does not exist or cannot be read.
func ReadRC() map[string]string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	f, err := os.Open(filepath.Join(cwd, ".axerc"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	m := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// ResolveAppName returns APP_NAME from the flag value or .axerc file in the current directory.
func ResolveAppName(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}

	if rc := ReadRC(); rc != nil {
		if v := rc["APP_NAME"]; v != "" {
			return v, nil
		}
	}

	return "", fmt.Errorf("APP_NAME not specified. Use --app <name> or set APP_NAME in .axerc")
}

// SimProcess represents a running app process on an iOS simulator.
type SimProcess struct {
	PID        int    `json:"pid"`
	App        string `json:"app"`
	BundleID   string `json:"bundle_id"`
	DeviceUDID string `json:"device_udid"`
	DeviceName string `json:"device_name"`
}

var coreSimRe = regexp.MustCompile(`CoreSimulator/Devices/([0-9A-Fa-f-]+)/`)
var appNameRe = regexp.MustCompile(`.*/([^/]+)\.app/`)
var appPathRe = regexp.MustCompile(`(/\S+\.app)/`)

// ListSimulatorProcesses returns all app processes running on iOS simulators.
func ListSimulatorProcesses() ([]SimProcess, error) {
	deviceMap, err := buildDeviceMap()
	if err != nil {
		return nil, err
	}

	out, err := exec.Command("ps", "-eo", "pid,args").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run ps: %w", err)
	}

	return parseSimulatorProcesses(string(out), deviceMap), nil
}

// parseSimulatorProcesses parses ps output and returns simulator app processes.
func parseSimulatorProcesses(psOutput string, deviceMap map[string]string) []SimProcess {
	var procs []SimProcess
	for _, line := range strings.Split(strings.TrimSpace(psOutput), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "CoreSimulator/Devices/") {
			continue
		}
		if strings.Contains(line, "launchd_sim") {
			continue
		}

		udidMatch := coreSimRe.FindStringSubmatch(line)
		if udidMatch == nil {
			continue
		}
		udid := udidMatch[1]

		appMatch := appNameRe.FindStringSubmatch(line)
		if appMatch == nil {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		deviceName := deviceMap[udid]
		if deviceName == "" {
			deviceName = "unknown"
		}

		bundleID := ""
		if pathMatch := appPathRe.FindStringSubmatch(line); pathMatch != nil {
			bundleID = readBundleID(pathMatch[1])
		}

		procs = append(procs, SimProcess{
			PID:        pid,
			App:        appMatch[1],
			BundleID:   bundleID,
			DeviceUDID: udid,
			DeviceName: deviceName,
		})
	}
	return procs
}

// readBundleID reads CFBundleIdentifier from the Info.plist inside an .app bundle.
func readBundleID(appPath string) string {
	data, err := os.ReadFile(filepath.Join(appPath, "Info.plist"))
	if err != nil {
		return ""
	}
	var info struct {
		BundleID string `plist:"CFBundleIdentifier"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.BundleID
}

// FindProcess returns the PID of the named process by searching simulator processes.
// If device is non-empty and not "booted", only processes on that device are matched.
// When multiple processes match, the first PID is used and a warning is logged.
func FindProcess(name string, device string) (int, error) {
	procs, err := ListSimulatorProcesses()
	if err != nil {
		return 0, fmt.Errorf("process '%s' not found. Is the app running?", name)
	}

	matched := matchProcesses(procs, name, device)

	if len(matched) == 0 {
		return 0, fmt.Errorf("process '%s' not found. Is the app running?", name)
	}

	if len(matched) > 1 {
		pids := make([]string, len(matched))
		for i, p := range matched {
			pids[i] = fmt.Sprintf("%d(%s)", p.PID, p.DeviceName)
		}
		slog.Warn("Multiple processes found", "name", name, "pids", pids, "selected", matched[0].PID)
	}

	return matched[0].PID, nil
}

// matchProcesses filters processes by app name and optionally by device.
// The device value is matched against both DeviceUDID and DeviceName,
// since simctl accepts either form. If device is empty or "booted",
// all matching processes are returned.
func matchProcesses(procs []SimProcess, name string, device string) []SimProcess {
	var matched []SimProcess
	for _, p := range procs {
		if p.App != name {
			continue
		}
		if device != "" && device != "booted" && p.DeviceUDID != device && p.DeviceName != device {
			continue
		}
		matched = append(matched, p)
	}
	return matched
}

func buildDeviceMap() (map[string]string, error) {
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run simctl: %w", err)
	}

	var result struct {
		Devices map[string][]struct {
			Name string `json:"name"`
			UDID string `json:"udid"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse simctl JSON: %w", err)
	}

	m := make(map[string]string)
	for _, devices := range result.Devices {
		for _, d := range devices {
			m[d.UDID] = d.Name
		}
	}
	return m, nil
}
