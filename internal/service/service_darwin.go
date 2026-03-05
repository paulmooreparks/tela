//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func plistDir() string {
	return "/Library/LaunchDaemons"
}

func plistPath(binaryName string) string {
	label := plistLabel(binaryName)
	return filepath.Join(plistDir(), label+".plist")
}

func plistLabel(binaryName string) string {
	return "com.tela." + binaryName
}

// Install creates a launchd plist and loads it.
func Install(binaryName string, cfg *Config) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required — run with sudo")
	}

	if err := SaveConfig(binaryName, cfg); err != nil {
		return err
	}

	label := plistLabel(binaryName)

	// The binary reads its own YAML config from the system config
	// directory (e.g. /etc/tela/telahubd.yaml) — no env vars needed.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>service</string>
        <string>run</string>
    </array>
    <key>WorkingDirectory</key>
    <string>%s</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/%s.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/%s.err</string>
</dict>
</plist>
`, label, cfg.BinaryPath, cfg.WorkingDir, binaryName, binaryName)

	if err := os.WriteFile(plistPath(binaryName), []byte(plist), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load the service
	if err := run("launchctl", "load", plistPath(binaryName)); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	return nil
}

// Uninstall unloads and removes the launchd plist.
func Uninstall(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required — run with sudo")
	}

	path := plistPath(binaryName)

	// Best-effort unload
	_ = run("launchctl", "unload", path)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	_ = RemoveConfig(binaryName)
	return nil
}

// Start starts the launchd service.
func Start(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required — run with sudo")
	}
	label := plistLabel(binaryName)
	return run("launchctl", "start", label)
}

// Stop stops the launchd service.
func Stop(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("root privileges required — run with sudo")
	}
	label := plistLabel(binaryName)
	return run("launchctl", "stop", label)
}

// QueryStatus returns the current launchd service status.
func QueryStatus(binaryName string) (*Status, error) {
	// Check if plist exists
	if _, err := os.Stat(plistPath(binaryName)); os.IsNotExist(err) {
		return &Status{Installed: false, Info: "not installed"}, nil
	}

	label := plistLabel(binaryName)
	out, err := exec.Command("launchctl", "list", label).Output()
	if err != nil {
		return &Status{Installed: true, Running: false, Info: "installed (not running)"}, nil
	}

	info := strings.TrimSpace(string(out))
	running := !strings.Contains(info, "Could not find service")

	return &Status{
		Installed: true,
		Running:   running,
		Info:      "installed",
	}, nil
}

// IsElevated returns true if the current process is running as root.
func IsElevated() bool {
	return os.Geteuid() == 0
}

// IsWindowsService always returns false on macOS.
func IsWindowsService() bool {
	return false
}

// Handler wraps a start/stop function pair.
type Handler struct {
	Run func(stopCh <-chan struct{})
}

// RunAsService runs the handler directly (launchd manages lifecycle).
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
