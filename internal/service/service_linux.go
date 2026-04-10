//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitDir = "/etc/systemd/system"

func unitPath(binaryName string) string {
	return unitDir + "/" + ServiceName(binaryName) + ".service"
}

// Install creates a systemd unit file and enables the service.
func Install(binaryName string, cfg *Config) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required. Run with sudo.")
	}

	if err := SaveConfig(binaryName, cfg); err != nil {
		return err
	}

	svcName := ServiceName(binaryName)

	// Build ExecStart -- the binary reads its YAML config from the
	// standard system location (e.g. /etc/tela/telad.yaml).
	// Quote the binary path in case it contains spaces.
	execStart := `"` + cfg.BinaryPath + `" service run`

	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=%s

[Install]
WantedBy=multi-user.target
`, cfg.Description, execStart, cfg.WorkingDir, svcName)

	if err := os.WriteFile(unitPath(binaryName), []byte(unit), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	// Reload and enable
	if err := run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := run("systemctl", "enable", svcName); err != nil {
		return fmt.Errorf("enable %s: %w", svcName, err)
	}

	return nil
}

// Uninstall stops, disables, and removes the systemd unit file.
func Uninstall(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required. Run with sudo.")
	}

	svcName := ServiceName(binaryName)

	// Best-effort stop + disable
	_ = run("systemctl", "stop", svcName)
	_ = run("systemctl", "disable", svcName)

	path := unitPath(binaryName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	_ = run("systemctl", "daemon-reload")
	return nil
}

// Start starts the systemd service.
func Start(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required. Run with sudo.")
	}
	return run("systemctl", "start", ServiceName(binaryName))
}

// Stop stops the systemd service.
func Stop(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required. Run with sudo.")
	}
	return run("systemctl", "stop", ServiceName(binaryName))
}

// QueryStatus returns the current systemd service status.
func QueryStatus(binaryName string) (*Status, error) {
	svcName := ServiceName(binaryName)

	// Check if unit file exists
	if _, err := os.Stat(unitPath(binaryName)); os.IsNotExist(err) {
		return &Status{Installed: false, Info: "not installed"}, nil
	}

	out, err := exec.Command("systemctl", "is-active", svcName).Output()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		state = "unknown"
	}

	return &Status{
		Installed: true,
		Running:   state == "active",
		Info:      state,
	}, nil
}

// IsElevated returns true if the current process is running as root.
func IsElevated() bool {
	return os.Geteuid() == 0
}

// IsWindowsService always returns false on Linux.
func IsWindowsService() bool {
	return false
}

// Handler wraps a start/stop function pair (used on Windows; on Linux
// the process just runs normally and handles SIGTERM).
type Handler struct {
	Run func(stopCh <-chan struct{})
}

// RunAsService is a no-op on Linux (systemd manages the process lifecycle).
func RunAsService(binaryName string, handler *Handler) error {
	stopCh := make(chan struct{})
	handler.Run(stopCh)
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ── User-level autostart (systemd --user) ─────────────────────────

func userUnitDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func userUnitPath(binaryName string) string {
	return filepath.Join(userUnitDir(), ServiceName(binaryName)+".service")
}

// UserInstall creates a systemd user unit and enables it. No root required.
func UserInstall(binaryName string, cfg *Config) error {
	if err := SaveUserConfig(binaryName, cfg); err != nil {
		return err
	}

	dir := userUnitDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create user unit dir: %w", err)
	}

	svcName := ServiceName(binaryName)
	execStart := fmt.Sprintf(`"%s" service run --user`, cfg.BinaryPath)

	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, cfg.Description, execStart, cfg.WorkingDir)

	if err := os.WriteFile(userUnitPath(binaryName), []byte(unit), 0644); err != nil {
		return fmt.Errorf("write user unit: %w", err)
	}

	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := run("systemctl", "--user", "enable", svcName); err != nil {
		return fmt.Errorf("enable %s: %w", svcName, err)
	}
	return nil
}

// UserUninstall stops, disables, and removes the user unit.
func UserUninstall(binaryName string) error {
	svcName := ServiceName(binaryName)
	_ = run("systemctl", "--user", "stop", svcName)
	_ = run("systemctl", "--user", "disable", svcName)

	path := userUnitPath(binaryName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return nil
}

// UserStart starts the user service.
func UserStart(binaryName string) error {
	return run("systemctl", "--user", "start", ServiceName(binaryName))
}

// UserStop stops the user service.
func UserStop(binaryName string) error {
	return run("systemctl", "--user", "stop", ServiceName(binaryName))
}

// QueryUserStatus returns the status of the user-level service.
func QueryUserStatus(binaryName string) (*Status, error) {
	if _, err := os.Stat(userUnitPath(binaryName)); os.IsNotExist(err) {
		return &Status{Installed: false, UserMode: true, Info: "not installed"}, nil
	}

	out, err := exec.Command("systemctl", "--user", "is-active", ServiceName(binaryName)).Output()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		state = "unknown"
	}

	return &Status{
		Installed: true,
		Running:   state == "active",
		UserMode:  true,
		Info:      state,
	}, nil
}
