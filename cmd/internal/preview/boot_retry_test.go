package preview

import (
	"context"
	"errors"
	"testing"

	"github.com/k-kohey/axe/internal/idb"
)

func TestBootHeadlessWithRetryFunc_SucceedsAfterRetries(t *testing.T) {
	attempts := 0
	fn := func(_, _ string) (*idb.Companion, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary boot failure")
		}
		return &idb.Companion{}, nil
	}

	companion, err := bootHeadlessWithRetryFunc(context.Background(), "UDID", "/tmp/devset", fn)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if companion == nil {
		t.Fatal("expected non-nil companion")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestBootHeadlessWithRetryFunc_FailsAfterMaxAttempts(t *testing.T) {
	attempts := 0
	fn := func(_, _ string) (*idb.Companion, error) {
		attempts++
		return nil, errors.New("always fails")
	}

	_, err := bootHeadlessWithRetryFunc(context.Background(), "UDID", "/tmp/devset", fn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	wantAttempts := 1 + bootHeadlessMaxRetries
	if attempts != wantAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, wantAttempts)
	}
}

func TestBootHeadlessWithRetryFunc_StopsOnContextCancel(t *testing.T) {
	attempts := 0
	fn := func(_, _ string) (*idb.Companion, error) {
		attempts++
		return nil, errors.New("fails")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bootHeadlessWithRetryFunc(ctx, "UDID", "/tmp/devset", fn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d, want 0 (no attempt after pre-cancel)", attempts)
	}
}
