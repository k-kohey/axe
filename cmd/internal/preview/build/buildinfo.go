package build

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BuildInfo is the build-system-independent compile information emitted by an external
// build tool, e.g. a Bazel wrapper (schemaVersion 1). When a BuildInfo file is given,
// axe does not invoke xcodebuild: the producer is responsible for building the app
// with `-enable-implicit-dynamic` and `-enable-private-imports`.
type BuildInfo struct {
	SchemaVersion          int      `json:"schemaVersion"`
	Module                 string   `json:"module"`
	Configuration          string   `json:"configuration"`
	Provider               string   `json:"provider"`
	SwiftcArguments        []string `json:"swiftcArguments"`
	SourceFiles            []string `json:"sourceFiles"`
	ModuleSearchPaths      []string `json:"moduleSearchPaths"`
	FrameworkSearchPaths   []string `json:"frameworkSearchPaths"`
	IndexStorePath         string   `json:"indexStorePath"`
	TransitiveDependencies []string `json:"transitiveDependencies"`
	AppBundlePath          string   `json:"appBundlePath"`
	AppBundleID            string   `json:"appBundleId"`
}

// LoadBuildInfo reads and validates a BuildInfo JSON file.
func LoadBuildInfo(path string) (*BuildInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading build info: %w", err)
	}
	var info BuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("decoding build info %s: %w", path, err)
	}
	if info.SchemaVersion != 1 {
		return nil, fmt.Errorf("build info %s: unsupported schemaVersion %d (expected 1)", path, info.SchemaVersion)
	}
	if info.AppBundlePath == "" || info.AppBundleID == "" {
		return nil, fmt.Errorf("build info %s: appBundlePath and appBundleId are required for previews (is %q linked into an app?)", path, info.Module)
	}
	return &info, nil
}

var targetTriple = regexp.MustCompile(`-ios([0-9.]+)-simulator$`)

// Settings converts BuildInfo into the Settings axe uses for thunk compilation and launching.
func (b *BuildInfo) Settings() (*Settings, error) {
	appName := strings.TrimSuffix(filepath.Base(b.AppBundlePath), ".app")
	s := &Settings{
		ModuleName:       appName,
		BundleID:         "axe." + b.AppBundleID,
		OriginalBundleID: b.AppBundleID,
		BuiltProductsDir: filepath.Dir(b.AppBundlePath),
	}
	args := b.SwiftcArguments
	seenI := map[string]bool{s.BuiltProductsDir: true}
	seenF := map[string]bool{s.BuiltProductsDir: true}
	for i := range args {
		arg := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case arg == "-target" && next != "":
			if m := targetTriple.FindStringSubmatch(next); m != nil {
				s.DeploymentTarget = m[1]
			}
		case arg == "-swift-version" && next != "":
			s.SwiftVersion = next
		case arg == "-Xcc" && strings.HasPrefix(next, "-fmodule-map-file="):
			s.ExtraModuleMapFiles = append(s.ExtraModuleMapFiles, strings.TrimPrefix(next, "-fmodule-map-file="))
		}
	}
	for _, p := range b.ModuleSearchPaths {
		if !seenI[p] {
			seenI[p] = true
			s.ExtraIncludePaths = append(s.ExtraIncludePaths, p)
		}
	}
	for _, p := range b.FrameworkSearchPaths {
		if !seenF[p] {
			seenF[p] = true
			s.ExtraFrameworkPaths = append(s.ExtraFrameworkPaths, p)
		}
	}
	s.SourceModules = map[string]string{}
	for _, f := range b.SourceFiles {
		s.SourceModules[f] = b.Module
		if resolved, err := filepath.EvalSymlinks(f); err == nil {
			s.SourceModules[resolved] = b.Module
		}
	}
	if s.DeploymentTarget == "" {
		return nil, fmt.Errorf("build info: no iOS simulator -target in swiftcArguments")
	}
	return s, nil
}

// PrepareFromBuildInfo is Prepare for projects whose build is driven by another tool.
// The index store named by the build info is linked to the location axe reads it from.
func PrepareFromBuildInfo(path string, dirs ProjectDirs) (*Result, error) {
	info, err := LoadBuildInfo(path)
	if err != nil {
		return nil, err
	}
	s, err := info.Settings()
	if err != nil {
		return nil, err
	}
	if info.IndexStorePath != "" {
		link := dirs.IndexStorePath()
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return nil, fmt.Errorf("creating index store directory: %w", err)
		}
		if current, err := os.Readlink(link); err != nil || current != info.IndexStorePath {
			_ = os.RemoveAll(link)
			if err := os.Symlink(info.IndexStorePath, link); err != nil {
				return nil, fmt.Errorf("linking index store: %w", err)
			}
		}
	}
	slog.Info("Using build info", "path", path, "provider", info.Provider, "module", info.Module, "app", info.AppBundlePath)
	return &Result{Settings: s, Dirs: dirs, Built: false}, nil
}
