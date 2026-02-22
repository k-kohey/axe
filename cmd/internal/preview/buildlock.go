package preview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// buildLock provides file-based reader-writer locking for the shared build directory.
// Multiple preview processes for the same project share a single Build dir.
//
// Lock/Unlock (LOCK_EX): exclusive access for xcodebuild execution.
// RLock/RUnlock (LOCK_SH): shared access for reading build artifacts
// (compileThunk, extractCompilerPaths, installApp).
//
// LOCK_SH holders can coexist, but LOCK_EX blocks until all LOCK_SH are released
// and vice versa. This mirrors Xcode's ResourceGraph Actor serialization
// (see docs/xcode-preview-reverse-engineering.md).
type buildLock struct {
	path string
	file *os.File
}

func newBuildLock(buildDir string) *buildLock {
	return &buildLock{
		path: filepath.Join(buildDir, ".axe-build.lock"),
	}
}

// Lock acquires an exclusive file lock, polling until the lock is obtained or
// ctx is cancelled.
func (l *buildLock) Lock(ctx context.Context) error {
	return l.lockWithMode(ctx, syscall.LOCK_EX)
}

// RLock acquires a shared file lock. Multiple readers can hold LOCK_SH
// simultaneously, but LOCK_SH blocks while LOCK_EX is held (and vice versa).
func (l *buildLock) RLock(ctx context.Context) error {
	return l.lockWithMode(ctx, syscall.LOCK_SH)
}

// Unlock releases the file lock and closes the underlying file.
func (l *buildLock) Unlock() {
	if l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// RUnlock releases the shared lock. Implementation is identical to Unlock.
func (l *buildLock) RUnlock() {
	l.Unlock()
}

// lockWithMode acquires a file lock with the given mode (LOCK_EX or LOCK_SH),
// polling until the lock is obtained or ctx is cancelled. The poll interval
// avoids busy-waiting while keeping responsiveness reasonable.
func (l *buildLock) lockWithMode(ctx context.Context, mode int) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("creating lock directory: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening lock file: %w", err)
	}
	l.file = f
	defer func() {
		if retErr != nil {
			_ = f.Close()
			l.file = nil
		}
	}()

	for {
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		// Only retry on EWOULDBLOCK (lock held by another process).
		// Any other error (EBADF, etc.) is unrecoverable.
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("flock: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			// Retry after short interval.
		}
	}
}
