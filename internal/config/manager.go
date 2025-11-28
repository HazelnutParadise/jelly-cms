package config

import (
	"encoding/json"
	"os"
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

// Save writes the config to data/config.json
func Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Ensure data directory exists
	if err := os.MkdirAll("data", 0755); err != nil {
		return err
	}

	return os.WriteFile("data/config.json", data, 0644)
}
