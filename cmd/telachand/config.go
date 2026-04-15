package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config is the telachand runtime configuration. It is read from a YAML
// file at startup. All fields have defaults so the daemon runs without a
// config file for quick testing.
type Config struct {
	// Listen is the HTTP listen address. Default: ":9900".
	Listen string `yaml:"listen,omitempty"`

	// Data is the directory that holds channel manifest files and the
	// files/ subdirectory of binary downloads. Default: platform-specific.
	Data string `yaml:"data,omitempty"`

	// PublicURL is the base URL that update clients use to reach this
	// server (e.g. "http://192.168.1.10:9900"). It is embedded in
	// generated manifests as the downloadBase prefix. Required for
	// "telachand publish" unless -base-url is supplied on the command line.
	PublicURL string `yaml:"publicURL,omitempty"`

	// Update configures telachand's own self-update behaviour.
	Update updateConfig `yaml:"update,omitempty"`
}

type updateConfig struct {
	// Channel is the channel telachand checks when self-updating.
	// Default: "stable".
	Channel string `yaml:"channel,omitempty"`

	// Base is an alternative manifest base URL. If empty, the official
	// GitHub release URL is used.
	Base string `yaml:"base,omitempty"`
}

// defaultDataDir returns the platform-appropriate default data directory.
func defaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "telachand")
		}
		return filepath.Join(os.Getenv("APPDATA"), "telachand")
	default:
		return "/var/lib/telachand"
	}
}

// defaultConfig returns a Config populated with sensible defaults.
func defaultConfig() Config {
	return Config{
		Listen: ":9900",
		Data:   defaultDataDir(),
		Update: updateConfig{Channel: "stable"},
	}
}

// loadConfig reads a YAML config file and returns the resulting Config.
// If path is empty, defaultConfig is returned without error.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	// Apply defaults for fields left empty in the file.
	if cfg.Listen == "" {
		cfg.Listen = ":9900"
	}
	if cfg.Data == "" {
		cfg.Data = defaultDataDir()
	}
	if cfg.Update.Channel == "" {
		cfg.Update.Channel = "stable"
	}
	return cfg, nil
}

// saveConfig writes cfg to path as YAML, creating parent directories.
func saveConfig(cfg Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
