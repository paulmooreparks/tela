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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds the minimal metadata needed by the OS service manager.
// The actual runtime configuration (hub URLs, ports, etc.) usually lives in a
// YAML file, but can optionally be stored inline in this JSON for better
// permission handling on Windows.
type Config struct {
	// BinaryPath is the absolute path to the executable.
	BinaryPath string `json:"binaryPath"`

	// Description is a human-readable service description.
	Description string `json:"description,omitempty"`

	// WorkingDir is the working directory for the service process.
	WorkingDir string `json:"workingDir,omitempty"`

	// YAMLConfig optionally stores the binary's YAML config inline (base64-encoded).
	// If present, YAMLConfig takes precedence over the separate YAML file.
	// This avoids file permission issues on Windows (service runs as SYSTEM,
	// which may not have read access to user-created files).
	YAMLConfig string `json:"yamlConfig,omitempty"`
}

// Status represents the current state of an installed service.
type Status struct {
	Installed bool
	Running   bool
	UserMode  bool   // true when installed as a user-level autostart task
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

// UserConfigDir returns the user-level config directory for autostart
// tasks that do not require admin/root privileges.
// Windows: %APPDATA%\Tela
// Linux:   ~/.tela
// macOS:   ~/.tela
func UserConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Tela")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming", "Tela")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".tela")
	}
}

// UserConfigPath returns the path to the user-level service config JSON.
func UserConfigPath(binaryName string) string {
	return filepath.Join(UserConfigDir(), binaryName+"-service.json")
}

// UserBinaryConfigPath returns the path to the user-level binary YAML
// config (e.g., ~/.tela/tela.yaml).
func UserBinaryConfigPath(binaryName string) string {
	return filepath.Join(UserConfigDir(), binaryName+".yaml")
}

// UserLogPath returns the path to the user-level log file.
func UserLogPath(binaryName string) string {
	return filepath.Join(UserConfigDir(), binaryName+".log")
}

// SaveUserConfig writes the service configuration to the user dir.
func SaveUserConfig(binaryName string, cfg *Config) error {
	dir := UserConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create user config dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := UserConfigPath(binaryName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadUserConfig reads the service configuration from the user dir.
func LoadUserConfig(binaryName string) (*Config, error) {
	path := UserConfigPath(binaryName)
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

// ConfigDirPerm returns the permission mode for the service config directory.
// On Windows, directories under ProgramData must be accessible to the SYSTEM
// account (which runs services). On Unix, 0700 is fine since the service
// typically runs as root.
func ConfigDirPerm() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0755
	}
	return 0700
}

// ConfigFilePerm returns the permission mode for service config files.
// On Windows, files must be readable by the SYSTEM account. On Unix,
// 0600 restricts access to the owning user (typically root).
func ConfigFilePerm() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0644
	}
	return 0600
}

// SaveConfig writes the service configuration to disk.
func SaveConfig(binaryName string, cfg *Config) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, ConfigDirPerm()); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := ConfigPath(binaryName)
	if err := os.WriteFile(path, data, ConfigFilePerm()); err != nil {
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
//
//	"telad" → /etc/tela/telad.yaml             (Linux/macOS)
func BinaryConfigPath(binaryName string) string {
	return filepath.Join(ConfigDir(), binaryName+".yaml")
}

// LogPath returns the path to the service log file.
// e.g. "telad" → C:\ProgramData\Tela\telad.log  (Windows)
//
//	"telad" → /etc/tela/telad.log             (Linux/macOS)
func LogPath(binaryName string) string {
	return filepath.Join(ConfigDir(), binaryName+".log")
}

// EncodeYAMLConfig encodes YAML content as base64 for storage in Config.YAMLConfig.
func EncodeYAMLConfig(yamlContent string) string {
	return base64.StdEncoding.EncodeToString([]byte(yamlContent))
}

// DecodeYAMLConfig decodes the base64-encoded YAML from Config.YAMLConfig.
func DecodeYAMLConfig(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode YAML config: %w", err)
	}
	return string(data), nil
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
