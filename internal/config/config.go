package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	LogRotationSizeMB int    `json:"log_rotation_size_mb"`
	MaxLogRotations   int    `json:"max_log_rotations"`
	PromptPrefix      string `json:"prompt_prefix"`
}

var Global Config

func init() {
	// Set defaults
	Global = Config{
		LogRotationSizeMB: 1,
		MaxLogRotations:   5,
		PromptPrefix:      "psh",
	}
}

func Load() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(home, ".config", "persishtent", "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil // No config, use defaults
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &Global)
}
