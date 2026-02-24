package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "github.com/k-kohey/axe/internal/preview/analysisproto"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	xcodeLibPathOnce sync.Once
	xcodeLibPathVal  string
)

// xcodeToolchainLibPath returns the path to the Xcode toolchain's lib directory
// containing libIndexStore.dylib. The result is cached for the process lifetime.
// Returns empty string on failure (e.g. xcode-select not found or Xcode not installed).
func xcodeToolchainLibPath() string {
	xcodeLibPathOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		out, err := exec.CommandContext(ctx, "xcode-select", "-p").Output()
		if err != nil {
			slog.Debug("xcode-select -p failed; DYLD_LIBRARY_PATH will not be set", "error", err)
			return
		}
		devPath := strings.TrimSpace(string(out))
		if devPath == "" {
			return
		}
		xcodeLibPathVal = filepath.Join(devPath, "Toolchains", "XcodeDefault.xctoolchain", "usr", "lib")
	})
	return xcodeLibPathVal
}

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
	if libPath := xcodeToolchainLibPath(); libPath != "" {
		cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+libPath)
	}
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
