package preview

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k-kohey/axe/internal/idb"
)

const (
	bootMaxRetries = 3
)

type bootFn func(udid, deviceSetPath string) (*idb.Companion, error)

var (
	bootRetryDelay = 2 * time.Second
	bootWait       = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
)

func bootWithRetry(ctx context.Context, udid, deviceSetPath string, headless bool) (*idb.Companion, error) {
	fn := idb.Boot
	if headless {
		fn = idb.BootHeadless
	}
	return bootWithRetryFunc(ctx, udid, deviceSetPath, fn)
}

func bootWithRetryFunc(ctx context.Context, udid, deviceSetPath string, fn bootFn) (*idb.Companion, error) {
	maxAttempts := 1 + bootMaxRetries
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

		if err := bootWait(ctx, bootRetryDelay); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("boot failed after %d attempts: %w", maxAttempts, lastErr)
}
