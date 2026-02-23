package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"google.golang.org/protobuf/encoding/protojson"
)

// ReadTypeFileMap invokes axe-index-reader on the given index store path
// and returns a map of type names to file paths.
// sourceRoot, if non-empty, limits results to files under that directory.
func ReadTypeFileMap(ctx context.Context, indexStorePath string, sourceRoot string) (map[string]string, error) {
	binPath, err := ensureIndexReader()
	if err != nil {
		return nil, fmt.Errorf("ensuring index reader: %w", err)
	}

	args := []string{indexStorePath}
	if sourceRoot != "" {
		args = append(args, "--source-root", sourceRoot)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("axe-index-reader failed: %w\n%s", err, ee.Stderr)
		}
		return nil, fmt.Errorf("running axe-index-reader: %w", err)
	}

	var tfm pb.TypeFileMap
	if err := protojson.Unmarshal(out, &tfm); err != nil {
		return nil, fmt.Errorf("decoding axe-index-reader output: %w", err)
	}

	slog.Debug("Read type-file map from index store", "types", len(tfm.GetTypes()))
	return tfm.GetTypes(), nil
}
