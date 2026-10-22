package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type DatabaseConfig struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type Config struct {
	Database DatabaseConfig `json:"database"`
}

var GlobalConfig *Config

// Load attempts to load config from file.
func Load() (*Config, error) {
	cfg := &Config{}

	// Only load from file
	data, err := os.ReadFile("data/config.json")
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	GlobalConfig = cfg
	return cfg, nil
}

const (
	configDir  = "data"
	configName = "config.json"
)

// Save writes the config to data/config.json atomically. The new content is
// written to a temporary file in the same directory and only replaces the
// existing file after it has been synced and closed. A failure before the
// rename leaves the previous config untouched and removes the temporary file.
func Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	tmp, err := os.CreateTemp(configDir, "."+configName+".*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}

	written, err := tmp.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}

	if err := os.Rename(tmpName, filepath.Join(configDir, configName)); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	renamed = true

	return nil
}
