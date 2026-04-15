package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/paulmooreparks/tela/internal/service"
	"github.com/paulmooreparks/tela/internal/telelog"

	"gopkg.in/yaml.v3"
)

func handleServiceCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, `telachand service -- manage telachand as an OS service or user autostart task

Usage:
  telachand service install -config <file>
      Install as system service (requires admin/root)

  telachand service install --user -config <file>
      Install as user autostart (no admin required)

  telachand service uninstall [--user]      Remove the service or autostart task
  telachand service start [--user]          Start the installed service or task
  telachand service stop [--user]           Stop the running service or task
  telachand service restart [--user]        Restart the service or task
  telachand service status                  Show status of both system and user installations
  telachand service run [--user]            Run in service mode (used by the service manager)

The --user flag selects user-level autostart instead of a system service.
User autostart runs at login without administrator or root privileges.
`)
		os.Exit(1)
	}

	switch os.Args[2] {
	case "install":
		serviceInstall()
	case "uninstall":
		serviceUninstall()
	case "start":
		serviceStart()
	case "stop":
		serviceStop()
	case "restart":
		serviceRestart()
	case "status":
		serviceStatus()
	case "run":
		if service.IsWindowsService() {
			runAsWindowsService()
		} else {
			serviceRun()
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand: %s\n", os.Args[2])
		os.Exit(1)
	}
}

func serviceHasUserFlag() bool {
	for _, arg := range os.Args[3:] {
		if arg == "--user" || arg == "-user" {
			return true
		}
	}
	return false
}

func serviceInstall() {
	// Locate the config file. Unlike telad, telachand always requires an
	// explicit -config flag (no inline flag mode since config is small).
	var configPath string
	userMode := false
	for i, arg := range os.Args[3:] {
		switch arg {
		case "-config", "--config":
			if i+1 < len(os.Args[3:]) {
				configPath = os.Args[4+i]
			}
		case "--user", "-user":
			userMode = true
		}
	}
	// Simple re-parse using flag package for cleanliness.
	_ = configPath
	_ = userMode

	// Re-parse properly.
	configPath = ""
	userMode = false
	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-config", "--config":
			if i+1 < len(args) {
				i++
				configPath = args[i]
			}
		case "--user", "-user":
			userMode = true
		}
	}

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "error: -config <path> is required")
		fmt.Fprintln(os.Stderr, "  telachand service install -config telachand.yaml")
		os.Exit(1)
	}

	absConfig, _ := filepath.Abs(configPath)
	if _, err := loadConfig(absConfig); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid config: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(absConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		os.Exit(1)
	}
	yamlContent := string(data)

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.Abs(exePath)

	svcCfg := &service.Config{
		BinaryPath:  exePath,
		Description: "Tela Channel Daemon -- release channel server",
		YAMLConfig:  service.EncodeYAMLConfig(yamlContent),
	}

	if userMode {
		userDest := service.UserBinaryConfigPath("telachand")
		if err := copyFile(absConfig, userDest); err != nil {
			fmt.Fprintf(os.Stderr, "error copying config to user dir: %v\n", err)
			os.Exit(1)
		}
		if err := service.UserInstall("telachand", svcCfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("telachand user autostart installed")
		fmt.Printf("  config: %s\n", userDest)
		fmt.Println("  start:  telachand service start --user")
	} else {
		dest := service.BinaryConfigPath("telachand")
		if err := copyFile(absConfig, dest); err != nil {
			fmt.Fprintf(os.Stderr, "error copying config: %v\n", err)
			os.Exit(1)
		}
		if err := service.Install("telachand", svcCfg); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("telachand system service installed")
		fmt.Printf("  config: %s\n", dest)
		fmt.Println("  start:  telachand service start")
		fmt.Println("  edit:   " + dest)
	}
}

func serviceUninstall() {
	if serviceHasUserFlag() {
		if err := service.UserUninstall("telachand"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("telachand user autostart uninstalled")
		return
	}
	if err := service.Uninstall("telachand"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telachand system service uninstalled")
	fmt.Printf("  config retained: %s\n", service.BinaryConfigPath("telachand"))
}

func serviceStart() {
	if serviceHasUserFlag() {
		if err := service.UserStart("telachand"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("telachand user autostart started")
		return
	}
	if err := service.Start("telachand"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telachand system service started")
}

func serviceStop() {
	if serviceHasUserFlag() {
		if err := service.UserStop("telachand"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("telachand user autostart stopped")
		return
	}
	if err := service.Stop("telachand"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telachand system service stopped")
}

func serviceRestart() {
	userMode := serviceHasUserFlag()
	fmt.Println("stopping telachand...")
	if userMode {
		_ = service.UserStop("telachand")
	} else {
		_ = service.Stop("telachand")
	}
	time.Sleep(time.Second)
	if userMode {
		if err := service.UserStart("telachand"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := service.Start("telachand"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("telachand restarted")
}

func serviceStatus() {
	st, err := service.QueryStatus("telachand")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("System service:")
	fmt.Printf("  installed: %v\n", st.Installed)
	fmt.Printf("  running:   %v\n", st.Running)
	fmt.Printf("  status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("  config:    %s\n", service.BinaryConfigPath("telachand"))
	}

	ust, err := service.QueryUserStatus("telachand")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("User autostart:")
	fmt.Printf("  installed: %v\n", ust.Installed)
	fmt.Printf("  running:   %v\n", ust.Running)
	fmt.Printf("  status:    %s\n", ust.Info)
	if ust.Installed {
		fmt.Printf("  config:    %s\n", service.UserBinaryConfigPath("telachand"))
	}
}

// serviceRun is called by "telachand service run" from systemd/launchd.
// It handles the stop signal itself (unlike the Windows SCM path).
func serviceRun() {
	svcStop := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(svcStop)
	}()

	if serviceHasUserFlag() {
		serviceRunUserDaemon(svcStop)
	} else {
		serviceRunDaemon(svcStop)
	}
}

func serviceRunDaemon(svcStop <-chan struct{}) {
	logDest := io.Writer(os.Stderr)
	if runtime.GOOS == "windows" && service.IsWindowsService() {
		logPath := service.LogPath("telachand")
		if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, service.ConfigFilePerm()); err == nil {
			logDest = lf
			os.Stderr = lf
		}
	}
	telelog.Init("telachand", logDest)

	svcCfg, err := service.LoadConfig("telachand")
	if err != nil {
		log.Fatalf("service config: %v", err)
	}
	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	cfg := loadServiceConfig(svcCfg, service.BinaryConfigPath("telachand"))

	go runServer(cfg, svcStop)
	<-svcStop
	log.Println("service stopping")
}

func serviceRunUserDaemon(svcStop <-chan struct{}) {
	logDest := io.Writer(os.Stderr)
	logPath := service.UserLogPath("telachand")
	if lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		logDest = lf
	}
	telelog.Init("telachand", logDest)

	svcCfg, err := service.LoadUserConfig("telachand")
	if err != nil {
		log.Fatalf("user service config: %v", err)
	}
	if svcCfg.WorkingDir != "" {
		os.Chdir(svcCfg.WorkingDir)
	}

	cfg := loadServiceConfig(svcCfg, service.UserBinaryConfigPath("telachand"))

	go runServer(cfg, svcStop)
	<-svcStop
	log.Println("service stopping")
}

// loadServiceConfig loads the telachand Config from the standard YAML path,
// falling back to the base64-encoded YAML embedded in the service metadata.
func loadServiceConfig(svcCfg *service.Config, yamlPath string) Config {
	if _, err := os.Stat(yamlPath); err == nil {
		cfg, err := loadConfig(yamlPath)
		if err != nil {
			log.Fatalf("config %s: %v", yamlPath, err)
		}
		log.Printf("loaded config from %s", yamlPath)
		return cfg
	}

	if svcCfg.YAMLConfig == "" {
		log.Fatalf("no config found: expected %s or inline metadata", yamlPath)
	}

	yamlContent, err := service.DecodeYAMLConfig(svcCfg.YAMLConfig)
	if err != nil {
		log.Fatalf("decode inline config: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlContent), &cfg); err != nil {
		log.Fatalf("parse inline config: %v", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":9900"
	}
	if cfg.Data == "" {
		cfg.Data = defaultDataDir()
	}
	log.Println("loaded config from service metadata")
	return cfg
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunDaemon(svcStopCh)
		},
	}
	if err := service.RunAsService("telachand", handler); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), service.ConfigDirPerm()); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, service.ConfigFilePerm()); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
