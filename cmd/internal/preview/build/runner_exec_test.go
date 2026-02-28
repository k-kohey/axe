package build

import (
	"context"
	"strings"
	"testing"
)

func TestRun_EmptyArgs(t *testing.T) {
	t.Parallel()

	_, err := run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "empty command args") {
		t.Fatalf("unexpected err: %v", err)
	}
}
