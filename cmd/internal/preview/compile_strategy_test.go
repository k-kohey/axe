package preview

import (
	"context"
	"errors"
	"testing"
)

func TestNewCompileStrategy_OneshotDefault(t *testing.T) {
	t.Parallel()
	s := NewCompileStrategy(false, false, false, false)
	if s.Primary != CompileModeMainOnly {
		t.Errorf("Primary = %d, want CompileModeMainOnly", s.Primary)
	}
	if s.Fallback != nil {
		t.Error("Fallback should be nil for oneshot default")
	}
}

func TestNewCompileStrategy_OneshotFullThunk(t *testing.T) {
	t.Parallel()
	s := NewCompileStrategy(false, false, true, false)
	if s.Primary != CompileModeFull {
		t.Errorf("Primary = %d, want CompileModeFull", s.Primary)
	}
	if s.Fallback != nil {
		t.Error("Fallback should be nil for oneshot --full-thunk")
	}
}

func TestNewCompileStrategy_WatchDefault(t *testing.T) {
	t.Parallel()
	s := NewCompileStrategy(true, false, false, false)
	if s.Primary != CompileModeFull {
		t.Errorf("Primary = %d, want CompileModeFull", s.Primary)
	}
	if s.Fallback == nil || *s.Fallback != CompileModeMainOnly {
		t.Error("Fallback should be CompileModeMainOnly for watch default")
	}
}

func TestNewCompileStrategy_ServeDefault(t *testing.T) {
	t.Parallel()
	s := NewCompileStrategy(false, true, false, false)
	if s.Primary != CompileModeFull {
		t.Errorf("Primary = %d, want CompileModeFull", s.Primary)
	}
	if s.Fallback == nil || *s.Fallback != CompileModeMainOnly {
		t.Error("Fallback should be CompileModeMainOnly for serve default")
	}
}

func TestNewCompileStrategy_WatchStrict(t *testing.T) {
	t.Parallel()
	s := NewCompileStrategy(true, false, false, true)
	if s.Primary != CompileModeFull {
		t.Errorf("Primary = %d, want CompileModeFull", s.Primary)
	}
	if s.Fallback != nil {
		t.Error("Fallback should be nil for watch --strict")
	}
}

func TestExecuteCompileStrategy_PrimarySuccess(t *testing.T) {
	t.Parallel()
	s := CompileStrategy{Primary: CompileModeFull}
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(_ context.Context) (string, error) {
			return "/path/to/thunk.dylib", nil
		},
	}
	result, err := ExecuteCompileStrategy(context.Background(), s, compilers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DylibPath != "/path/to/thunk.dylib" {
		t.Errorf("DylibPath = %q, want /path/to/thunk.dylib", result.DylibPath)
	}
	if result.Degraded {
		t.Error("Degraded should be false when primary succeeds")
	}
}

func TestExecuteCompileStrategy_FallbackUsed(t *testing.T) {
	t.Parallel()
	mainOnly := CompileModeMainOnly
	s := CompileStrategy{Primary: CompileModeFull, Fallback: &mainOnly}
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(_ context.Context) (string, error) {
			return "", errors.New("full compile failed")
		},
		CompileModeMainOnly: func(_ context.Context) (string, error) {
			return "/path/to/main_only.dylib", nil
		},
	}
	result, err := ExecuteCompileStrategy(context.Background(), s, compilers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DylibPath != "/path/to/main_only.dylib" {
		t.Errorf("DylibPath = %q, want /path/to/main_only.dylib", result.DylibPath)
	}
	if !result.Degraded {
		t.Error("Degraded should be true when fallback is used")
	}
}

func TestExecuteCompileStrategy_NoFallback(t *testing.T) {
	t.Parallel()
	s := CompileStrategy{Primary: CompileModeFull}
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(_ context.Context) (string, error) {
			return "", errors.New("full compile failed")
		},
	}
	_, err := ExecuteCompileStrategy(context.Background(), s, compilers)
	if err == nil {
		t.Fatal("expected error when primary fails with no fallback")
	}
}

func TestExecuteCompileStrategy_BothFail(t *testing.T) {
	t.Parallel()
	mainOnly := CompileModeMainOnly
	s := CompileStrategy{Primary: CompileModeFull, Fallback: &mainOnly}
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(_ context.Context) (string, error) {
			return "", errors.New("full failed")
		},
		CompileModeMainOnly: func(_ context.Context) (string, error) {
			return "", errors.New("main-only failed")
		},
	}
	_, err := ExecuteCompileStrategy(context.Background(), s, compilers)
	if err == nil {
		t.Fatal("expected error when both compilers fail")
	}
}

func TestExecuteCompileStrategy_SkipsFallbackOnCancelledContext(t *testing.T) {
	t.Parallel()
	mainOnly := CompileModeMainOnly
	s := CompileStrategy{Primary: CompileModeFull, Fallback: &mainOnly}

	fallbackCalled := false
	compilers := map[CompileMode]CompileFunc{
		CompileModeFull: func(_ context.Context) (string, error) {
			return "", errors.New("full failed")
		},
		CompileModeMainOnly: func(_ context.Context) (string, error) {
			fallbackCalled = true
			return "/path/to/main_only.dylib", nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before executing

	_, err := ExecuteCompileStrategy(ctx, s, compilers)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	if fallbackCalled {
		t.Error("fallback should not be called when context is cancelled")
	}
}
