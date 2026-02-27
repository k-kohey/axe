package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetPreviewGlobals zeroes all package-level preview flags so that
// tests do not leak state into each other.
func resetPreviewGlobals(t *testing.T) {
	t.Helper()
	previewProject = ""
	previewWorkspace = ""
	previewScheme = ""
	previewConfiguration = ""
	previewDevice = ""
	t.Cleanup(func() {
		previewProject = ""
		previewWorkspace = ""
		previewScheme = ""
		previewConfiguration = ""
		previewDevice = ""
	})
}

// chdir changes the working directory and restores it on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// writeAxeRC writes a .axerc file in dir with the given content.
func writeAxeRC(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".axerc"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjectConfig_AxeRCExclusivity(t *testing.T) {
	t.Run("error when .axerc has both PROJECT and WORKSPACE", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "PROJECT=./App.xcodeproj\nWORKSPACE=./App.xcworkspace\nSCHEME=MyScheme\n")
		chdir(t, dir)

		_, err := resolveProjectConfig()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "PROJECT and WORKSPACE in .axerc are mutually exclusive") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("no error when flag overrides .axerc with both", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "PROJECT=./App.xcodeproj\nWORKSPACE=./App.xcworkspace\nSCHEME=MyScheme\n")
		chdir(t, dir)

		previewProject = filepath.Join(dir, "Flag.xcodeproj")
		previewScheme = "FlagScheme"

		pc, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(pc.Project, "Flag.xcodeproj") {
			t.Errorf("expected Flag.xcodeproj, got %s", pc.Project)
		}
	})

	t.Run("uses .axerc PROJECT when only PROJECT is set", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "PROJECT=./App.xcodeproj\nSCHEME=MyScheme\n")
		chdir(t, dir)

		pc, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(pc.Project, "App.xcodeproj") {
			t.Errorf("expected App.xcodeproj, got %s", pc.Project)
		}
	})

	t.Run("uses .axerc WORKSPACE when only WORKSPACE is set", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "WORKSPACE=./App.xcworkspace\nSCHEME=MyScheme\n")
		chdir(t, dir)

		pc, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(pc.Workspace, "App.xcworkspace") {
			t.Errorf("expected App.xcworkspace, got %s", pc.Workspace)
		}
	})
}

func TestResolveProjectConfig_LocalVariables(t *testing.T) {
	t.Run("globals not mutated by auto-detect", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "App.xcodeproj"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeAxeRC(t, dir, "SCHEME=MyScheme\n")
		chdir(t, dir)

		_, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if previewProject != "" {
			t.Errorf("previewProject was mutated to %q, want empty", previewProject)
		}
		if previewWorkspace != "" {
			t.Errorf("previewWorkspace was mutated to %q, want empty", previewWorkspace)
		}
	})

	t.Run("globals not mutated by .axerc fallback", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "PROJECT=./App.xcodeproj\nSCHEME=MyScheme\n")
		chdir(t, dir)

		_, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if previewProject != "" {
			t.Errorf("previewProject was mutated to %q, want empty", previewProject)
		}
	})

	t.Run("scheme and device globals are set from .axerc", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		writeAxeRC(t, dir, "PROJECT=./App.xcodeproj\nSCHEME=RCScheme\nDEVICE=AAAA-BBBB\n")
		chdir(t, dir)

		_, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if previewScheme != "RCScheme" {
			t.Errorf("previewScheme = %q, want RCScheme", previewScheme)
		}
		if previewDevice != "AAAA-BBBB" {
			t.Errorf("previewDevice = %q, want AAAA-BBBB", previewDevice)
		}
	})
}

func TestResolveProjectConfig_Priority(t *testing.T) {
	t.Run("flag takes priority over auto-detect", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		// Auto-detect would find this workspace
		if err := os.Mkdir(filepath.Join(dir, "Auto.xcworkspace"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeAxeRC(t, dir, "SCHEME=MyScheme\n")
		chdir(t, dir)

		previewProject = filepath.Join(dir, "Flag.xcodeproj")
		previewScheme = "FlagScheme"

		pc, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(pc.Project, "Flag.xcodeproj") {
			t.Errorf("expected flag project, got %s", pc.Project)
		}
		if pc.Workspace != "" {
			t.Errorf("expected empty workspace, got %s", pc.Workspace)
		}
	})

	t.Run("auto-detect takes priority over .axerc", func(t *testing.T) {
		resetPreviewGlobals(t)
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "Auto.xcworkspace"), 0o750); err != nil {
			t.Fatal(err)
		}
		writeAxeRC(t, dir, "PROJECT=./RC.xcodeproj\nSCHEME=MyScheme\n")
		chdir(t, dir)

		pc, err := resolveProjectConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(pc.Workspace, "Auto.xcworkspace") {
			t.Errorf("expected auto-detected workspace, got %s", pc.Workspace)
		}
		if pc.Project != "" {
			t.Errorf("expected empty project, got %s", pc.Project)
		}
	})
}
