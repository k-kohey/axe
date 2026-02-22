package preview

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
	linkDylibErr    error
	compileCErr     error
	codesignErr     error

	// Captured args for assertions.
	compileSwiftArgs []string
	linkDylibArgs    []string
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

func (f *fakeToolchainRunner) LinkDylib(_ context.Context, args []string) ([]byte, error) {
	f.callOrder = append(f.callOrder, "LinkDylib")
	f.linkDylibArgs = args
	return nil, f.linkDylibErr
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

// --- compileThunk tests ---

func TestCompileThunk_FullFlow(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tc := &fakeToolchainRunner{sdkPathResult: "/sdk/iphonesimulator"}
	bs := &buildSettings{
		ModuleName:       "TestModule",
		BuiltProductsDir: filepath.Join(tmpDir, "products"),
		DeploymentTarget: "17.0",
		SwiftVersion:     "5.0",
	}
	dirs := previewDirs{
		Build: tmpDir,
		Thunk: filepath.Join(tmpDir, "thunk"),
	}

	dylibPath, err := compileThunk(
		context.Background(),
		filepath.Join(tmpDir, "thunk.swift"),
		bs, dirs, 0, "HogeView.swift",
		tc,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the full call order: SDKPath -> CompileSwift -> LinkDylib -> Codesign.
	wantOrder := []string{"SDKPath", "CompileSwift", "LinkDylib", "Codesign"}
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
	bs := &buildSettings{
		ModuleName:          "TestModule",
		BuiltProductsDir:    filepath.Join(tmpDir, "products"),
		DeploymentTarget:    "17.0",
		SwiftVersion:        "6.0",
		ExtraIncludePaths:   []string{"/extra/include"},
		ExtraFrameworkPaths: []string{"/extra/framework"},
		ExtraModuleMapFiles: []string{"/extra/module.modulemap"},
	}
	dirs := previewDirs{
		Build: tmpDir,
		Thunk: filepath.Join(tmpDir, "thunk"),
	}

	_, err := compileThunk(
		context.Background(),
		filepath.Join(tmpDir, "thunk.swift"),
		bs, dirs, 3, "HogeView.swift",
		tc,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compileArgs := strings.Join(tc.compileSwiftArgs, " ")

	// Verify SDK is passed.
	if !strings.Contains(compileArgs, "-sdk /sdk/iphonesimulator") {
		t.Error("compile args should contain -sdk")
	}

	// Verify target includes deployment target.
	if !strings.Contains(compileArgs, "-target arm64-apple-ios17.0-simulator") {
		t.Errorf("compile args should contain target, got: %s", compileArgs)
	}

	// Verify module name uses replacement format.
	if !strings.Contains(compileArgs, "-module-name TestModule_PreviewReplacement_HogeView_3") {
		t.Errorf("compile args should contain replacement module name, got: %s", compileArgs)
	}

	// Verify extra paths are passed.
	if !strings.Contains(compileArgs, "-I /extra/include") {
		t.Error("compile args should contain extra include path")
	}
	if !strings.Contains(compileArgs, "-F /extra/framework") {
		t.Error("compile args should contain extra framework path")
	}
	if !strings.Contains(compileArgs, "-fmodule-map-file=/extra/module.modulemap") {
		t.Error("compile args should contain extra module map file")
	}

	// Verify swift-version is trimmed (6.0 -> 6).
	if !strings.Contains(compileArgs, "-swift-version 6") {
		t.Errorf("compile args should contain -swift-version 6, got: %s", compileArgs)
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
			bs := &buildSettings{
				ModuleName:       "TestModule",
				BuiltProductsDir: filepath.Join(tmpDir, "products"),
				DeploymentTarget: "17.0",
				SwiftVersion:     tt.swiftVer,
			}
			dirs := previewDirs{
				Build: tmpDir,
				Thunk: filepath.Join(tmpDir, "thunk"),
			}

			_, err := compileThunk(
				context.Background(),
				filepath.Join(tmpDir, "thunk.swift"),
				bs, dirs, 0, "HogeView.swift",
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
			wantError: "compiling thunk.swift -> .o",
			wantCalls: 2,
		},
		{
			name: "LinkDylib failure",
			tc: &fakeToolchainRunner{
				sdkPathResult: "/sdk/iphonesimulator",
				linkDylibErr:  errors.New("linker error"),
			},
			wantError: "linking thunk.o -> .dylib",
			wantCalls: 3,
		},
		{
			name: "Codesign failure",
			tc: &fakeToolchainRunner{
				sdkPathResult: "/sdk/iphonesimulator",
				codesignErr:   errors.New("signing error"),
			},
			wantError: "codesigning dylib",
			wantCalls: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			bs := &buildSettings{
				ModuleName:       "TestModule",
				BuiltProductsDir: filepath.Join(tmpDir, "products"),
				DeploymentTarget: "17.0",
			}
			dirs := previewDirs{
				Build: tmpDir,
				Thunk: filepath.Join(tmpDir, "thunk"),
			}

			_, err := compileThunk(
				context.Background(),
				filepath.Join(tmpDir, "thunk.swift"),
				bs, dirs, 0, "HogeView.swift",
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

func TestCompileThunk_LinkArgs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tc := &fakeToolchainRunner{sdkPathResult: "/sdk/iphonesimulator"}
	bs := &buildSettings{
		ModuleName:       "TestModule",
		BuiltProductsDir: filepath.Join(tmpDir, "products"),
		DeploymentTarget: "17.0",
	}
	dirs := previewDirs{
		Build: tmpDir,
		Thunk: filepath.Join(tmpDir, "thunk"),
	}

	_, err := compileThunk(
		context.Background(),
		filepath.Join(tmpDir, "thunk.swift"),
		bs, dirs, 2, "HogeView.swift",
		tc,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	linkArgs := strings.Join(tc.linkDylibArgs, " ")

	// Verify emit-library flag.
	if !strings.Contains(linkArgs, "-emit-library") {
		t.Error("link args should contain -emit-library")
	}

	// Verify flat_namespace for dynamic replacement.
	if !strings.Contains(linkArgs, "-flat_namespace") {
		t.Error("link args should contain -flat_namespace")
	}

	// Verify output path uses counter.
	if !strings.Contains(linkArgs, "thunk_2.dylib") {
		t.Errorf("link args should reference thunk_2.dylib, got: %s", linkArgs)
	}

	// Verify module-name in link args.
	if !strings.Contains(linkArgs, "-module-name TestModule_PreviewReplacement_HogeView_2") {
		t.Errorf("link args should contain replacement module name, got: %s", linkArgs)
	}
}
