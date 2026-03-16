//go:build windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

var procSetServiceObjectSecurity = syscall.NewLazyDLL("advapi32.dll").NewProc("SetServiceObjectSecurity")

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

	// Grant SYSTEM read+execute on the binary and config so the service can start.
	// User-created directories (e.g. "C:\Program Files\Tela") may not inherit
	// SYSTEM access from the parent.
	grantSystemAccess(cfg.BinaryPath)
	grantSystemAccess(ConfigDir())

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

	// Set an explicit DACL on the service granting Administrators and SYSTEM
	// full control. The default DACL inherited from the SCM may not include
	// these on all Windows configurations, which causes "Access is denied"
	// when trying to start or stop the service later.
	setServiceDACL(s.Handle)

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

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	svcName := ServiceName(binaryName)
	s, err := m.OpenService(svcName)
	if err != nil {
		return fmt.Errorf("open service %s: %w", svcName, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		// Query the service config to show the binary path for diagnostics.
		if cfg, qerr := s.Config(); qerr == nil {
			return fmt.Errorf("start service %s: %w\n  binary: %s", svcName, err, cfg.BinaryPathName)
		}
		return fmt.Errorf("start service %s: %w", svcName, err)
	}
	return nil
}

// Stop stops the running service.
func Stop(binaryName string) error {
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
		return fmt.Errorf("open service %s: %w", svcName, err)
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop service %s: %w", svcName, err)
	}
	return nil
}

// QueryStatus returns the current state of the service.
func QueryStatus(binaryName string) (*Status, error) {
	svcName := ServiceName(binaryName)
	h, err := openServiceHandle(svcName, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		// Service does not exist or SCM not accessible
		return &Status{Installed: false, Info: "not installed"}, nil
	}
	defer windows.CloseServiceHandle(h)

	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return &Status{Installed: true, Info: fmt.Sprintf("query error: %v", err)}, nil
	}

	running := st.CurrentState == windows.SERVICE_RUNNING
	stateStr := "unknown"
	switch st.CurrentState {
	case windows.SERVICE_STOPPED:
		stateStr = "stopped"
	case windows.SERVICE_START_PENDING:
		stateStr = "start pending"
	case windows.SERVICE_STOP_PENDING:
		stateStr = "stop pending"
	case windows.SERVICE_RUNNING:
		stateStr = "running"
	case windows.SERVICE_CONTINUE_PENDING:
		stateStr = "continue pending"
	case windows.SERVICE_PAUSE_PENDING:
		stateStr = "pause pending"
	case windows.SERVICE_PAUSED:
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

// openServiceHandle opens a service with the specified access rights using
// the Windows API directly. The Go mgr.OpenService always requests
// SERVICE_ALL_ACCESS which can fail even when elevated. Using minimal
// access rights (e.g. SERVICE_START, SERVICE_STOP) avoids this.
func openServiceHandle(svcName string, access uint32) (windows.Handle, error) {
	scm, err := openSCManager(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(scm)

	svcNamePtr, err := syscall.UTF16PtrFromString(svcName)
	if err != nil {
		return 0, err
	}
	h, err := windows.OpenService(scm, svcNamePtr, access)
	if err != nil {
		return 0, err
	}
	return h, nil
}

// openSCManager opens the Service Control Manager with the specified access
// rights. The Go mgr.Connect always requests SC_MANAGER_ALL_ACCESS, but
// SC_MANAGER_CONNECT is sufficient for opening existing services.
func openSCManager(access uint32) (windows.Handle, error) {
	h, err := windows.OpenSCManager(nil, nil, access)
	if err != nil {
		return 0, fmt.Errorf("connect to SCM: %w", err)
	}
	return h, nil
}

// setServiceDACL sets the standard Windows service DACL on the given service
// handle. This grants SYSTEM and Administrators full control, and Interactive
// and Service users read access. Without this, some Windows configurations
// deny Administrators the ability to start/stop services they created.
func setServiceDACL(handle windows.Handle) {
	// Standard Windows service SDDL:
	//   SY = SYSTEM (full control)
	//   BA = Built-in Administrators (full control)
	//   IU = Interactive Users (read/query)
	//   SU = Service Logon Users (read/query)
	const sddl = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"

	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return
	}

	// SetServiceObjectSecurity(hService, DACL_SECURITY_INFORMATION, lpSecurityDescriptor)
	r1, _, _ := procSetServiceObjectSecurity.Call(
		uintptr(handle),
		uintptr(windows.DACL_SECURITY_INFORMATION),
		uintptr(unsafe.Pointer(sd)),
	)
	_ = r1 // Non-fatal; best-effort.
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

