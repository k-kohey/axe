package preview

import (
	"context"
	"fmt"
	"log/slog"
)

// CompileMode represents a thunk compilation strategy.
type CompileMode int

const (
	// CompileModeFull uses per-file dynamic replacement with full dependency analysis.
	CompileModeFull CompileMode = iota
	// CompileModeMainOnly generates only the main thunk (no per-file replacements).
	CompileModeMainOnly
)

// CompileStrategy defines which compilation mode to try first and an optional fallback.
type CompileStrategy struct {
	Primary  CompileMode
	Fallback *CompileMode // nil means no fallback
}

// CompileResult holds the output of ExecuteCompileStrategy.
type CompileResult struct {
	DylibPath string
	Degraded  bool // true when the fallback was used
}

// CompileFunc is a function that performs thunk compilation and returns the dylib path.
type CompileFunc func(ctx context.Context) (string, error)

// NewCompileStrategy returns the compile strategy for the given mode flags.
//
// Strategy table:
//
//	| Mode              | Primary   | Fallback   |
//	|-------------------|-----------|------------|
//	| oneshot default   | MainOnly  | nil        |
//	| oneshot --full    | Full      | nil        |
//	| watch/serve       | Full      | &MainOnly  |
//	| watch/serve strict| Full      | nil        |
func NewCompileStrategy(watch, serve, fullThunk, strict bool) CompileStrategy {
	if watch || serve {
		s := CompileStrategy{Primary: CompileModeFull}
		if !strict {
			mainOnly := CompileModeMainOnly
			s.Fallback = &mainOnly
		}
		return s
	}
	if fullThunk {
		return CompileStrategy{Primary: CompileModeFull}
	}
	return CompileStrategy{Primary: CompileModeMainOnly}
}

// ExecuteCompileStrategy runs the primary compiler; if it fails and a fallback
// is configured, runs the fallback. Returns the compile result or the first
// error if no fallback is available.
func ExecuteCompileStrategy(ctx context.Context, strategy CompileStrategy, compilers map[CompileMode]CompileFunc) (*CompileResult, error) {
	primary, ok := compilers[strategy.Primary]
	if !ok {
		return nil, fmt.Errorf("no compiler registered for primary mode %d", strategy.Primary)
	}

	dylibPath, err := primary(ctx)
	if err == nil {
		return &CompileResult{DylibPath: dylibPath, Degraded: false}, nil
	}

	if strategy.Fallback == nil {
		return nil, err
	}

	// If the context was cancelled (e.g. Ctrl+C), don't waste time on fallback.
	if ctx.Err() != nil {
		return nil, err
	}

	slog.Warn("Primary compile failed, falling back to degraded mode", "primaryErr", err)

	fallback, ok := compilers[*strategy.Fallback]
	if !ok {
		return nil, fmt.Errorf("no compiler registered for fallback mode %d", *strategy.Fallback)
	}

	dylibPath, fallbackErr := fallback(ctx)
	if fallbackErr != nil {
		return nil, fmt.Errorf("fallback compile also failed: %w (primary: %w)", fallbackErr, err)
	}

	return &CompileResult{DylibPath: dylibPath, Degraded: true}, nil
}
