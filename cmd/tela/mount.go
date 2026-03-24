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
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/webdav"
)

// ── Mount state ──────────────────────────────────────────────────

var (
	mountControlBase  string
	mountControlToken string
)

// ── Directory listing cache ──────────────────────────────────────

const mountCacheTTL = 3 * time.Second

type mountCacheEntry struct {
	resp    *mountFsResponse
	fetched time.Time
}

var (
	mountDirCache   = map[string]*mountCacheEntry{}
	mountDirCacheMu sync.Mutex
)

func mountCachedList(machine, path string) (*mountFsResponse, error) {
	key := machine + ":" + path
	mountDirCacheMu.Lock()
	if e, ok := mountDirCache[key]; ok && time.Since(e.fetched) < mountCacheTTL {
		mountDirCacheMu.Unlock()
		return e.resp, nil
	}
	mountDirCacheMu.Unlock()

	resp, err := mountFileShareRequest(machine, mountFsRequest{Op: "list", Path: path})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	mountDirCacheMu.Lock()
	mountDirCache[key] = &mountCacheEntry{resp: resp, fetched: time.Now()}
	mountDirCacheMu.Unlock()
	return resp, nil
}

func mountInvalidateCache(machine, path string) {
	mountDirCacheMu.Lock()
	defer mountDirCacheMu.Unlock()
	delete(mountDirCache, machine+":"+path)
	parent := filepath.Dir(path)
	if parent == "." {
		parent = ""
	}
	delete(mountDirCache, machine+":"+parent)
}

// ── Protocol types ───────────────────────────────────────────────

type mountFsRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path,omitempty"`
	NewName string `json:"newName,omitempty"`
	NewPath string `json:"newPath,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

type mountFsResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Entries []mountFsEntry `json:"entries,omitempty"`
	Size    int64          `json:"size,omitempty"`
}

type mountFsEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// ── Control API helpers ──────────────────────────────────────────

func mountLoadControlEndpoint() (string, string, error) {
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

func mountFileShareRequest(machine string, op interface{}) (*mountFsResponse, error) {
	reqBody, _ := json.Marshal(op)
	reqBody = append(reqBody, '\n')

	url := mountControlBase + "/files/" + machine
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+mountControlToken)
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

	lines := strings.SplitN(string(body), "\n", 2)
	var fsResp mountFsResponse
	if err := json.Unmarshal([]byte(lines[0]), &fsResp); err != nil {
		return nil, err
	}
	return &fsResp, nil
}

func mountListMachines() ([]string, error) {
	url := mountControlBase + "/services"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+mountControlToken)
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

// ── Command entry point ──────────────────────────────────────────

func cmdMount(args []string) {
	fs := flag.NewFlagSet("mount", flag.ExitOnError)
	port := fs.Int("port", 18080, "WebDAV listen port")
	mountPoint := fs.String("mount", "", "Drive letter (Windows: T:) or directory path to mount")
	fs.Parse(args)

	if v := os.Getenv("TELA_MOUNT_PORT"); v != "" {
		fmt.Sscanf(v, "%d", port)
	}

	log.SetFlags(log.Ltime)
	log.SetPrefix("[mount] ")

	base, token, err := mountLoadControlEndpoint()
	if err != nil {
		log.Fatalf("No running tela instance: %v", err)
	}
	mountControlBase = base
	mountControlToken = token
	log.Printf("connected to tela at %s", base)

	// Start WebDAV server
	handler := &webdav.Handler{
		FileSystem: &mountFS{},
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("%s %s -> %v", r.Method, r.URL.Path, err)
			}
		},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *port)

	go func() {
		log.Printf("WebDAV server listening on %s", addr)
		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("WebDAV server: %v", err)
		}
	}()

	// Perform OS mount if requested
	var mountCmd *exec.Cmd
	if *mountPoint != "" {
		mountCmd = platformMount(*mountPoint, addr)
	} else {
		log.Printf("No -mount specified. Map manually:")
		if runtime.GOOS == "windows" {
			log.Printf("  net use T: http://%s/", addr)
		} else {
			log.Printf("  mount -t davfs http://%s/ /mnt/tela", addr)
		}
	}

	// Wait for signal, then clean up
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("shutting down...")

	if *mountPoint != "" {
		platformUnmount(*mountPoint)
	}
	_ = mountCmd
}

// ── Platform mount/unmount ───────────────────────────────────────

func platformMount(mountArg, addr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return platformMountWindows(mountArg, addr)
	}
	return platformMountUnix(mountArg, addr)
}

func platformMountWindows(mountArg, addr string) *exec.Cmd {
	vol := filepath.VolumeName(mountArg)

	if vol != "" && vol == mountArg {
		// Drive letter mapping: "T:" exactly
		log.Printf("mapping drive %s", mountArg)
		cmd := exec.Command("net", "use", mountArg, "http://"+addr+"/")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Printf("net use failed: %v", err)
			log.Printf("Ensure the WebClient service is running: sc start WebClient")
		} else {
			log.Printf("drive %s mapped", mountArg)
		}
		return cmd
	}

	// Directory mount point
	absPath, err := filepath.Abs(mountArg)
	if err != nil {
		log.Printf("invalid mount path: %v", err)
		return nil
	}
	log.Printf("mounting to %s", absPath)
	uncPath := fmt.Sprintf("\\\\localhost@%s\\DavWWWRoot", strings.Split(addr, ":")[1])
	cmd := exec.Command("mklink", "/d", absPath, uncPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("mklink failed: %v (you may need to run as Administrator)", err)
	} else {
		log.Printf("mounted at %s", absPath)
	}
	return cmd
}

func platformMountUnix(mountArg, addr string) *exec.Cmd {
	absPath, err := filepath.Abs(mountArg)
	if err != nil {
		log.Printf("invalid mount path: %v", err)
		return nil
	}

	// Create mount point if needed
	if err := os.MkdirAll(absPath, 0755); err != nil {
		log.Printf("cannot create mount point: %v", err)
		return nil
	}

	davURL := "http://" + addr + "/"

	if runtime.GOOS == "darwin" {
		log.Printf("mounting to %s", absPath)
		cmd := exec.Command("mount_webdav", davURL, absPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Printf("mount_webdav failed: %v", err)
		} else {
			log.Printf("mounted at %s", absPath)
		}
		return cmd
	}

	// Linux: try gio mount first (no root needed), fall back to mount -t davfs
	log.Printf("mounting to %s", absPath)
	gioPath, _ := exec.LookPath("gio")
	if gioPath != "" {
		cmd := exec.Command("gio", "mount", "dav://"+addr+"/")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			log.Printf("mounted via gio at dav://%s/", addr)
			return cmd
		}
		log.Printf("gio mount failed, trying mount -t davfs")
	}

	cmd := exec.Command("mount", "-t", "davfs", davURL, absPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("mount failed: %v (you may need root or davfs2 installed)", err)
	} else {
		log.Printf("mounted at %s", absPath)
	}
	return cmd
}

func platformUnmount(mountArg string) {
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(mountArg)
		if vol != "" && vol == mountArg {
			log.Printf("unmapping drive %s", mountArg)
			cmd := exec.Command("net", "use", mountArg, "/delete", "/y")
			cmd.Run()
			return
		}
		// Directory symlink: remove it
		absPath, _ := filepath.Abs(mountArg)
		log.Printf("removing mount point %s", absPath)
		os.Remove(absPath)
		return
	}

	absPath, _ := filepath.Abs(mountArg)
	if runtime.GOOS == "darwin" {
		log.Printf("unmounting %s", absPath)
		exec.Command("umount", absPath).Run()
	} else {
		log.Printf("unmounting %s", absPath)
		exec.Command("umount", absPath).Run()
	}
}

// ── WebDAV FileSystem implementation ─────────────────────────────

type mountFS struct{}

func mountParsePath(name string) (machine, remotePath string) {
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

func (fs *mountFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	machine, remotePath := mountParsePath(name)
	if machine == "" || remotePath == "" {
		return os.ErrPermission
	}
	resp, err := mountFileShareRequest(machine, mountFsRequest{Op: "mkdir", Path: remotePath})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	mountInvalidateCache(machine, remotePath)
	return nil
}

func (fs *mountFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	machine, remotePath := mountParsePath(name)

	if machine == "" {
		return &mountRootDir{}, nil
	}

	if remotePath == "" {
		return &mountMachineDir{machine: machine, path: ""}, nil
	}

	if _, err := mountCachedList(machine, remotePath); err == nil {
		return &mountMachineDir{machine: machine, path: remotePath}, nil
	}

	return &mountTelaFile{machine: machine, path: remotePath, flag: flag}, nil
}

func (fs *mountFS) RemoveAll(ctx context.Context, name string) error {
	machine, remotePath := mountParsePath(name)
	if machine == "" || remotePath == "" {
		return os.ErrPermission
	}
	resp, err := mountFileShareRequest(machine, mountFsRequest{Op: "delete", Path: remotePath})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	mountInvalidateCache(machine, remotePath)
	return nil
}

func (fs *mountFS) Rename(ctx context.Context, oldName, newName string) error {
	oldMachine, oldPath := mountParsePath(oldName)
	newMachine, newPath := mountParsePath(newName)
	if oldMachine == "" || oldPath == "" || newMachine == "" || newPath == "" {
		return os.ErrPermission
	}
	if oldMachine != newMachine {
		return fmt.Errorf("cannot move between machines")
	}

	oldDir := filepath.Dir(oldPath)
	newDir := filepath.Dir(newPath)
	if oldDir == newDir {
		newBaseName := filepath.Base(newPath)
		resp, err := mountFileShareRequest(oldMachine, mountFsRequest{Op: "rename", Path: oldPath, NewName: newBaseName})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
	} else {
		resp, err := mountFileShareRequest(oldMachine, mountFsRequest{Op: "move", Path: oldPath, NewPath: newPath})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
	}
	mountInvalidateCache(oldMachine, oldPath)
	mountInvalidateCache(oldMachine, newPath)
	return nil
}

func (fs *mountFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	machine, remotePath := mountParsePath(name)

	if machine == "" {
		return &mountDirInfo{name: "/", modTime: time.Now()}, nil
	}

	if remotePath == "" {
		return &mountDirInfo{name: machine, modTime: time.Now()}, nil
	}

	parentPath := filepath.Dir(remotePath)
	if parentPath == "." {
		parentPath = ""
	}
	baseName := filepath.Base(remotePath)

	resp, err := mountCachedList(machine, parentPath)
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
				return &mountDirInfo{name: e.Name, modTime: modTime}, nil
			}
			return &mountFileInfo{name: e.Name, size: e.Size, modTime: modTime}, nil
		}
	}
	return nil, os.ErrNotExist
}

// ── Root directory (lists machines) ──────────────────────────────

type mountRootDir struct {
	entries []os.FileInfo
	pos     int
}

func (d *mountRootDir) Close() error                                   { return nil }
func (d *mountRootDir) Write(p []byte) (int, error)                    { return 0, os.ErrPermission }
func (d *mountRootDir) Read(p []byte) (int, error)                     { return 0, io.EOF }
func (d *mountRootDir) Seek(offset int64, whence int) (int64, error)   { return 0, nil }
func (d *mountRootDir) Stat() (os.FileInfo, error) {
	return &mountDirInfo{name: "/", modTime: time.Now()}, nil
}

func (d *mountRootDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.entries == nil {
		machines, _ := mountListMachines()
		for _, m := range machines {
			d.entries = append(d.entries, &mountDirInfo{name: m, modTime: time.Now()})
		}
	}
	return mountReaddir(&d.entries, &d.pos, count)
}

// ── Machine directory ────────────────────────────────────────────

type mountMachineDir struct {
	machine string
	path    string
	entries []os.FileInfo
	pos     int
}

func (d *mountMachineDir) Close() error                                   { return nil }
func (d *mountMachineDir) Write(p []byte) (int, error)                    { return 0, os.ErrPermission }
func (d *mountMachineDir) Read(p []byte) (int, error)                     { return 0, io.EOF }
func (d *mountMachineDir) Seek(offset int64, whence int) (int64, error)   { return 0, nil }

func (d *mountMachineDir) Stat() (os.FileInfo, error) {
	name := d.machine
	if d.path != "" {
		name = filepath.Base(d.path)
	}
	return &mountDirInfo{name: name, modTime: time.Now()}, nil
}

func (d *mountMachineDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.entries == nil {
		resp, err := mountCachedList(d.machine, d.path)
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
				d.entries = append(d.entries, &mountDirInfo{name: e.Name, modTime: modTime})
			} else {
				d.entries = append(d.entries, &mountFileInfo{name: e.Name, size: e.Size, modTime: modTime})
			}
		}
	}
	return mountReaddir(&d.entries, &d.pos, count)
}

// ── File ─────────────────────────────────────────────────────────

type mountTelaFile struct {
	machine string
	path    string
	flag    int
}

func (f *mountTelaFile) Close() error                                   { return nil }
func (f *mountTelaFile) Write(p []byte) (int, error)                    { return 0, os.ErrPermission }
func (f *mountTelaFile) Read(p []byte) (int, error)                     { return 0, io.EOF }
func (f *mountTelaFile) Seek(offset int64, whence int) (int64, error)   { return 0, nil }
func (f *mountTelaFile) Readdir(count int) ([]os.FileInfo, error)       { return nil, os.ErrInvalid }

func (f *mountTelaFile) Stat() (os.FileInfo, error) {
	parentPath := filepath.Dir(f.path)
	if parentPath == "." {
		parentPath = ""
	}
	baseName := filepath.Base(f.path)

	resp, err := mountCachedList(f.machine, parentPath)
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
			return &mountFileInfo{name: e.Name, size: e.Size, modTime: modTime}, nil
		}
	}
	return nil, os.ErrNotExist
}

// ── FileInfo implementations ─────────────────────────────────────

type mountDirInfo struct {
	name    string
	modTime time.Time
}

func (i *mountDirInfo) Name() string        { return i.name }
func (i *mountDirInfo) Size() int64         { return 0 }
func (i *mountDirInfo) Mode() os.FileMode   { return os.ModeDir | 0755 }
func (i *mountDirInfo) ModTime() time.Time  { return i.modTime }
func (i *mountDirInfo) IsDir() bool         { return true }
func (i *mountDirInfo) Sys() interface{}    { return nil }

type mountFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (i *mountFileInfo) Name() string        { return i.name }
func (i *mountFileInfo) Size() int64         { return i.size }
func (i *mountFileInfo) Mode() os.FileMode   { return 0644 }
func (i *mountFileInfo) ModTime() time.Time  { return i.modTime }
func (i *mountFileInfo) IsDir() bool         { return false }
func (i *mountFileInfo) Sys() interface{}    { return nil }

// ── Shared Readdir helper ────────────────────────────────────────

func mountReaddir(entries *[]os.FileInfo, pos *int, count int) ([]os.FileInfo, error) {
	if *pos >= len(*entries) {
		return nil, io.EOF
	}
	if count <= 0 {
		result := (*entries)[*pos:]
		*pos = len(*entries)
		return result, nil
	}
	end := *pos + count
	if end > len(*entries) {
		end = len(*entries)
	}
	result := (*entries)[*pos:end]
	*pos = end
	if *pos >= len(*entries) {
		return result, io.EOF
	}
	return result, nil
}
