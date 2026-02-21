package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// axeConfig represents the persistent config stored at ~/Library/Developer/axe/config.json.
type axeConfig struct {
	DefaultSimulator string `json:"defaultSimulator,omitempty"`
}

// ConfigStore reads and writes the axe global config file.
type ConfigStore struct {
	path string
}

// NewConfigStore creates a ConfigStore with the default path
// (~/ Library/Developer/axe/config.json).
func NewConfigStore() (*ConfigStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory: %w", err)
	}
	p := filepath.Join(home, "Library", "Developer", "axe", "config.json")
	return &ConfigStore{path: p}, nil
}

// NewConfigStoreWithPath creates a ConfigStore with a custom path (for testing).
func NewConfigStoreWithPath(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

// Load reads the config file and returns the parsed config.
// Returns an empty config if the file does not exist.
func (s *ConfigStore) Load() (axeConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return axeConfig{}, nil
		}
		return axeConfig{}, fmt.Errorf("reading config: %w", err)
	}
	var cfg axeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return axeConfig{}, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

// Save writes the config to disk atomically, creating parent directories if needed.
// It writes to a temporary file first, then renames to avoid partial writes.
func (s *ConfigStore) Save(cfg axeConfig) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	data = append(data, '\n')

	// Write to a temp file in the same directory, then rename for atomicity.
	tmp, err := os.CreateTemp(dir, ".config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp config file: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		// Remove temp file on any error; on success tmpPath no longer exists
		// (it was renamed), so Remove is a harmless no-op.
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp config file: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming config file: %w", err)
	}
	return nil
}

// GetDefault returns the default simulator UDID, or "" if not set.
func (s *ConfigStore) GetDefault() (string, error) {
	cfg, err := s.Load()
	if err != nil {
		return "", err
	}
	return cfg.DefaultSimulator, nil
}

// SetDefault sets the default simulator UDID.
func (s *ConfigStore) SetDefault(udid string) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	cfg.DefaultSimulator = udid
	return s.Save(cfg)
}

// ClearDefault removes the default simulator setting.
func (s *ConfigStore) ClearDefault() error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	cfg.DefaultSimulator = ""
	return s.Save(cfg)
}
