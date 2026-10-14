package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunBuildInfoCommand runs pc.BuildInfoCommand for sourceFile and device and writes its JSON output to a cache file,
// returning the file's path. The values reach the command as AXE_PREVIEW_FILE and AXE_PREVIEW_DEVICE environment
// variables, never as part of the shell source, so they are not evaluated. The command builds what the preview needs
// (with whatever build system the project uses), so it is also how watch mode rebuilds.
func RunBuildInfoCommand(pc ProjectConfig, sourceFile, device string) (string, error) {
	if strings.Contains(pc.BuildInfoCommand, "{file}") || strings.Contains(pc.BuildInfoCommand, "{device}") {
		return "", fmt.Errorf(`BUILD_INFO_COMMAND no longer substitutes {file}/{device}; use "$AXE_PREVIEW_FILE" instead of {file} and "$AXE_PREVIEW_DEVICE" instead of {device} (%s)`, pc.BuildInfoCommand)
	}
	if device == "" {
		device = "generic"
	}
	cmd := exec.Command("/bin/sh", "-c", pc.BuildInfoCommand)
	cmd.Env = append(os.Environ(), "AXE_PREVIEW_FILE="+sourceFile, "AXE_PREVIEW_DEVICE="+device)
	cmd.Dir = pc.Root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("BUILD_INFO_COMMAND failed (%s): %w", pc.BuildInfoCommand, err)
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
