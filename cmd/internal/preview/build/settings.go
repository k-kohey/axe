package build

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
}
