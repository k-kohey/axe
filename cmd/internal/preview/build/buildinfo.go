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

// BuildInfo is the compile information an external build tool (e.g. Bazel) emits instead of
// xcodebuild; the producer builds the app with `-enable-implicit-dynamic` and `-enable-private-imports`.
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

// resolvePaths makes every relative path in the build info absolute against root;
// absolute paths are kept. The producer may write paths relative to the project.
func (b *BuildInfo) resolvePaths(root string) {
	each := func(ps []string) {
		for i, p := range ps {
			ps[i] = resolveAgainst(root, p)
		}
	}
	b.IndexStorePath = resolveAgainst(root, b.IndexStorePath)
	b.AppBundlePath = resolveAgainst(root, b.AppBundlePath)
	each(b.SourceFiles)
	each(b.ModuleSearchPaths)
	each(b.FrameworkSearchPaths)
}

// resolveAgainst joins a relative path onto root; absolute and empty paths are kept.
func resolveAgainst(root, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
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
	// dedup drops repeats and the built products dir (already searched), keeping order.
	dedup := func(paths []string) (out []string) {
		seen := map[string]bool{s.BuiltProductsDir: true}
		for _, p := range paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		return out
	}
	args := b.SwiftcArguments
	for i, arg := range args {
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
	s.ExtraIncludePaths = dedup(b.ModuleSearchPaths)
	s.ExtraFrameworkPaths = dedup(b.FrameworkSearchPaths)
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
// Relative paths in the build info are resolved against projectRoot. The index store named
// by the build info is linked to the location axe reads it from.
func PrepareFromBuildInfo(path, projectRoot string, dirs ProjectDirs) (*Result, error) {
	info, err := LoadBuildInfo(path)
	if err != nil {
		return nil, err
	}
	info.resolvePaths(projectRoot)
	s, err := info.Settings()
	if err != nil {
		return nil, err
	}
	for i, m := range s.ExtraModuleMapFiles {
		s.ExtraModuleMapFiles[i] = resolveAgainst(projectRoot, m)
	}
	link := dirs.IndexStorePath()
	if info.IndexStorePath == "" {
		// Drop a link or store left by an earlier run so it is not read as current.
		_ = os.RemoveAll(link)
	} else {
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
