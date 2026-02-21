package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigStore_LoadEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStoreWithPath(filepath.Join(dir, "config.json"))

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if cfg.DefaultSimulator != "" {
		t.Errorf("expected empty DefaultSimulator, got %q", cfg.DefaultSimulator)
	}
}

func TestConfigStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStoreWithPath(filepath.Join(dir, "config.json"))

	want := axeConfig{DefaultSimulator: "TEST-UDID-1234"}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultSimulator != want.DefaultSimulator {
		t.Errorf("DefaultSimulator = %q, want %q", got.DefaultSimulator, want.DefaultSimulator)
	}
}

func TestConfigStore_SetDefault(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStoreWithPath(filepath.Join(dir, "config.json"))

	if err := store.SetDefault("UDID-ABCD"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	got, err := store.GetDefault()
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if got != "UDID-ABCD" {
		t.Errorf("GetDefault() = %q, want %q", got, "UDID-ABCD")
	}
}

func TestConfigStore_ClearDefault(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStoreWithPath(filepath.Join(dir, "config.json"))

	if err := store.SetDefault("UDID-ABCD"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := store.ClearDefault(); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}

	got, err := store.GetDefault()
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if got != "" {
		t.Errorf("GetDefault() after clear = %q, want empty", got)
	}
}

func TestConfigStore_LoadCorruptedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")

	// Write invalid JSON.
	if err := os.WriteFile(p, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("writing corrupt file: %v", err)
	}

	store := NewConfigStoreWithPath(p)
	_, err := store.Load()
	if err == nil {
		t.Fatal("expected error on corrupted JSON, got nil")
	}
}

func TestConfigStore_CreatesDirIfMissing(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "dir", "config.json")
	store := NewConfigStoreWithPath(nested)

	if err := store.SetDefault("UDID-1234"); err != nil {
		t.Fatalf("SetDefault with nested path: %v", err)
	}

	// Verify the file was created.
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("config file not created at %s: %v", nested, err)
	}

	got, err := store.GetDefault()
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if got != "UDID-1234" {
		t.Errorf("GetDefault() = %q, want %q", got, "UDID-1234")
	}
}
