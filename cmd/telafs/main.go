/*
telafs -- Mount Tela file shares as a local drive via WebDAV.

telafs starts a WebDAV server backed by the file shares of connected
machines. Windows Explorer, macOS Finder, and Linux file managers can
mount it as a network drive.

Usage:

	telafs                    Start on default port (18080)
	telafs -port 9999         Start on custom port
	telafs version            Print version and exit
	telafs service install    Install as OS service
	telafs service start      Start the installed service
	telafs service stop       Stop the running service
	telafs service status     Show service status
	telafs service uninstall  Remove the service

Then map a network drive in Windows:

	net use T: http://localhost:18080/

Or in Explorer: This PC -> Map network drive -> \\localhost@18080\DavWWWRoot

The root listing shows each connected machine that has file sharing
enabled. Subdirectories are the remote file system.

Requires a running tela connect session (reads control.json).

Environment:

	TELAFS_PORT    Listen port (default 18080)
*/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/webdav"

	"github.com/paulmooreparks/tela/internal/service"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var (
	controlBase  string
	controlToken string
)

// ── Directory listing cache ──────────────────────────────────────

const cacheTTL = 3 * time.Second

type cacheEntry struct {
	resp    *fsResponse
	fetched time.Time
}

var (
	dirCache   = map[string]*cacheEntry{}
	dirCacheMu sync.Mutex
)

func cachedList(machine, path string) (*fsResponse, error) {
	key := machine + ":" + path
	dirCacheMu.Lock()
	if e, ok := dirCache[key]; ok && time.Since(e.fetched) < cacheTTL {
		dirCacheMu.Unlock()
		return e.resp, nil
	}
	dirCacheMu.Unlock()

	resp, err := fileShareRequest(machine, fsRequest{Op: "list", Path: path})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	dirCacheMu.Lock()
	dirCache[key] = &cacheEntry{resp: resp, fetched: time.Now()}
	dirCacheMu.Unlock()
	return resp, nil
}

// invalidateCache removes cached entries for a machine/path after a mutation.
func invalidateCache(machine, path string) {
	dirCacheMu.Lock()
	defer dirCacheMu.Unlock()
	// Invalidate this path and its parent
	delete(dirCache, machine+":"+path)
	parent := filepath.Dir(path)
	if parent == "." {
		parent = ""
	}
	delete(dirCache, machine+":"+parent)
}

func main() {
	// Check for service subcommand or Windows SCM launch before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "service" {
		handleServiceCommand()
		return
	}
	if service.IsWindowsService() {
		runAsWindowsService()
		return
	}

	// Check for version subcommand before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Printf("telafs %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}

	runServer(0)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `telafs -- Mount Tela file shares as a local drive via WebDAV

Usage:
  telafs [options]          Start the WebDAV server
  telafs version            Print version and exit
  telafs service <command>  Manage telafs as an OS service

Options:
  -port <port>   Listen port (default 18080, env TELAFS_PORT)

Service commands:
  install   Install as an OS service
  uninstall Remove the service
  start     Start the installed service
  stop      Stop the running service
  restart   Restart the service
  status    Show service status

Drive mapping (after starting):
  net use T: http://localhost:18080/
`)
}

// runServer starts the WebDAV server. If port is 0, it parses flags and env.
func runServer(port int) {
	log.SetFlags(log.Ltime)
	log.SetPrefix("[telafs] ")

	if port == 0 {
		p := flag.Int("port", 18080, "WebDAV listen port")
		flag.Parse()
		port = *p
		if v := os.Getenv("TELAFS_PORT"); v != "" {
			fmt.Sscanf(v, "%d", &port)
		}
	}

	base, token, err := loadControlEndpoint()
	if err != nil {
		log.Fatalf("No running tela instance: %v", err)
	}
	controlBase = base
	controlToken = token

	log.Printf("connected to tela at %s", base)

	handler := &webdav.Handler{
		FileSystem: &telaFS{},
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("%s %s -> %v", r.Method, r.URL.Path, err)
			}
		},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("WebDAV server listening on %s", addr)
	log.Printf("Map a drive: net use T: http://%s/", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// ── Control API helpers ──────────────────────────────────────────

func telaConfigDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela")
}

func loadControlEndpoint() (string, string, error) {
	controlPath := filepath.Join(telaConfigDir(), "run", "control.json")
	data, err := os.ReadFile(controlPath)
	if err != nil {
		return "", "", fmt.Errorf("no control file: %w", err)
	}
	var info struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &info) != nil || info.Port == 0 {
		return "", "", fmt.Errorf("invalid control file")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", info.Port), info.Token, nil
}

// fileShareRequest sends a file operation to the control API and returns the response.
func fileShareRequest(machine string, op interface{}) (*fsResponse, error) {
	reqBody, _ := json.Marshal(op)
	reqBody = append(reqBody, '\n')

	url := controlBase + "/files/" + machine
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+controlToken)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		var errResp struct{ Error string `json:"error"` }
		json.Unmarshal(body, &errResp)
		if errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Response may be multiline (JSON header + chunk data). Take first line.
	lines := strings.SplitN(string(body), "\n", 2)
	var fsResp fsResponse
	if err := json.Unmarshal([]byte(lines[0]), &fsResp); err != nil {
		return nil, err
	}
	return &fsResp, nil
}

// listMachines returns the machines with file share from the control API.
func listMachines() ([]string, error) {
	url := controlBase + "/services"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+controlToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var services []struct {
		Machine string `json:"machine"`
		Name    string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&services)

	seen := map[string]bool{}
	var machines []string
	for _, s := range services {
		if !seen[s.Machine] {
			seen[s.Machine] = true
			machines = append(machines, s.Machine)
		}
	}
	return machines, nil
}

// ── Protocol types ───────────────────────────────────────────────

type fsRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path,omitempty"`
	NewName string `json:"newName,omitempty"`
	NewPath string `json:"newPath,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

type fsResponse struct {
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Entries []fsEntry `json:"entries,omitempty"`
	Size    int64     `json:"size,omitempty"`
}

type fsEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// ── WebDAV FileSystem implementation ─────────────────────────────

type telaFS struct{}

// parsePath splits a WebDAV path into machine and remote path.
// "/" returns ("", "")
// "/barn" returns ("barn", "")
// "/barn/docs/file.txt" returns ("barn", "docs/file.txt")
func parsePath(name string) (machine, remotePath string) {
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", ""
	}
	parts := strings.SplitN(name, "/", 2)
	machine = parts[0]
	if len(parts) > 1 {
		remotePath = parts[1]
	}
	return
}

func (fs *telaFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	machine, remotePath := parsePath(name)
	if machine == "" || remotePath == "" {
		return os.ErrPermission
	}
	resp, err := fileShareRequest(machine, fsRequest{Op: "mkdir", Path: remotePath})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	invalidateCache(machine, remotePath)
	return nil
}

func (fs *telaFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	machine, remotePath := parsePath(name)

	// Root: list machines
	if machine == "" {
		return &rootDir{}, nil
	}

	// Machine root or subdirectory
	if remotePath == "" {
		return &machineDir{machine: machine, path: ""}, nil
	}

	// Check if it's a directory by listing (cached)
	if _, err := cachedList(machine, remotePath); err == nil {
		return &machineDir{machine: machine, path: remotePath}, nil
	}

	// It's a file (or doesn't exist)
	return &telaFile{machine: machine, path: remotePath, flag: flag}, nil
}

func (fs *telaFS) RemoveAll(ctx context.Context, name string) error {
	machine, remotePath := parsePath(name)
	if machine == "" || remotePath == "" {
		return os.ErrPermission
	}
	resp, err := fileShareRequest(machine, fsRequest{Op: "delete", Path: remotePath})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	invalidateCache(machine, remotePath)
	return nil
}

func (fs *telaFS) Rename(ctx context.Context, oldName, newName string) error {
	oldMachine, oldPath := parsePath(oldName)
	newMachine, newPath := parsePath(newName)
	if oldMachine == "" || oldPath == "" || newMachine == "" || newPath == "" {
		return os.ErrPermission
	}
	if oldMachine != newMachine {
		return fmt.Errorf("cannot move between machines")
	}

	// Check if it's a rename (same dir) or a move (different dir)
	oldDir := filepath.Dir(oldPath)
	newDir := filepath.Dir(newPath)
	if oldDir == newDir {
		newBaseName := filepath.Base(newPath)
		resp, err := fileShareRequest(oldMachine, fsRequest{Op: "rename", Path: oldPath, NewName: newBaseName})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
	} else {
		resp, err := fileShareRequest(oldMachine, fsRequest{Op: "move", Path: oldPath, NewPath: newPath})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
	}
	invalidateCache(oldMachine, oldPath)
	invalidateCache(oldMachine, newPath)
	return nil
}

func (fs *telaFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	machine, remotePath := parsePath(name)

	if machine == "" {
		return &dirInfo{name: "/", modTime: time.Now()}, nil
	}

	if remotePath == "" {
		return &dirInfo{name: machine, modTime: time.Now()}, nil
	}

	// Try listing the parent to find this entry (cached)
	parentPath := filepath.Dir(remotePath)
	if parentPath == "." {
		parentPath = ""
	}
	baseName := filepath.Base(remotePath)

	resp, err := cachedList(machine, parentPath)
	if err != nil {
		return nil, os.ErrNotExist
	}

	for _, e := range resp.Entries {
		if e.Name == baseName {
			modTime := time.Now()
			if e.ModTime != "" {
				if t, err := time.Parse(time.RFC3339, e.ModTime); err == nil {
					modTime = t
				}
			}
			if e.IsDir {
				return &dirInfo{name: e.Name, modTime: modTime}, nil
			}
			return &fileInfo{name: e.Name, size: e.Size, modTime: modTime}, nil
		}
	}
	return nil, os.ErrNotExist
}

// ── Root directory (lists machines) ──────────────────────────────

type rootDir struct {
	entries []os.FileInfo
	pos     int
}

func (d *rootDir) Close() error { return nil }
func (d *rootDir) Write(p []byte) (int, error) { return 0, os.ErrPermission }
func (d *rootDir) Read(p []byte) (int, error) { return 0, io.EOF }
func (d *rootDir) Seek(offset int64, whence int) (int64, error) { return 0, nil }

func (d *rootDir) Stat() (os.FileInfo, error) {
	return &dirInfo{name: "/", modTime: time.Now()}, nil
}

func (d *rootDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.entries == nil {
		machines, _ := listMachines()
		for _, m := range machines {
			d.entries = append(d.entries, &dirInfo{name: m, modTime: time.Now()})
		}
	}
	if d.pos >= len(d.entries) {
		return nil, io.EOF
	}
	if count <= 0 {
		result := d.entries[d.pos:]
		d.pos = len(d.entries)
		return result, nil
	}
	end := d.pos + count
	if end > len(d.entries) {
		end = len(d.entries)
	}
	result := d.entries[d.pos:end]
	d.pos = end
	if d.pos >= len(d.entries) {
		return result, io.EOF
	}
	return result, nil
}

// ── Machine directory ────────────────────────────────────────────

type machineDir struct {
	machine string
	path    string
	entries []os.FileInfo
	pos     int
}

func (d *machineDir) Close() error { return nil }
func (d *machineDir) Write(p []byte) (int, error) { return 0, os.ErrPermission }
func (d *machineDir) Read(p []byte) (int, error) { return 0, io.EOF }
func (d *machineDir) Seek(offset int64, whence int) (int64, error) { return 0, nil }

func (d *machineDir) Stat() (os.FileInfo, error) {
	name := d.machine
	if d.path != "" {
		name = filepath.Base(d.path)
	}
	return &dirInfo{name: name, modTime: time.Now()}, nil
}

func (d *machineDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.entries == nil {
		resp, err := cachedList(d.machine, d.path)
		if err != nil {
			return nil, err
		}
		for _, e := range resp.Entries {
			modTime := time.Now()
			if e.ModTime != "" {
				if t, err := time.Parse(time.RFC3339, e.ModTime); err == nil {
					modTime = t
				}
			}
			if e.IsDir {
				d.entries = append(d.entries, &dirInfo{name: e.Name, modTime: modTime})
			} else {
				d.entries = append(d.entries, &fileInfo{name: e.Name, size: e.Size, modTime: modTime})
			}
		}
	}
	if d.pos >= len(d.entries) {
		return nil, io.EOF
	}
	if count <= 0 {
		result := d.entries[d.pos:]
		d.pos = len(d.entries)
		return result, nil
	}
	end := d.pos + count
	if end > len(d.entries) {
		end = len(d.entries)
	}
	result := d.entries[d.pos:end]
	d.pos = end
	if d.pos >= len(d.entries) {
		return result, io.EOF
	}
	return result, nil
}

// ── File (read-only for now) ─────────────────────────────────────

type telaFile struct {
	machine string
	path    string
	flag    int
}

func (f *telaFile) Close() error                                   { return nil }
func (f *telaFile) Write(p []byte) (int, error)                    { return 0, os.ErrPermission }
func (f *telaFile) Read(p []byte) (int, error)                     { return 0, io.EOF }
func (f *telaFile) Seek(offset int64, whence int) (int64, error)   { return 0, nil }
func (f *telaFile) Readdir(count int) ([]os.FileInfo, error)       { return nil, os.ErrInvalid }

func (f *telaFile) Stat() (os.FileInfo, error) {
	// Need to get file info from parent listing (cached)
	parentPath := filepath.Dir(f.path)
	if parentPath == "." {
		parentPath = ""
	}
	baseName := filepath.Base(f.path)

	resp, err := cachedList(f.machine, parentPath)
	if err != nil {
		return nil, os.ErrNotExist
	}

	for _, e := range resp.Entries {
		if e.Name == baseName {
			modTime := time.Now()
			if e.ModTime != "" {
				if t, err := time.Parse(time.RFC3339, e.ModTime); err == nil {
					modTime = t
				}
			}
			return &fileInfo{name: e.Name, size: e.Size, modTime: modTime}, nil
		}
	}
	return nil, os.ErrNotExist
}

// ── FileInfo implementations ─────────────────────────────────────

type dirInfo struct {
	name    string
	modTime time.Time
}

func (i *dirInfo) Name() string      { return i.name }
func (i *dirInfo) Size() int64       { return 0 }
func (i *dirInfo) Mode() os.FileMode { return os.ModeDir | 0755 }
func (i *dirInfo) ModTime() time.Time { return i.modTime }
func (i *dirInfo) IsDir() bool       { return true }
func (i *dirInfo) Sys() interface{}  { return nil }

type fileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (i *fileInfo) Name() string      { return i.name }
func (i *fileInfo) Size() int64       { return i.size }
func (i *fileInfo) Mode() os.FileMode { return 0644 }
func (i *fileInfo) ModTime() time.Time { return i.modTime }
func (i *fileInfo) IsDir() bool       { return false }
func (i *fileInfo) Sys() interface{}  { return nil }

// ── Service management ───────────────────────────────────────────

// telafsConfig is the YAML configuration for telafs service mode.
type telafsConfig struct {
	Port int `yaml:"port"`
}

func handleServiceCommand() {
	if len(os.Args) < 3 {
		cfgPath := service.BinaryConfigPath("telafs")
		fmt.Fprintf(os.Stderr, `telafs service -- manage telafs as an OS service

Usage:
  telafs service install [-port <port>]  Install service
  telafs service uninstall               Remove the service
  telafs service start                   Start the installed service
  telafs service stop                    Stop the running service
  telafs service restart                 Restart the service
  telafs service status                  Show service status
  telafs service run                     Run in service mode (used by the service manager)

The service reads its configuration from:
  %s

Edit that file and run "telafs service restart" to reconfigure.

Install example:
  telafs service install -port 18080
`, cfgPath)
		os.Exit(1)
	}

	subcmd := os.Args[2]

	switch subcmd {
	case "install":
		svcInstall()
	case "uninstall":
		svcUninstall()
	case "start":
		svcStart()
	case "stop":
		svcStop()
	case "restart":
		svcRestart()
	case "status":
		svcStatus()
	case "run":
		if service.IsWindowsService() {
			runAsWindowsService()
		} else {
			svcRun()
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service subcommand: %s\n", subcmd)
		os.Exit(1)
	}
}

func svcInstall() {
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	port := fs.Int("port", 18080, "WebDAV listen port")
	fs.Parse(os.Args[3:])

	if v := os.Getenv("TELAFS_PORT"); v != "" {
		fmt.Sscanf(v, "%d", port)
	}

	yamlContent := fmt.Sprintf("port: %d\n", *port)

	// Write config to system directory
	destPath := service.BinaryConfigPath("telafs")
	if err := os.MkdirAll(filepath.Dir(destPath), service.ConfigDirPerm()); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(destPath, []byte(yamlContent), service.ConfigFilePerm()); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		os.Exit(1)
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.Abs(exePath)

	cfg := &service.Config{
		BinaryPath:  exePath,
		Description: "Tela File System -- WebDAV server for Tela file shares",
		YAMLConfig:  service.EncodeYAMLConfig(yamlContent),
	}

	if err := service.Install("telafs", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("telafs service installed successfully")
	fmt.Printf("  config: %s\n", destPath)
	fmt.Printf("  port:   %d\n", *port)
	fmt.Println("  start:  telafs service start")
}

func svcUninstall() {
	if err := service.Uninstall("telafs"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telafs service uninstalled")
	fmt.Printf("  config retained: %s\n", service.BinaryConfigPath("telafs"))
}

func svcStart() {
	if err := service.Start("telafs"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telafs service started")
}

func svcStop() {
	if err := service.Stop("telafs"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telafs service stopped")
}

func svcRestart() {
	fmt.Println("stopping telafs service...")
	_ = service.Stop("telafs")
	time.Sleep(time.Second)
	if err := service.Start("telafs"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("telafs service restarted")
}

func svcStatus() {
	st, err := service.QueryStatus("telafs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed: %v\n", st.Installed)
	fmt.Printf("running:   %v\n", st.Running)
	fmt.Printf("status:    %s\n", st.Info)
	if st.Installed {
		fmt.Printf("config:    %s\n", service.BinaryConfigPath("telafs"))
	}
}

// serviceRunDaemon loads config from service metadata and runs the WebDAV server.
// It blocks until svcStop is closed.
func serviceRunDaemon(svcStop <-chan struct{}) {
	log.SetFlags(log.Ltime)
	log.SetPrefix("[telafs] ")

	if runtime.GOOS == "windows" && service.IsWindowsService() {
		logPath := service.LogPath("telafs")
		lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, service.ConfigFilePerm())
		if err == nil {
			log.SetOutput(lf)
			os.Stderr = lf
		}
	}

	// Load port from config
	port := 18080
	yamlPath := service.BinaryConfigPath("telafs")
	if data, err := os.ReadFile(yamlPath); err == nil {
		var cfg telafsConfig
		// Simple YAML parse for port field
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "port:") {
				fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "port:")), "%d", &port)
			}
		}
		_ = cfg
	}

	go runServer(port)
	<-svcStop
	log.Println("service stopping")
}

func svcRun() {
	svcStop := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		close(svcStop)
	}()
	serviceRunDaemon(svcStop)
}

func runAsWindowsService() {
	handler := &service.Handler{
		Run: func(svcStopCh <-chan struct{}) {
			serviceRunDaemon(svcStopCh)
		},
	}
	if err := service.RunAsService("telafs", handler); err != nil {
		log.Fatalf("service failed: %v", err)
	}
}
