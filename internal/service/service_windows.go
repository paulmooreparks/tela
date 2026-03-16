//go:build windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Install registers the service with the Windows Service Control Manager.
func Install(binaryName string, cfg *Config) error {
	if !IsElevated() {
		return fmt.Errorf("administrator privileges required. Run from an elevated prompt.")
	}

	if err := SaveConfig(binaryName, cfg); err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	svcName := ServiceName(binaryName)

	// Check if already installed
	s, err := m.OpenService(svcName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q is already installed. Uninstall first.", svcName)
	}

	// Build the command line: binary "service" "run"
	// The service runner will load config from the JSON file.
	// Quote the binary path in case it contains spaces (e.g. "C:\Program Files\...").
	binPath := `"` + cfg.BinaryPath + `" service run`

	s, err = m.CreateService(svcName, binPath, mgr.Config{
		DisplayName:  svcName + " - Tela",
		Description:  cfg.Description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	})
	if err != nil {
		return fmt.Errorf("create service %q: %w", svcName, err)
	}
	defer s.Close()

	// Set recovery actions: restart after 5s on first three failures.
	err = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 86400) // reset failure count after 24h
	if err != nil {
		// Non-fatal -- log but continue
		fmt.Fprintf(os.Stderr, "warning: could not set recovery actions: %v\n", err)
	}

	// Grant SYSTEM read+execute on the binary and config so the service can start.
	// User-created directories (e.g. "C:\Program Files\Tela") may not inherit
	// SYSTEM access from the parent.
	grantSystemAccess(cfg.BinaryPath)
	grantSystemAccess(ConfigDir())

	return nil
}

// Uninstall removes the service from the SCM and deletes the config file.
func Uninstall(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("administrator privileges required. Run from an elevated prompt.")
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	svcName := ServiceName(binaryName)
	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("open service %q: %w (is it installed?)", svcName, err)
	}
	defer s.Close()

	// Stop if running
	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		_, _ = s.Control(svc.Stop)
		// Wait briefly for stop
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			status, err = s.Query()
			if err != nil || status.State == svc.Stopped {
				break
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", svcName, err)
	}

	return nil
}

// Start starts the installed service.
func Start(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("administrator privileges required. Run from an elevated prompt.")
	}

	svcName := ServiceName(binaryName)
	out, err := exec.Command("sc", "start", svcName).CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if strings.Contains(output, "1056") {
			return fmt.Errorf("service %s is already running", svcName)
		}
		return fmt.Errorf("start service %s: %s", svcName, output)
	}
	return nil
}

// Stop stops the running service.
func Stop(binaryName string) error {
	if !IsElevated() {
		return fmt.Errorf("administrator privileges required. Run from an elevated prompt.")
	}

	svcName := ServiceName(binaryName)
	out, err := exec.Command("sc", "stop", svcName).CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if strings.Contains(output, "1062") {
			return fmt.Errorf("service %s is not running", svcName)
		}
		return fmt.Errorf("stop service %s: %s", svcName, output)
	}
	return nil
}

// QueryStatus returns the current state of the service.
func QueryStatus(binaryName string) (*Status, error) {
	m, err := mgr.Connect()
	if err != nil {
		// Non-elevated users can still query via sc.exe
		return queryStatusFallback(binaryName)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName(binaryName))
	if err != nil {
		return &Status{Installed: false, Info: "not installed"}, nil
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return &Status{Installed: true, Info: fmt.Sprintf("query error: %v", err)}, nil
	}

	running := st.State == svc.Running
	stateStr := "unknown"
	switch st.State {
	case svc.Stopped:
		stateStr = "stopped"
	case svc.StartPending:
		stateStr = "start pending"
	case svc.StopPending:
		stateStr = "stop pending"
	case svc.Running:
		stateStr = "running"
	case svc.ContinuePending:
		stateStr = "continue pending"
	case svc.PausePending:
		stateStr = "pause pending"
	case svc.Paused:
		stateStr = "paused"
	}

	return &Status{
		Installed: true,
		Running:   running,
		Info:      stateStr,
	}, nil
}

// IsElevated returns true if the current process is running as Administrator.
func IsElevated() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// IsWindowsService returns true if the process was started by the SCM.
func IsWindowsService() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return is
}

// Handler wraps a start/stop function pair for the Windows SCM.
type Handler struct {
	// Run is the main function. It must block until stopCh is closed,
	// then shut down gracefully.
	Run func(stopCh <-chan struct{})
}

// Execute implements svc.Handler for the Windows Service Control Manager.
func (h *Handler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	stopCh := make(chan struct{})
	done := make(chan struct{})

	go func() {
		h.Run(stopCh)
		close(done)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stopCh)
				<-done
				return false, 0
			}
		case <-done:
			// Service exited on its own
			return false, 0
		}
	}
}

// RunAsService runs the handler under the Windows SCM.
// This should only be called when IsWindowsService() returns true.
func RunAsService(binaryName string, handler *Handler) error {
	return svc.Run(ServiceName(binaryName), handler)
}

// grantSystemAccess uses icacls to ensure the SYSTEM account has read+execute
// access to the given path. For directories, access is inherited by children.
// Errors are silently ignored (non-fatal).
func grantSystemAccess(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.IsDir() {
		// (OI)(CI) = inherit to files and subdirectories
		_ = exec.Command("icacls", path, "/grant", "SYSTEM:(OI)(CI)(RX)", "/T", "/Q").Run()
	} else {
		_ = exec.Command("icacls", path, "/grant", "SYSTEM:(RX)", "/Q").Run()
		// Also grant SYSTEM access to the directory containing the binary
		dir := filepath.Dir(path)
		_ = exec.Command("icacls", dir, "/grant", "SYSTEM:(OI)(CI)(RX)", "/Q").Run()
	}
}

// queryStatusFallback uses sc.exe when the SCM API isn't accessible.
func queryStatusFallback(binaryName string) (*Status, error) {
	svcName := ServiceName(binaryName)
	out, err := exec.Command("sc", "query", svcName).Output()
	if err != nil {
		return &Status{Installed: false, Info: "not installed"}, nil
	}
	output := string(out)
	if strings.Contains(output, "RUNNING") {
		return &Status{Installed: true, Running: true, Info: "running"}, nil
	}
	if strings.Contains(output, "STOPPED") {
		return &Status{Installed: true, Running: false, Info: "stopped"}, nil
	}
	return &Status{Installed: true, Running: false, Info: "installed"}, nil
}
