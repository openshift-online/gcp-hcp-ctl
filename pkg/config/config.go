// Package config provides configuration file support for the gcphcpctl CLI.
// Configuration is loaded from ~/.gcphcpctl/config.yaml and can be overridden
// by environment variables and CLI flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the CLI configuration loaded from config file.
type Config struct {
	Project string `yaml:"project"`
	Region  string `yaml:"region"`
	Output  string `yaml:"output"`
}

// DefaultConfigDir returns the default config directory path.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gcphcpctl")
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.yaml")
}

// Load reads configuration from the given path. If the file does not exist,
// it returns an empty Config without error. Returns an error only if the file
// exists but cannot be parsed.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	if path == "" {
		return &Config{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	return &cfg, nil
}
