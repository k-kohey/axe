package build

import (
	"maps"
	"path/filepath"
)

// Settings holds values extracted from xcodebuild -showBuildSettings,
// plus additional compiler paths extracted from the swiftc response file.
type Settings struct {
	ModuleName       string
	BundleID         string // axe-prefixed bundle ID (used for terminate/launch)
	OriginalBundleID string // original bundle ID from xcodebuild
	BuiltProductsDir string
	DeploymentTarget string
	SwiftVersion     string

	// Fields below are populated by ExtractCompilerPaths after build.
	ExtraIncludePaths   []string // additional -I paths (SPM C module headers)
	ExtraFrameworkPaths []string // additional -F paths (e.g. PackageFrameworks)
	ExtraModuleMapFiles []string // -fmodule-map-file= paths (generated ObjC module maps)

	// SourceModules maps absolute source paths to their module, when known from build info.
	// It takes precedence over index store lookups (which depend on how paths were recorded).
	SourceModules map[string]string
}

// ModuleForSource returns the module of sourceFile recorded in SourceModules, or "".
func (s *Settings) ModuleForSource(sourceFile string) string {
	if s == nil || s.SourceModules == nil {
		return ""
	}
	if m, ok := s.SourceModules[sourceFile]; ok {
		return m
	}
	if resolved, err := filepath.EvalSymlinks(sourceFile); err == nil {
		return s.SourceModules[resolved]
	}
	return ""
}

// Clone returns a deep copy of the Settings. Use this when multiple goroutines
// need independent copies (e.g. per-stream settings in multi-stream mode)
// to avoid data races on the mutable slice fields.
func (s *Settings) Clone() *Settings {
	c := *s
	c.ExtraIncludePaths = append([]string(nil), s.ExtraIncludePaths...)
	c.ExtraFrameworkPaths = append([]string(nil), s.ExtraFrameworkPaths...)
	c.ExtraModuleMapFiles = append([]string(nil), s.ExtraModuleMapFiles...)
	if s.SourceModules != nil {
		c.SourceModules = make(map[string]string, len(s.SourceModules))
		maps.Copy(c.SourceModules, s.SourceModules)
	}
	return &c
}
