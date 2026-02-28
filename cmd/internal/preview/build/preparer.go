package build

import (
	"context"
	"sync"
)

// Preparer caches the result of Prepare() so that multiple callers
// (e.g. report-mode captures or concurrent serve-mode streams) pay the cost
// of FetchSettings + Build + ExtractCompilerPaths only once.
//
// Concurrent callers that arrive while a Prepare is already in-flight
// block on the same operation rather than starting a second one.
type Preparer struct {
	mu     sync.Mutex
	flight *inFlight
	cache  *Result
	pc     ProjectConfig
	dirs   ProjectDirs
	reuse  bool
	r      Runner
}

// inFlight represents a Prepare() call that is currently executing.
// Other goroutines wait on done and then read res/err.
type inFlight struct {
	done chan struct{}
	res  *Result
	err  error
}

// NewPreparer creates a Preparer for the given project configuration.
func NewPreparer(pc ProjectConfig, dirs ProjectDirs, reuse bool, r Runner) *Preparer {
	return &Preparer{
		pc:    pc,
		dirs:  dirs,
		reuse: reuse,
		r:     r,
	}
}

// Prepare returns a cached Result or runs the full build pipeline.
// The returned Result is always a clone — callers may mutate it freely
// without affecting the cache or other callers.
//
// Errors are never cached: a subsequent call will retry the pipeline.
func (p *Preparer) Prepare(ctx context.Context) (*Result, error) {
	p.mu.Lock()

	// Cache hit: return a clone with Built=false (build already happened).
	if p.cache != nil {
		r := cloneResult(p.cache)
		r.Built = false
		p.mu.Unlock()
		return r, nil
	}

	// In-flight: another goroutine is already running Prepare.
	// Wait for it to finish, but bail out if our own context is cancelled.
	if p.flight != nil {
		f := p.flight
		p.mu.Unlock()
		select {
		case <-f.done:
			if f.err != nil {
				return nil, f.err
			}
			r := cloneResult(f.res)
			r.Built = false
			return r, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Miss: start a new Prepare.
	f := &inFlight{done: make(chan struct{})}
	p.flight = f
	p.mu.Unlock()

	f.res, f.err = Prepare(ctx, p.pc, p.dirs, p.reuse, p.r)
	close(f.done)

	p.mu.Lock()
	p.flight = nil
	if f.err == nil {
		p.cache = f.res
	}
	p.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	return cloneResult(f.res), nil
}

// Invalidate clears the cached result so that the next Prepare() call
// runs the full pipeline again.
// If a Prepare call is currently in-flight, Invalidate has no effect on
// that call's result; the in-flight result will still be cached on success.
func (p *Preparer) Invalidate() {
	p.mu.Lock()
	p.cache = nil
	p.mu.Unlock()
}

// Cached returns the cached Result without triggering a Prepare.
// Returns nil if no result has been cached yet.
func (p *Preparer) Cached() *Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		return nil
	}
	return cloneResult(p.cache)
}

// cloneResult returns a shallow copy of the Result with a cloned Settings.
// Built is preserved from the original — callers that need to mark a clone
// as "not freshly built" (e.g. cache-hit path) set Built=false explicitly.
func cloneResult(in *Result) *Result {
	return &Result{
		Settings: in.Settings.Clone(),
		Dirs:     in.Dirs,
		Built:    in.Built,
	}
}
