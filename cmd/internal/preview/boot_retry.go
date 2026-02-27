package preview

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k-kohey/axe/internal/idb"
)

const (
	bootHeadlessMaxRetries = 3
)

type bootHeadlessFn func(udid, deviceSetPath string) (*idb.Companion, error)

var (
	bootHeadlessRetryDelay = 2 * time.Second
	bootHeadlessWait       = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
)

func bootHeadlessWithRetry(ctx context.Context, udid, deviceSetPath string) (*idb.Companion, error) {
	return bootHeadlessWithRetryFunc(ctx, udid, deviceSetPath, idb.BootHeadless)
}

func bootHeadlessWithRetryFunc(ctx context.Context, udid, deviceSetPath string, fn bootHeadlessFn) (*idb.Companion, error) {
	maxAttempts := 1 + bootHeadlessMaxRetries
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		companion, err := fn(udid, deviceSetPath)
		if err == nil {
			return companion, nil
		}
		lastErr = err

		if attempt == maxAttempts {
			break
		}

		slog.Warn("Boot attempt failed, retrying",
			"attempt", attempt,
			"maxAttempts", maxAttempts,
			"udid", udid,
			"err", err,
		)

		if err := bootHeadlessWait(ctx, bootHeadlessRetryDelay); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("boot failed after %d attempts: %w", maxAttempts, lastErr)
}
