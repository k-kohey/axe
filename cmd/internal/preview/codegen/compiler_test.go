package codegen

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// --- Fake ToolchainRunner ---

type fakeToolchainRunner struct {
	sdkPathResult string
	sdkPathErr    error

	compileSwiftErr error
	compileCErr     error
	codesignErr     error

	// Captured args for assertions.
	compileSwiftArgs []string
	compileCArgs     []string
	codesignPath     string

	// Call order tracking.
	callOrder []string
}

func (f *fakeToolchainRunner) SDKPath(_ context.Context, sdk string) (string, error) {
	f.callOrder = append(f.callOrder, "SDKPath")
	if f.sdkPathErr != nil {
		return "", f.sdkPathErr
	}
	return f.sdkPathResult, nil
}

func (f *fakeToolchainRunner) CompileSwift(_ context.Context, args []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileSwift")
	f.compileSwiftArgs = args
	return nil, f.compileSwiftErr
}

func (f *fakeToolchainRunner) CompileC(_ context.Context, args []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "CompileC")
	f.compileCArgs = args
	return nil, f.compileCErr
}

func (f *fakeToolchainRunner) Codesign(_ context.Context, path string) error {
	f.callOrder = append(f.callOrder, "Codesign")
	f.codesignPath = path
	return f.codesignErr
}

// --- CompileThunk tests ---

func TestCompileThunk_FullFlow(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tc := &fakeToolchainRunner{sdkPathResult: "/sdk/iphonesimulator"}
	cfg := CompileConfig{
		ModuleName:       "TestModule",
		BuiltProductsDir: filepath.Join(tmpDir, "products"),
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.0",
	}
	thunkDir := filepath.Join(tmpDir, "thunk")
	buildDir := tmpDir

	thunkPaths := []string{
		filepath.Join(tmpDir, "thunk_0_HogeView.swift"),
		filepath.Join(tmpDir, "thunk_0__main.swift"),
	}

	dylibPath, err := CompileThunk(
		context.Background(),
		thunkPaths,
		cfg, thunkDir, buildDir, 0, "HogeView.swift",
		tc,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the call order: SDKPath -> CompileSwift -> Codesign.
	wantOrder := []string{"SDKPath", "CompileSwift", "Codesign"}
	if len(tc.callOrder) != len(wantOrder) {
		t.Fatalf("callOrder = %v, want %v", tc.callOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if tc.callOrder[i] != want {
			t.Errorf("callOrder[%d] = %q, want %q", i, tc.callOrder[i], want)
		}
	}

	// Verify dylib path format.
	if !strings.HasSuffix(dylibPath, "thunk_0.dylib") {
		t.Errorf("dylibPath = %q, want suffix %q", dylibPath, "thunk_0.dylib")
	}

	// Verify codesign was called on the dylib.
	if tc.codesignPath != dylibPath {
		t.Errorf("codesignPath = %q, want %q", tc.codesignPath, dylibPath)
	}
}

func TestCompileThunk_CompileSwiftArgs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tc := &fakeToolchainRunner{sdkPathResult: "/sdk/iphonesimulator"}
	cfg := CompileConfig{
		ModuleName:          "TestModule",
		BuiltProductsDir:    filepath.Join(tmpDir, "products"),
		DeploymentTarget:    "17.0",
		SwiftVersion:        "6.0",
		ExtraIncludePaths:   []string{"/extra/include"},
		ExtraFrameworkPaths: []string{"/extra/framework"},
		ExtraModuleMapFiles: []string{"/extra/module.modulemap"},
	}
	thunkDir := filepath.Join(tmpDir, "thunk")
	buildDir := tmpDir

	thunkPaths := []string{
		filepath.Join(tmpDir, "thunk_0_HogeView.swift"),
		filepath.Join(tmpDir, "thunk_0__main.swift"),
	}

	_, err := CompileThunk(
		context.Background(),
		thunkPaths,
		cfg, thunkDir, buildDir, 3, "HogeView.swift",
		tc,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileArgs := strings.Join(tc.compileSwiftArgs, " ")

	// Verify unified compile+link args.
	if !strings.Contains(compileArgs, "-emit-library") {
		t.Error("compile args should contain -emit-library")
	}
	if !strings.Contains(compileArgs, "-sdk /sdk/iphonesimulator") {
		t.Error("compile args should contain -sdk")
	}
	if !strings.Contains(compileArgs, "-target arm64-apple-ios17.0-simulator") {
		t.Errorf("compile args should contain target, got: %s", compileArgs)
	}
	if !strings.Contains(compileArgs, "-module-name TestModule_PreviewReplacement_HogeView_3") {
		t.Errorf("compile args should contain replacement module name, got: %s", compileArgs)
	}
	if !strings.Contains(compileArgs, "-I /extra/include") {
		t.Error("compile args should contain extra include path")
	}
	if !strings.Contains(compileArgs, "-F /extra/framework") {
		t.Error("compile args should contain extra framework path")
	}
	if !strings.Contains(compileArgs, "-fmodule-map-file=/extra/module.modulemap") {
		t.Error("compile args should contain extra module map file")
	}
	if !strings.Contains(compileArgs, "-swift-version 6") {
		t.Errorf("compile args should contain -swift-version 6, got: %s", compileArgs)
	}

	// Verify -enable-private-imports for per-file @_private scoping.
	if !strings.Contains(compileArgs, "-enable-private-imports") {
		t.Error("compile args should contain -enable-private-imports")
	}
	if strings.Contains(compileArgs, "-disable-access-control") {
		t.Error("compile args should NOT contain -disable-access-control")
	}

	// Verify linker flags are included in the unified invocation.
	if !strings.Contains(compileArgs, "-flat_namespace") {
		t.Error("compile args should contain -flat_namespace")
	}
	if !strings.Contains(compileArgs, "-L "+cfg.BuiltProductsDir) {
		t.Error("compile args should contain -L for BuiltProductsDir")
	}

	// Verify both thunk paths are present.
	if !strings.Contains(compileArgs, "thunk_0_HogeView.swift") {
		t.Error("compile args should contain per-file thunk path")
	}
	if !strings.Contains(compileArgs, "thunk_0__main.swift") {
		t.Error("compile args should contain main thunk path")
	}
}

func TestCompileThunk_SwiftVersionTrimming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		swiftVer    string
		wantVersion string
	}{
		{"6.0 trims to 6", "6.0", "6"},
		{"5.0 trims to 5", "5.0", "5"},
		{"4.0 stays as 4.0", "4.0", "4.0"},
		{"4.2 stays as 4.2", "4.2", "4.2"},
		{"empty omits flag", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			tc := &fakeToolchainRunner{sdkPathResult: "/sdk/iphonesimulator"}
			cfg := CompileConfig{
				ModuleName:       "TestModule",
				BuiltProductsDir: filepath.Join(tmpDir, "products"),
				DeploymentTarget: "17.0",
				SwiftVersion:     tt.swiftVer,
			}
			thunkDir := filepath.Join(tmpDir, "thunk")
			buildDir := tmpDir

			thunkPaths := []string{filepath.Join(tmpDir, "thunk.swift")}

			_, err := CompileThunk(
				context.Background(),
				thunkPaths,
				cfg, thunkDir, buildDir, 0, "HogeView.swift",
				tc,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			compileArgs := strings.Join(tc.compileSwiftArgs, " ")
			if tt.wantVersion == "" {
				if strings.Contains(compileArgs, "-swift-version") {
					t.Error("compile args should not contain -swift-version for empty version")
				}
			} else {
				want := "-swift-version " + tt.wantVersion
				if !strings.Contains(compileArgs, want) {
					t.Errorf("compile args should contain %q, got: %s", want, compileArgs)
				}
			}
		})
	}
}

func TestCompileThunk_ErrorPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tc        *fakeToolchainRunner
		wantError string
		wantCalls int // expected number of calls before failure
	}{
		{
			name: "SDKPath failure",
			tc: &fakeToolchainRunner{
				sdkPathErr: errors.New("xcrun not found"),
			},
			wantError: "getting simulator SDK path",
			wantCalls: 1,
		},
		{
			name: "CompileSwift failure",
			tc: &fakeToolchainRunner{
				sdkPathResult:   "/sdk/iphonesimulator",
				compileSwiftErr: errors.New("compilation error"),
			},
			wantError: "compiling thunk",
			wantCalls: 2,
		},
		{
			name: "Codesign failure",
			tc: &fakeToolchainRunner{
				sdkPathResult: "/sdk/iphonesimulator",
				codesignErr:   errors.New("signing error"),
			},
			wantError: "codesigning dylib",
			wantCalls: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			cfg := CompileConfig{
				ModuleName:       "TestModule",
				BuiltProductsDir: filepath.Join(tmpDir, "products"),
				DeploymentTarget: "17.0",
			}
			thunkDir := filepath.Join(tmpDir, "thunk")
			buildDir := tmpDir

			thunkPaths := []string{filepath.Join(tmpDir, "thunk.swift")}

			_, err := CompileThunk(
				context.Background(),
				thunkPaths,
				cfg, thunkDir, buildDir, 0, "HogeView.swift",
				tt.tc,
			)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantError)
			}
			if len(tt.tc.callOrder) != tt.wantCalls {
				t.Errorf("callOrder length = %d, want %d (calls: %v)",
					len(tt.tc.callOrder), tt.wantCalls, tt.tc.callOrder)
			}
		})
	}
}
