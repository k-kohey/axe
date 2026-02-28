package build

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// countingRunner tracks how many times FetchBuildSettings and Build are called.
type countingRunner struct {
	fetchOutput []byte
	fetchErr    error
	buildOutput []byte
	buildErr    error
	fetchCount  atomic.Int32
	buildCount  atomic.Int32
}

func (c *countingRunner) FetchBuildSettings(_ context.Context, _ []string) ([]byte, error) {
	c.fetchCount.Add(1)
	return c.fetchOutput, c.fetchErr
}

func (c *countingRunner) Build(_ context.Context, _ []string) ([]byte, error) {
	c.buildCount.Add(1)
	return c.buildOutput, c.buildErr
}

const validOutput = `    PRODUCT_MODULE_NAME = TestModule
    PRODUCT_BUNDLE_IDENTIFIER = com.example.TestModule
    IPHONEOS_DEPLOYMENT_TARGET = 17.0
`

func newTestPreparer(r Runner) *Preparer {
	pc := ProjectConfig{Project: "/tmp/TestProject.xcodeproj", Scheme: "TestScheme"}
	dirs := ProjectDirs{Build: "/tmp/build"}
	return NewPreparer(pc, dirs, false, r)
}

func TestPreparer_FirstCall(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	result, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", result.Settings.ModuleName, "TestModule")
	}
	if r.fetchCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", r.fetchCount.Load())
	}
	if r.buildCount.Load() != 1 {
		t.Errorf("Build called %d times, want 1", r.buildCount.Load())
	}
}

func TestPreparer_CachesResult(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	_, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	_, err = p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if r.fetchCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", r.fetchCount.Load())
	}
}

func TestPreparer_ConcurrentSafety(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = p.Prepare(context.Background())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	if r.fetchCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", r.fetchCount.Load())
	}
}

func TestPreparer_InFlightWait(t *testing.T) {
	t.Parallel()

	// Use a channel to control when the build completes, ensuring
	// the second caller sees the in-flight state.
	gate := make(chan struct{})
	entered := make(chan struct{})
	r := &gatedRunner{
		output:  []byte(validOutput),
		gate:    gate,
		entered: entered,
	}
	p := newTestPreparer(r)

	var wg sync.WaitGroup
	wg.Add(2)

	var r1, r2 *Result
	var e1, e2 error

	go func() {
		defer wg.Done()
		r1, e1 = p.Prepare(context.Background())
	}()

	// Wait until the first goroutine is inside FetchBuildSettings,
	// guaranteeing the second goroutine will see the in-flight state.
	<-entered

	go func() {
		defer wg.Done()
		r2, e2 = p.Prepare(context.Background())
	}()

	// Unblock the build.
	close(gate)
	wg.Wait()

	if e1 != nil {
		t.Fatalf("goroutine 1: %v", e1)
	}
	if e2 != nil {
		t.Fatalf("goroutine 2: %v", e2)
	}

	// Both should get a result with the same module name.
	if r1.Settings.ModuleName != r2.Settings.ModuleName {
		t.Errorf("module names differ: %q vs %q", r1.Settings.ModuleName, r2.Settings.ModuleName)
	}

	// But they must be different pointers (cloned).
	if r1.Settings == r2.Settings {
		t.Error("expected different Settings pointers from concurrent callers")
	}

	if r.fetchCount.Load() != 1 {
		t.Errorf("FetchBuildSettings called %d times, want 1", r.fetchCount.Load())
	}
}

func TestPreparer_ErrorNotCached(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte("error"),
		fetchErr:    errors.New("xcodebuild not found"),
	}
	p := newTestPreparer(r)

	_, err := p.Prepare(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Fix the runner and retry.
	r.fetchOutput = []byte(validOutput)
	r.fetchErr = nil
	r.buildOutput = []byte("BUILD SUCCEEDED")

	result, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q", result.Settings.ModuleName, "TestModule")
	}

	// FetchBuildSettings should have been called twice (first error + retry).
	if r.fetchCount.Load() != 2 {
		t.Errorf("FetchBuildSettings called %d times, want 2", r.fetchCount.Load())
	}
}

func TestPreparer_Invalidate(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	_, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	p.Invalidate()

	_, err = p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("after invalidate: %v", err)
	}

	if r.fetchCount.Load() != 2 {
		t.Errorf("FetchBuildSettings called %d times, want 2", r.fetchCount.Load())
	}
}

func TestPreparer_BuiltSemantics(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	first, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// First call triggers a real build (reuse=false), so Built must be true.
	if !first.Built {
		t.Error("first call Built = false, want true")
	}

	second, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Cache-hit: no new build occurred, so Built must be false.
	if second.Built {
		t.Error("cached Built = true, want false")
	}
}

func TestPreparer_CloneIsolation(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	r1, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Mutate the returned result.
	r1.Settings.ModuleName = "Mutated"
	r1.Settings.ExtraIncludePaths = append(r1.Settings.ExtraIncludePaths, "/mutated")

	// Second call should be unaffected.
	r2, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r2.Settings.ModuleName != "TestModule" {
		t.Errorf("ModuleName = %q, want %q (mutation leaked)", r2.Settings.ModuleName, "TestModule")
	}
	for _, p := range r2.Settings.ExtraIncludePaths {
		if p == "/mutated" {
			t.Error("ExtraIncludePaths mutation leaked to cache")
		}
	}
}

func TestPreparer_Cached(t *testing.T) {
	t.Parallel()

	r := &countingRunner{
		fetchOutput: []byte(validOutput),
		buildOutput: []byte("BUILD SUCCEEDED"),
	}
	p := newTestPreparer(r)

	// Before any Prepare, Cached should return nil.
	if c := p.Cached(); c != nil {
		t.Fatalf("Cached() before Prepare = %v, want nil", c)
	}

	_, err := p.Prepare(context.Background())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// After Prepare, Cached should return a non-nil result.
	c := p.Cached()
	if c == nil {
		t.Fatal("Cached() after Prepare = nil, want non-nil")
	}
	if c.Settings.ModuleName != "TestModule" {
		t.Errorf("Cached ModuleName = %q, want %q", c.Settings.ModuleName, "TestModule")
	}
}

func TestPreparer_InFlightRespectsContextCancel(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	entered := make(chan struct{})
	r := &gatedRunner{
		output:  []byte(validOutput),
		gate:    gate,
		entered: entered,
	}
	p := newTestPreparer(r)

	// Start a Prepare that will block on the gate.
	go func() {
		_, _ = p.Prepare(context.Background())
	}()

	// Wait until the first goroutine has entered FetchBuildSettings
	// (and therefore holds the in-flight slot).
	<-entered

	// Start a second Prepare with an already-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Prepare(ctx)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}

	// Unblock the first goroutine so the test can exit cleanly.
	close(gate)
}

// gatedRunner blocks FetchBuildSettings until the gate channel is closed.
// entered is closed when FetchBuildSettings is first called (for synchronization).
type gatedRunner struct {
	output     []byte
	gate       chan struct{}
	entered    chan struct{} // closed on first FetchBuildSettings call
	fetchCount atomic.Int32
}

func (g *gatedRunner) FetchBuildSettings(_ context.Context, _ []string) ([]byte, error) {
	if g.entered != nil {
		select {
		case <-g.entered:
			// already closed
		default:
			close(g.entered)
		}
	}
	<-g.gate
	g.fetchCount.Add(1)
	return g.output, nil
}

func (g *gatedRunner) Build(_ context.Context, _ []string) ([]byte, error) {
	return []byte("BUILD SUCCEEDED"), nil
}
