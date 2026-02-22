package preview

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEnsureBuildSettings_LazyInit(t *testing.T) {
	t.Parallel()

	output := `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`
	br := &fakeBuildRunner{fetchOutput: []byte(output)}
	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	dirs := previewDirs{Build: t.TempDir()}

	// First call should fetch build settings.
	bs, err := sm.ensureBuildSettings(context.Background(), dirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bs.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", bs.ModuleName, "TestModule")
	}
}

func TestEnsureBuildSettings_CachesResult(t *testing.T) {
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
	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	dirs := previewDirs{Build: t.TempDir()}

	// Call twice — FetchBuildSettings should only be invoked once.
	bs1, err := sm.ensureBuildSettings(context.Background(), dirs)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	bs2, err := sm.ensureBuildSettings(context.Background(), dirs)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if bs1 != bs2 {
		t.Error("expected same *buildSettings pointer from both calls")
	}

	if callCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", callCount.Load())
	}
}

func TestEnsureBuildSettings_ConcurrentSafety(t *testing.T) {
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
	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	dirs := previewDirs{Build: t.TempDir()}

	// Launch 10 concurrent calls.
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	results := make([]*buildSettings, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			bs, err := sm.ensureBuildSettings(context.Background(), dirs)
			results[idx] = bs
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

func TestEnsureBuildSettings_PropagatesError(t *testing.T) {
	t.Parallel()

	br := &fakeBuildRunner{
		fetchErr: errors.New("xcodebuild not found"),
	}
	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		br, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	dirs := previewDirs{Build: t.TempDir()}

	_, err := sm.ensureBuildSettings(context.Background(), dirs)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- ensureCompilerPathsExtracted tests ---

func TestEnsureCompilerPathsExtracted_OnlyOnce(t *testing.T) {
	t.Parallel()

	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		&fakeBuildRunner{}, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	bs := &buildSettings{ModuleName: "TestModule", BuiltProductsDir: "/tmp/none"}
	dirs := previewDirs{Build: t.TempDir()}

	// Call twice.
	sm.ensureCompilerPathsExtracted(context.Background(), bs, dirs)
	sm.ensureCompilerPathsExtracted(context.Background(), bs, dirs)

	// Verify the flag was set.
	sm.bsMu.RLock()
	extracted := sm.bsExtracted
	sm.bsMu.RUnlock()

	if !extracted {
		t.Error("expected bsExtracted to be true")
	}
}

func TestEnsureCompilerPathsExtracted_ConcurrentSafety(t *testing.T) {
	t.Parallel()

	sm := NewStreamManager(newFakeDevicePool(), NewEventWriter(&syncBuffer{}),
		ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}, "",
		&fakeBuildRunner{}, &fakeToolchainRunner{sdkPathResult: "/fake/sdk"}, &fakeAppRunner{}, &fakeFileCopier{}, &errSourceLister{})

	bs := &buildSettings{ModuleName: "TestModule", BuiltProductsDir: "/tmp/none"}
	dirs := previewDirs{Build: t.TempDir()}

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			sm.ensureCompilerPathsExtracted(context.Background(), bs, dirs)
		}()
	}
	wg.Wait()

	// Should not panic or race.
	sm.bsMu.RLock()
	extracted := sm.bsExtracted
	sm.bsMu.RUnlock()

	if !extracted {
		t.Error("expected bsExtracted to be true after concurrent calls")
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
