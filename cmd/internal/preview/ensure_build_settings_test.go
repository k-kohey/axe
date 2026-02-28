package preview

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/k-kohey/axe/internal/preview/build"
	"github.com/k-kohey/axe/internal/preview/protocol"
)

func TestEnsurePrepared_LazyInit(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	sm := NewStreamManager(newFakeDevicePool(), protocol.NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false)

	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: t.TempDir()}}

	// First call should run the full pipeline.
	result, err := sm.ensurePrepared(context.Background(), dirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", result.Settings.ModuleName, "TestModule")
	}
}

func TestEnsurePrepared_CachesResult(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	var callCount atomic.Int32
	br := &countingBuildRunner{
		output:    []byte(output),
		callCount: &callCount,
	}
	sm := NewStreamManager(newFakeDevicePool(), protocol.NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false)

	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: t.TempDir()}}

	// Call twice — Prepare should only be invoked once.
	r1, err := sm.ensurePrepared(context.Background(), dirs)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	r2, err := sm.ensurePrepared(context.Background(), dirs)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if r1.Settings != r2.Settings {
		t.Error("expected same *build.Settings pointer from both calls")
	}

	if callCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", callCount.Load())
	}
}

func TestEnsurePrepared_ConcurrentSafety(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	var callCount atomic.Int32
	br := &countingBuildRunner{
		output:    []byte(output),
		callCount: &callCount,
	}
	sm := NewStreamManager(newFakeDevicePool(), protocol.NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false)

	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: t.TempDir()}}

	// Launch 10 concurrent calls.
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	results := make([]*build.Result, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			r, err := sm.ensurePrepared(context.Background(), dirs)
			results[idx] = r
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	// All results should point to the same cached instance.
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Errorf("goroutine %d returned different pointer", i)
		}
	}

	if callCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", callCount.Load())
	}
}

func TestEnsurePrepared_PropagatesError(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{
		fetchErr: errors.New("xcodebuild not found"),
	}
	sm := NewStreamManager(newFakeDevicePool(), protocol.NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{}, false)

	dirs := previewDirs{ProjectDirs: build.ProjectDirs{Build: t.TempDir()}}

	_, err := sm.ensurePrepared(context.Background(), dirs)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// countingBuildRunner tracks how many times FetchBuildSettings is called.
type countingBuildRunner struct {
	output    []byte
	callCount *atomic.Int32
}

func (c *countingBuildRunner) FetchBuildSettings(_ context.Context, _ []string) ([]byte, error) {
	c.callCount.Add(1)
	return c.output, nil
}

func (c *countingBuildRunner) Build(_ context.Context, _ []string) ([]byte, error) {
	return nil, nil
}
