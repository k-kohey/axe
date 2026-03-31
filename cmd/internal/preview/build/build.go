package build

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/k-kohey/axe/internal/preview/buildlock"
)

// Result holds the output of a Prepare call.
type Result struct {
	Settings *Settings
	Dirs     ProjectDirs
	Built    bool // true if xcodebuild was invoked (false when reusing a previous build)
}

// Prepare runs the full build pipeline: fetch settings, optionally build,
// and extract compiler paths. This is the high-level entry point.
func Prepare(ctx context.Context, pc ProjectConfig, dirs ProjectDirs, reuse bool, r Runner) (*Result, error) {
	s, err := FetchSettings(ctx, pc, dirs, r)
	if err != nil {
		return nil, err
	}

	built := false
	if reuse && HasPreviousBuild(s, dirs) {
		slog.Info("Reusing previous build", "buildDir", dirs.Build)
	} else {
		if err := Run(ctx, pc, dirs, r); err != nil {
			return nil, err
		}
		built = true
	}

	ExtractCompilerPaths(ctx, s, dirs)

	return &Result{Settings: s, Dirs: dirs, Built: built}, nil
}

// FetchSettings runs "xcodebuild -showBuildSettings" and parses the output
// into a Settings struct.
func FetchSettings(ctx context.Context, pc ProjectConfig, dirs ProjectDirs, r Runner) (*Settings, error) {
	args := append(
		[]string{"xcodebuild", "-showBuildSettings"},
		pc.XcodebuildArgs()...,
	)
	args = append(args, "-destination", "generic/platform=iOS Simulator")

	out, err := r.FetchBuildSettings(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("xcodebuild -showBuildSettings failed: %w\n%s", err, out)
	}

	keys := map[string]string{
		"PRODUCT_MODULE_NAME":        "",
		"PRODUCT_BUNDLE_IDENTIFIER":  "",
		"IPHONEOS_DEPLOYMENT_TARGET": "",
		"SWIFT_VERSION":              "",
	}

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		for k := range keys {
			prefix := k + " = "
			if after, ok := strings.CutPrefix(line, prefix); ok {
				keys[k] = strings.TrimSpace(after)
			}
		}
	}

	config := pc.Configuration
	if config == "" {
		config = "Debug"
	}
	builtProductsDir := filepath.Join(dirs.Build, "Build", "Products", config+"-iphonesimulator")

	s := &Settings{
		ModuleName:       keys["PRODUCT_MODULE_NAME"],
		BundleID:         "axe." + keys["PRODUCT_BUNDLE_IDENTIFIER"],
		OriginalBundleID: keys["PRODUCT_BUNDLE_IDENTIFIER"],
		BuiltProductsDir: builtProductsDir,
		DeploymentTarget: keys["IPHONEOS_DEPLOYMENT_TARGET"],
		SwiftVersion:     keys["SWIFT_VERSION"],
	}

	if s.ModuleName == "" {
		return nil, fmt.Errorf("PRODUCT_MODULE_NAME not found in build settings")
	}
	if s.OriginalBundleID == "" {
		return nil, fmt.Errorf("PRODUCT_BUNDLE_IDENTIFIER not found in build settings")
	}
	if s.DeploymentTarget == "" {
		return nil, fmt.Errorf("IPHONEOS_DEPLOYMENT_TARGET not found in build settings")
	}

	slog.Debug("Build settings",
		"module", s.ModuleName,
		"bundle", s.BundleID,
		"products", s.BuiltProductsDir,
		"target", s.DeploymentTarget,
		"swiftVersion", s.SwiftVersion,
	)
	return s, nil
}

// Run executes "xcodebuild build" with the flags required for axe preview
// (dynamic replacement and private imports).
func Run(ctx context.Context, pc ProjectConfig, dirs ProjectDirs, r Runner) error {
	lock := buildlock.New(dirs.Build)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquiring build lock: %w", err)
	}
	defer lock.Unlock()

	args := append(
		[]string{"xcodebuild", "build"},
		pc.XcodebuildArgs()...,
	)
	args = append(args,
		"-destination", "generic/platform=iOS Simulator",
		"-derivedDataPath", dirs.Build,
		"OTHER_SWIFT_FLAGS=-Xfrontend -enable-implicit-dynamic -Xfrontend -enable-private-imports",
	)

	out, err := r.Build(ctx, args)
	if err != nil {
		return fmt.Errorf("xcodebuild build failed: %w\n%s", err, out)
	}

	return nil
}

// ExtractCompilerPaths reads the swiftc response file (.resp) generated
// during the xcodebuild build and extracts -I, -F, and -fmodule-map-file=
// flags. These are required so that the thunk compilation can resolve
// transitive SPM dependencies (C module headers, framework bundles, and
// generated ObjC module maps) that xcodebuild manages internally.
func ExtractCompilerPaths(ctx context.Context, s *Settings, dirs ProjectDirs) {
	// Clear previously extracted paths so that re-extraction after a rebuild
	// produces fresh results (idempotent).
	s.ExtraIncludePaths = nil
	s.ExtraFrameworkPaths = nil
	s.ExtraModuleMapFiles = nil

	lock := buildlock.New(dirs.Build)
	if err := lock.RLock(ctx); err != nil {
		slog.Warn("Failed to acquire read lock for compiler paths", "err", err)
		return
	}
	defer lock.RUnlock()
	// Response files live under:
	//   <dirs.Build>/Build/Intermediates.noindex/
	//     <project>.build/<config>-iphonesimulator/<module>.build/
	//     Objects-normal/arm64/arguments-<hash>.resp
	respPattern := filepath.Join(
		dirs.Build, "Build", "Intermediates.noindex",
		"*", "*", s.ModuleName+".build", "Objects-normal", "arm64", "arguments-*.resp",
	)
	matches, _ := filepath.Glob(respPattern)
	if len(matches) > 0 {
		// Read the first matching resp file.
		data, err := os.ReadFile(matches[0])
		if err != nil {
			slog.Warn("Failed to read swiftc response file", "path", matches[0], "err", err)
			return
		}
		extractCompilerPathsFromResp(s, string(data))
		return
	}

	slog.Debug("No swiftc response file found", "pattern", respPattern)

	// Xcode 26 no longer emits arguments-*.resp for Swift compilation in some
	// configurations. Fall back to the explicit dependency manifest that now
	// contains clang module map paths for SPM packages and SDK overlays.
	depsPattern := filepath.Join(
		dirs.Build, "Build", "Intermediates.noindex",
		"*", "*", s.ModuleName+".build", "Objects-normal", "arm64", "*-dependencies-*.json",
	)
	depMatches, _ := filepath.Glob(depsPattern)
	if len(depMatches) == 0 {
		slog.Debug("No dependency manifest found for compiler paths", "pattern", depsPattern)
		return
	}

	if err := extractCompilerPathsFromDependencies(s, dirs.Build, depMatches[0]); err != nil {
		slog.Warn("Failed to extract compiler paths from dependency manifest", "path", depMatches[0], "err", err)
		return
	}
}

func extractCompilerPathsFromResp(s *Settings, data string) {
	seenI := map[string]bool{s.BuiltProductsDir: true}
	seenF := map[string]bool{s.BuiltProductsDir: true}
	seenM := map[string]bool{}

	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// -fmodule-map-file=<path> (single line)
		if after, ok := strings.CutPrefix(line, "-fmodule-map-file="); ok {
			p := after
			if p != "" && !seenM[p] {
				seenM[p] = true
				s.ExtraModuleMapFiles = append(s.ExtraModuleMapFiles, p)
			}
			continue
		}

		// -I<path> (combined) or -I\n<path> (split across two lines)
		if after, ok := strings.CutPrefix(line, "-I"); ok {
			p := after
			if p == "" && i+1 < len(lines) {
				i++
				p = lines[i]
			}
			if strings.HasSuffix(p, ".hmap") || p == "" || seenI[p] {
				continue
			}
			seenI[p] = true
			s.ExtraIncludePaths = append(s.ExtraIncludePaths, p)
			continue
		}

		// -F<path> (combined) or -F\n<path> (split across two lines)
		if after, ok := strings.CutPrefix(line, "-F"); ok {
			p := after
			if p == "" && i+1 < len(lines) {
				i++
				p = lines[i]
			}
			if p == "" || seenF[p] {
				continue
			}
			seenF[p] = true
			s.ExtraFrameworkPaths = append(s.ExtraFrameworkPaths, p)
			continue
		}
	}
	slog.Debug("Extracted paths from resp file",
		"includePaths", len(s.ExtraIncludePaths),
		"frameworkPaths", len(s.ExtraFrameworkPaths),
		"moduleMapFiles", len(s.ExtraModuleMapFiles),
	)
}

type dependencyManifestEntry struct {
	ClangModuleMapPath string `json:"clangModuleMapPath"`
}

var umbrellaDirectiveRE = regexp.MustCompile(`^(umbrella header|umbrella)\s+"([^"]+)"`)

func extractCompilerPathsFromDependencies(s *Settings, buildDir, manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	var entries []dependencyManifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parsing dependency manifest: %w", err)
	}

	seenI := map[string]bool{s.BuiltProductsDir: true}
	seenF := map[string]bool{s.BuiltProductsDir: true}
	seenM := map[string]bool{}

	addIncludePath := func(path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || path == "/" || seenI[path] {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		seenI[path] = true
		s.ExtraIncludePaths = append(s.ExtraIncludePaths, path)
	}
	addFrameworkPath := func(path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || path == "/" || seenF[path] {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		seenF[path] = true
		s.ExtraFrameworkPaths = append(s.ExtraFrameworkPaths, path)
	}
	addModuleMapPath := func(path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || seenM[path] {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		seenM[path] = true
		s.ExtraModuleMapFiles = append(s.ExtraModuleMapFiles, path)
	}
	addAncestors := func(path string, depth int) {
		current := filepath.Clean(path)
		for range depth {
			addIncludePath(current)
			next := filepath.Dir(current)
			if next == current {
				break
			}
			current = next
		}
	}

	generatedModuleMapsDir := filepath.Join(buildDir, "Build", "Intermediates.noindex", "GeneratedModuleMaps-iphonesimulator")
	if generatedModuleMapsDir != "" {
		addIncludePath(generatedModuleMapsDir)
	}

	for _, entry := range entries {
		if entry.ClangModuleMapPath == "" {
			continue
		}
		addModuleMapPath(entry.ClangModuleMapPath)

		moduleMapPath := filepath.Clean(entry.ClangModuleMapPath)
		if strings.HasSuffix(moduleMapPath, "/Modules/module.modulemap") {
			for current := filepath.Dir(moduleMapPath); current != "/" && current != "."; current = filepath.Dir(current) {
				if strings.HasSuffix(current, ".framework") {
					addFrameworkPath(filepath.Dir(current))
					addIncludePath(filepath.Join(current, "Headers"))
					break
				}
				if parent := filepath.Dir(current); parent == current {
					break
				}
			}
		}

		moduleMapData, err := os.ReadFile(moduleMapPath)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(moduleMapData), "\n") {
			m := umbrellaDirectiveRE.FindStringSubmatch(strings.TrimSpace(line))
			if len(m) != 3 {
				continue
			}

			targetPath := m[2]
			if !filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(filepath.Dir(moduleMapPath), targetPath)
			}
			targetPath = filepath.Clean(targetPath)
			info, err := os.Stat(targetPath)
			if err != nil {
				continue
			}
			if info.IsDir() {
				addAncestors(targetPath, 3)
				continue
			}
			addAncestors(filepath.Dir(targetPath), 3)
		}
	}

	slog.Debug("Extracted paths from dependency manifest",
		"path", manifestPath,
		"includePaths", len(s.ExtraIncludePaths),
		"frameworkPaths", len(s.ExtraFrameworkPaths),
		"moduleMapFiles", len(s.ExtraModuleMapFiles),
	)
	return nil
}

// HasPreviousBuild checks whether a .app bundle exists in the build products
// directory, indicating that a previous build can be reused.
func HasPreviousBuild(s *Settings, dirs ProjectDirs) bool {
	appName := s.ModuleName + ".app"
	primaryPath := filepath.Join(s.BuiltProductsDir, appName)
	if _, err := os.Stat(primaryPath); err == nil {
		return true
	}
	pattern := filepath.Join(dirs.Build, "Build", "Products", "*", appName)
	matches, _ := filepath.Glob(pattern)
	return len(matches) > 0
}
