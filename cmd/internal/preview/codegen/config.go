package codegen

// CompileConfig holds the subset of build settings required for thunk compilation.
// This decouples codegen from the parent package's buildSettings type.
type CompileConfig struct {
	ModuleName       string
	BuiltProductsDir string
	DeploymentTarget string
	SwiftVersion     string

	ExtraIncludePaths   []string // additional -I paths (SPM C module headers)
	ExtraFrameworkPaths []string // additional -F paths (e.g. PackageFrameworks)
	ExtraModuleMapFiles []string // -fmodule-map-file= paths (generated ObjC module maps)
}
