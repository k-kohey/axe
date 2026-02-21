package preview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildLock_LockUnlock(t *testing.T) {
	dir := t.TempDir()
	lock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lock.Lock(ctx); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	lock.Unlock()
}

func TestBuildLock_ExclusiveAccess(t *testing.T) {
	dir := t.TempDir()

	lock1 := newBuildLock(dir)
	lock2 := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lock1.Lock(ctx); err != nil {
		t.Fatalf("Lock1 failed: %v", err)
	}

	// lock2 should block until lock1 is released.
	var mu sync.Mutex
	acquired := false

	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		if err := lock2.Lock(ctx2); err != nil {
			t.Errorf("Lock2 failed: %v", err)
			return
		}
		mu.Lock()
		acquired = true
		mu.Unlock()
		lock2.Unlock()
	}()

	// Give the goroutine time to attempt the lock.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if acquired {
		mu.Unlock()
		t.Fatal("Lock2 should not have been acquired while Lock1 is held")
	}
	mu.Unlock()

	// Release lock1; lock2 should acquire.
	lock1.Unlock()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if !acquired {
		mu.Unlock()
		t.Fatal("Lock2 should have been acquired after Lock1 was released")
	}
	mu.Unlock()
}

func TestBuildLock_ContextCancellation(t *testing.T) {
	dir := t.TempDir()

	lock1 := newBuildLock(dir)
	lock2 := newBuildLock(dir)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()

	if err := lock1.Lock(ctx1); err != nil {
		t.Fatalf("Lock1 failed: %v", err)
	}
	defer lock1.Unlock()

	// Cancel lock2's context quickly.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()

	err := lock2.Lock(ctx2)
	if err == nil {
		lock2.Unlock()
		t.Fatal("Lock2 should have failed due to context cancellation")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestBuildLock_InvalidDirectory(t *testing.T) {
	// Lock file under a non-existent, non-creatable path should fail.
	lock := newBuildLock("/dev/null/impossible")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := lock.Lock(ctx)
	if err == nil {
		lock.Unlock()
		t.Fatal("Lock should have failed for invalid directory")
	}
	if !strings.Contains(err.Error(), "creating lock directory") {
		t.Errorf("expected 'creating lock directory' error, got: %v", err)
	}
}

func TestBuildLock_ReadOnlyLockFile(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".axe-build.lock")

	// Create a directory where the lock file would be, making OpenFile fail.
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}

	lock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := lock.Lock(ctx)
	if err == nil {
		lock.Unlock()
		t.Fatal("Lock should have failed when lock path is a directory")
	}
	if !strings.Contains(err.Error(), "opening lock file") {
		t.Errorf("expected 'opening lock file' error, got: %v", err)
	}
}

func TestBuildLock_DoubleUnlockIsSafe(t *testing.T) {
	dir := t.TempDir()
	lock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lock.Lock(ctx); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	lock.Unlock()
	lock.Unlock() // should not panic
}

func TestBuildLock_RLockRUnlock(t *testing.T) {
	dir := t.TempDir()
	lock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lock.RLock(ctx); err != nil {
		t.Fatalf("RLock failed: %v", err)
	}
	lock.RUnlock()
}

func TestBuildLock_SharedReaders(t *testing.T) {
	dir := t.TempDir()

	lock1 := newBuildLock(dir)
	lock2 := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Both shared locks should be acquired simultaneously.
	if err := lock1.RLock(ctx); err != nil {
		t.Fatalf("RLock1 failed: %v", err)
	}
	defer lock1.RUnlock()

	if err := lock2.RLock(ctx); err != nil {
		t.Fatalf("RLock2 failed: %v", err)
	}
	defer lock2.RUnlock()
}

func TestBuildLock_ExclusiveBlocksShared(t *testing.T) {
	dir := t.TempDir()

	exLock := newBuildLock(dir)
	shLock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := exLock.Lock(ctx); err != nil {
		t.Fatalf("Lock (exclusive) failed: %v", err)
	}

	// Shared lock should block while exclusive is held.
	var mu sync.Mutex
	acquired := false

	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		if err := shLock.RLock(ctx2); err != nil {
			t.Errorf("RLock failed: %v", err)
			return
		}
		mu.Lock()
		acquired = true
		mu.Unlock()
		shLock.RUnlock()
	}()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if acquired {
		mu.Unlock()
		t.Fatal("RLock should not have been acquired while exclusive lock is held")
	}
	mu.Unlock()

	// Release exclusive; shared should acquire.
	exLock.Unlock()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if !acquired {
		mu.Unlock()
		t.Fatal("RLock should have been acquired after exclusive lock was released")
	}
	mu.Unlock()
}

func TestBuildLock_SharedBlocksExclusive(t *testing.T) {
	dir := t.TempDir()

	shLock := newBuildLock(dir)
	exLock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := shLock.RLock(ctx); err != nil {
		t.Fatalf("RLock failed: %v", err)
	}

	// Exclusive lock should block while shared is held.
	var mu sync.Mutex
	acquired := false

	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		if err := exLock.Lock(ctx2); err != nil {
			t.Errorf("Lock (exclusive) failed: %v", err)
			return
		}
		mu.Lock()
		acquired = true
		mu.Unlock()
		exLock.Unlock()
	}()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if acquired {
		mu.Unlock()
		t.Fatal("Exclusive lock should not have been acquired while shared lock is held")
	}
	mu.Unlock()

	// Release shared; exclusive should acquire.
	shLock.RUnlock()

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	if !acquired {
		mu.Unlock()
		t.Fatal("Exclusive lock should have been acquired after shared lock was released")
	}
	mu.Unlock()
}

func TestBuildLock_RLockContextCancellation(t *testing.T) {
	dir := t.TempDir()

	exLock := newBuildLock(dir)
	shLock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := exLock.Lock(ctx); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	defer exLock.Unlock()

	// RLock should fail when context is cancelled while waiting.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()

	err := shLock.RLock(ctx2)
	if err == nil {
		shLock.RUnlock()
		t.Fatal("RLock should have failed due to context cancellation")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestBuildLock_DoubleRUnlockIsSafe(t *testing.T) {
	dir := t.TempDir()
	lock := newBuildLock(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := lock.RLock(ctx); err != nil {
		t.Fatalf("RLock failed: %v", err)
	}

	lock.RUnlock()
	lock.RUnlock() // should not panic
}
