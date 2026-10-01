package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunBuildInfoCommand runs pc.BuildInfoCommand for sourceFile and device ({file}, {device}) and writes its JSON
// output to a cache file, returning the file's path. The command builds what the preview needs (with whatever build
// system the project uses), so it is also how watch mode rebuilds.
func RunBuildInfoCommand(pc ProjectConfig, sourceFile, device string) (string, error) {
	if device == "" {
		device = "generic"
	}
	command := strings.NewReplacer("{file}", shellQuote(sourceFile), "{device}", shellQuote(device)).Replace(pc.BuildInfoCommand)
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = pc.Root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("BUILD_INFO_COMMAND failed (%s): %w", command, err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(pc.Root + "\x00" + sourceFile))
	path := filepath.Join(cacheDir, "axe", fmt.Sprintf("build-info-%x.json", h[:8]))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
