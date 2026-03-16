// Package service provides cross-platform OS service management for Tela
// binaries (telad, telahubd). It handles install, uninstall, start, stop,
// and status operations using the native service manager on each OS
// (Windows SCM, systemd, launchd).
//
// Each binary reads its runtime configuration from a YAML file in a
// well-known system directory. The service manager simply starts the
// binary with "service run", and the binary loads its config from the
// standard location. To reconfigure, edit the YAML and restart the service.
package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds the minimal metadata needed by the OS service manager.
// The actual runtime configuration (hub URLs, ports, etc.) lives in a
// YAML file that the binary reads on startup.
type Config struct {
	// BinaryPath is the absolute path to the executable.
	BinaryPath string `json:"binaryPath"`

	// Description is a human-readable service description.
	Description string `json:"description,omitempty"`

	// WorkingDir is the working directory for the service process.
	WorkingDir string `json:"workingDir,omitempty"`
}

// Status represents the current state of an installed service.
type Status struct {
	Installed bool
	Running   bool
	Info      string // platform-specific status detail
}

// ServiceName returns the OS service name for a given Tela binary.
// e.g., "telad" → "telad", "telahubd" → "telahubd"
func ServiceName(binaryName string) string {
	return binaryName
}

// ConfigDir returns the directory where service configs are stored.
// Windows: %ProgramData%\Tela
// Linux:   /etc/tela
// macOS:   /etc/tela
func ConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "Tela")
	default:
		return "/etc/tela"
	}
}

// ConfigPath returns the full path to the service config JSON file.
func ConfigPath(binaryName string) string {
	return filepath.Join(ConfigDir(), binaryName+"-service.json")
}

// SaveConfig writes the service configuration to disk.
func SaveConfig(binaryName string, cfg *Config) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := ConfigPath(binaryName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadConfig reads the service configuration from disk.
func LoadConfig(binaryName string) (*Config, error) {
	path := ConfigPath(binaryName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// BinaryConfigPath returns the standard path to a binary's YAML config
// file in the system-wide service config directory.
// e.g. "telad" → C:\ProgramData\Tela\telad.yaml  (Windows)
//      "telad" → /etc/tela/telad.yaml             (Linux/macOS)
func BinaryConfigPath(binaryName string) string {
	return filepath.Join(ConfigDir(), binaryName+".yaml")
}

// Install registers the service with the OS service manager.
// Implemented per-platform in service_*.go files.
// Install(binaryName string, cfg *Config) error

// Uninstall removes the service from the OS service manager.
// Uninstall(binaryName string) error

// Start starts the installed service.
// Start(binaryName string) error

// Stop stops the running service.
// Stop(binaryName string) error

// QueryStatus returns the current status of the service.
// QueryStatus(binaryName string) (*Status, error)

// IsElevated returns true if the process has admin/root privileges.
// Implemented per-platform in service_*.go files.
// IsElevated() bool
