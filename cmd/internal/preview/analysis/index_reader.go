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

// readIndexStore invokes axe-index-reader on the given index store path
// and returns the full IndexStoreResult with per-file data and type→file map.
// sourceRoot, if non-empty, limits results to files under that directory.
func readIndexStore(ctx context.Context, indexStorePath string, sourceRoot string) (*pb.IndexStoreResult, error) {
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

	var result pb.IndexStoreResult
	if err := protojson.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("decoding axe-index-reader output: %w", err)
	}

	slog.Debug("Read index store",
		"files", len(result.GetFiles()),
		"typeMapEntries", len(result.GetTypeFileMap()),
	)
	return &result, nil
}

// readTypeFileMultiMap reads the index store and returns a multi-map of
// type names to file paths. Used by ResolveTransitiveDependencies for BFS.
func readTypeFileMultiMap(ctx context.Context, indexStorePath string, sourceRoot string) (map[string][]string, error) {
	result, err := readIndexStore(ctx, indexStorePath, sourceRoot)
	if err != nil {
		return nil, err
	}
	flat := result.GetTypeFileMap()
	multiMap := make(map[string][]string, len(flat))
	for typeName, filePath := range flat {
		multiMap[typeName] = []string{filePath}
	}
	return multiMap, nil
}
