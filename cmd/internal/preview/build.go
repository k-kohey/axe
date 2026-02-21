package preview

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func fetchBuildSettings(pc ProjectConfig, dirs previewDirs) (*buildSettings, error) {
	args := append(
		[]string{"xcodebuild", "-showBuildSettings"},
		pc.xcodebuildArgs()...,
	)
	args = append(args, "-destination", "generic/platform=iOS Simulator")

	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
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
			if strings.HasPrefix(line, prefix) {
				keys[k] = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		}
	}

	config := pc.Configuration
	if config == "" {
		config = "Debug"
	}
	builtProductsDir := filepath.Join(dirs.Build, "Build", "Products", config+"-iphonesimulator")

	bs := &buildSettings{
		ModuleName:       keys["PRODUCT_MODULE_NAME"],
		BundleID:         "axe." + keys["PRODUCT_BUNDLE_IDENTIFIER"],
		OriginalBundleID: keys["PRODUCT_BUNDLE_IDENTIFIER"],
		BuiltProductsDir: builtProductsDir,
		DeploymentTarget: keys["IPHONEOS_DEPLOYMENT_TARGET"],
		SwiftVersion:     keys["SWIFT_VERSION"],
	}

	if bs.ModuleName == "" {
		return nil, fmt.Errorf("PRODUCT_MODULE_NAME not found in build settings")
	}
	if bs.OriginalBundleID == "" {
		return nil, fmt.Errorf("PRODUCT_BUNDLE_IDENTIFIER not found in build settings")
	}
	if bs.DeploymentTarget == "" {
		return nil, fmt.Errorf("IPHONEOS_DEPLOYMENT_TARGET not found in build settings")
	}

	slog.Debug("Build settings",
		"module", bs.ModuleName,
		"bundle", bs.BundleID,
		"products", bs.BuiltProductsDir,
		"target", bs.DeploymentTarget,
		"swiftVersion", bs.SwiftVersion,
	)
	return bs, nil
}

func buildProject(pc ProjectConfig, dirs previewDirs) error {
	args := append(
		[]string{"xcodebuild", "build"},
		pc.xcodebuildArgs()...,
	)
	args = append(args,
		"-destination", "generic/platform=iOS Simulator",
		"-derivedDataPath", dirs.Build,
		"OTHER_SWIFT_FLAGS=-Xfrontend -enable-implicit-dynamic -Xfrontend -enable-private-imports",
	)

	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xcodebuild build failed: %w\n%s", err, out)
	}

	return nil
}

// extractCompilerPaths reads the swiftc response file (.resp) generated
// during the xcodebuild build and extracts -I, -F, and -fmodule-map-file=
// flags. These are required so that the thunk compilation can resolve
// transitive SPM dependencies (C module headers, framework bundles, and
// generated ObjC module maps) that xcodebuild manages internally.
func extractCompilerPaths(bs *buildSettings, dirs previewDirs) {
	// Response files live under:
	//   <dirs.Build>/Build/Intermediates.noindex/
	//     <project>.build/<config>-iphonesimulator/<module>.build/
	//     Objects-normal/arm64/arguments-<hash>.resp
	pattern := filepath.Join(
		dirs.Build, "Build", "Intermediates.noindex",
		"*", "*", bs.ModuleName+".build", "Objects-normal", "arm64", "arguments-*.resp",
	)
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		slog.Debug("No swiftc response file found", "pattern", pattern)
		return
	}

	// Read the first matching resp file.
	data, err := os.ReadFile(matches[0])
	if err != nil {
		slog.Warn("Failed to read swiftc response file", "path", matches[0], "err", err)
		return
	}

	seenI := map[string]bool{bs.BuiltProductsDir: true}
	seenF := map[string]bool{bs.BuiltProductsDir: true}

	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// -fmodule-map-file=<path> (single line)
		if strings.HasPrefix(line, "-fmodule-map-file=") {
			p := strings.TrimPrefix(line, "-fmodule-map-file=")
			if p != "" {
				bs.ExtraModuleMapFiles = append(bs.ExtraModuleMapFiles, p)
			}
			continue
		}

		// -I<path> (combined) or -I\n<path> (split across two lines)
		if strings.HasPrefix(line, "-I") {
			p := strings.TrimPrefix(line, "-I")
			if p == "" && i+1 < len(lines) {
				i++
				p = lines[i]
			}
			if strings.HasSuffix(p, ".hmap") || p == "" || seenI[p] {
				continue
			}
			seenI[p] = true
			bs.ExtraIncludePaths = append(bs.ExtraIncludePaths, p)
			continue
		}

		// -F<path> (combined) or -F\n<path> (split across two lines)
		if strings.HasPrefix(line, "-F") {
			p := strings.TrimPrefix(line, "-F")
			if p == "" && i+1 < len(lines) {
				i++
				p = lines[i]
			}
			if p == "" || seenF[p] {
				continue
			}
			seenF[p] = true
			bs.ExtraFrameworkPaths = append(bs.ExtraFrameworkPaths, p)
			continue
		}
	}
	slog.Debug("Extracted paths from resp file",
		"includePaths", len(bs.ExtraIncludePaths),
		"frameworkPaths", len(bs.ExtraFrameworkPaths),
		"moduleMapFiles", len(bs.ExtraModuleMapFiles),
	)
}
