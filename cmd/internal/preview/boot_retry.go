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
	bootHeadlessRetryDelay = 2 * time.Second
)

type bootHeadlessFn func(udid, deviceSetPath string) (*idb.Companion, error)

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

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(bootHeadlessRetryDelay):
		}
	}

	return nil, fmt.Errorf("boot failed after %d attempts: %w", maxAttempts, lastErr)
}
