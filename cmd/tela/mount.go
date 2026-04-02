package main

import (
	"bufio"
	"bytes"
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
	"strconv"
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

const mountPageSize = 50

func mountCachedList(machine, path string) (*mountFsResponse, error) {
	key := machine + ":" + path
	mountDirCacheMu.Lock()
	if e, ok := mountDirCache[key]; ok && time.Since(e.fetched) < mountCacheTTL {
		mountDirCacheMu.Unlock()
		return e.resp, nil
	}
	mountDirCacheMu.Unlock()

	// Fetch in pages to keep individual responses small over the tunnel
	var allEntries []mountFsEntry
	offset := 0
	for {
		resp, err := mountFileShareRequest(machine, mountFsRequest{
			Op: "list", Path: path, Offset: offset, Limit: mountPageSize,
		})
		if err != nil {
			return nil, err
		}
		if !resp.OK {
			return nil, fmt.Errorf("%s", resp.Error)
		}
		allEntries = append(allEntries, resp.Entries...)
		if resp.Total == 0 || offset+len(resp.Entries) >= resp.Total {
			break
		}
		offset += len(resp.Entries)
	}

	combined := &mountFsResponse{OK: true, Entries: allEntries, Total: len(allEntries)}
	mountDirCacheMu.Lock()
	mountDirCache[key] = &mountCacheEntry{resp: combined, fetched: time.Now()}
	mountDirCacheMu.Unlock()
	return combined, nil
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
	Offset  int    `json:"offset,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type mountFsResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Entries []mountFsEntry `json:"entries,omitempty"`
	Size    int64          `json:"size,omitempty"`
	Total   int            `json:"total,omitempty"`
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

	// Retry with backoff when the tunnel is temporarily down (e.g., during
	// a reconnect cycle after network change or laptop wake from sleep).
	// The tela connect process has its own reconnect loop; we just need to
	// wait long enough for it to re-establish the session.
	delays := []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}

		url := mountControlBase + "/files/" + machine
		req, _ := http.NewRequest("POST", url, strings.NewReader(string(reqBody)))
		req.Header.Set("Authorization", "Bearer "+mountControlToken)
		req.Header.Set("Content-Type", "application/json")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		req = req.WithContext(ctx)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue // control API unreachable, retry
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if resp.StatusCode == http.StatusServiceUnavailable {
			// 503 means tunnel is down but control API is alive.
			// This is the reconnect window; keep retrying.
			lastErr = fmt.Errorf("tunnel unavailable (reconnecting)")
			continue
		}

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
	return nil, lastErr
}

// mountDownloadToFile streams file content from a remote machine into dst
// using the CHUNK protocol. The caller is responsible for seeking dst back
// to the start and closing it.
func mountDownloadToFile(machine, path string, dst *os.File) error {
	reqBody, _ := json.Marshal(map[string]string{"op": "read", "path": path})
	reqBody = append(reqBody, '\n')

	url := mountControlBase + "/files/" + machine
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer "+mountControlToken)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct{ Error string `json:"error"` }
		json.Unmarshal(body, &errResp)
		if errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	// Read and discard the JSON header line (fsResponse)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	// Read CHUNK protocol: "CHUNK <size>\n<data>" repeated, "CHUNK 0\n" to end
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read chunk header: %w", err)
		}
		line = strings.TrimRight(line, "\n\r")

		if !strings.HasPrefix(line, "CHUNK ") {
			return fmt.Errorf("protocol error: expected CHUNK, got: %s", line)
		}

		chunkSize, err := strconv.ParseInt(strings.TrimPrefix(line, "CHUNK "), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid chunk size: %w", err)
		}
		if chunkSize == 0 {
			break
		}

		if _, err := io.CopyN(dst, reader, chunkSize); err != nil {
			return fmt.Errorf("read chunk data: %w", err)
		}
	}

	return nil
}

// mountUploadFile writes file content to a remote machine using the control
// API's file share proxy. It sends the data using the CHUNK protocol.
func mountUploadFile(machine, path string, data []byte) error {
	// Build the CHUNK-encoded body: JSON header line + CHUNK <size>\n<data> + CHUNK 0\n
	var buf bytes.Buffer
	header, _ := json.Marshal(map[string]interface{}{"op": "write", "path": path, "size": len(data)})
	buf.Write(header)
	buf.WriteByte('\n')
	fmt.Fprintf(&buf, "CHUNK %d\n", len(data))
	buf.Write(data)
	fmt.Fprintf(&buf, "CHUNK 0\n")

	url := mountControlBase + "/files/" + machine
	req, _ := http.NewRequest("POST", url, &buf)
	req.Header.Set("Authorization", "Bearer "+mountControlToken)
	req.Header.Set("Content-Type", "application/octet-stream")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var errResp struct{ Error string `json:"error"` }
		json.Unmarshal(body, &errResp)
		if errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Parse the JSON response to check for errors
	lines := strings.SplitN(string(body), "\n", 2)
	var fsResp mountFsResponse
	if err := json.Unmarshal([]byte(lines[0]), &fsResp); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if !fsResp.OK {
		return fmt.Errorf("%s", fsResp.Error)
	}
	return nil
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

	// Collect unique machines, then probe each for file share support
	seen := map[string]bool{}
	var candidates []string
	for _, s := range services {
		if !seen[s.Machine] {
			seen[s.Machine] = true
			candidates = append(candidates, s.Machine)
		}
	}

	var machines []string
	for _, m := range candidates {
		// A successful list request means file sharing is enabled
		_, err := mountFileShareRequest(m, mountFsRequest{Op: "list", Path: ""})
		if err == nil {
			machines = append(machines, m)
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
			} else {
				log.Printf("%s %s -> ok", r.Method, r.URL.Path)
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

	// Validate and perform OS mount if requested
	if *mountPoint != "" {
		if err := validateMountPoint(*mountPoint); err != nil {
			log.Fatalf("invalid mount point %q: %v", *mountPoint, err)
		}
		if err := platformMount(*mountPoint, addr); err != nil {
			log.Fatalf("mount failed: %v", err)
		}
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
	log.Printf("[mount] OpenFile: name=%q flag=%d", name, flag)
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

	writable := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0
	return &mountTelaFile{machine: machine, path: remotePath, flag: flag, writable: writable}, nil
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

func (d *mountRootDir) Close() error                                 { return nil }
func (d *mountRootDir) Write(p []byte) (int, error)                  { return 0, os.ErrPermission }
func (d *mountRootDir) Read(p []byte) (int, error)                   { return 0, io.EOF }
func (d *mountRootDir) Seek(offset int64, whence int) (int64, error) { return 0, nil }
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

func (d *mountMachineDir) Close() error                                 { return nil }
func (d *mountMachineDir) Write(p []byte) (int, error)                  { return 0, os.ErrPermission }
func (d *mountMachineDir) Read(p []byte) (int, error)                   { return 0, io.EOF }
func (d *mountMachineDir) Seek(offset int64, whence int) (int64, error) { return 0, nil }

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
	machine  string
	path     string
	flag     int
	tmpFile  *os.File // lazy-loaded content spooled to a temp file (reads)
	loaded   bool
	writable bool     // true if opened for writing
	writeTmp *os.File // temp file for buffered writes
}

func (f *mountTelaFile) Close() error {
	// Upload buffered writes
	if f.writable && f.writeTmp != nil {
		size, _ := f.writeTmp.Seek(0, io.SeekEnd)
		if size > 0 {
			f.writeTmp.Seek(0, io.SeekStart)
			data, _ := io.ReadAll(f.writeTmp)
			if err := mountUploadFile(f.machine, f.path, data); err != nil {
				log.Printf("[mount] upload %s/%s failed: %v", f.machine, f.path, err)
				f.writeTmp.Close()
				os.Remove(f.writeTmp.Name())
				return err
			}
		}
		f.writeTmp.Close()
		os.Remove(f.writeTmp.Name())
		mountInvalidateCache(f.machine, f.path)
	}
	// Clean up read temp file
	if f.tmpFile != nil {
		name := f.tmpFile.Name()
		f.tmpFile.Close()
		os.Remove(name)
	}
	return nil
}

func (f *mountTelaFile) Write(p []byte) (int, error) {
	if !f.writable {
		return 0, os.ErrPermission
	}
	if f.writeTmp == nil {
		tmp, err := os.CreateTemp("", "tela-mount-write-*")
		if err != nil {
			return 0, err
		}
		f.writeTmp = tmp
	}
	return f.writeTmp.Write(p)
}

func (f *mountTelaFile) Read(p []byte) (int, error) {
	if err := f.ensureLoaded(); err != nil {
		log.Printf("[mount] Read %s/%s: ensureLoaded error: %v", f.machine, f.path, err)
		return 0, err
	}
	n, err := f.tmpFile.Read(p)
	if err != nil && err != io.EOF {
		log.Printf("[mount] Read %s/%s: read error: %v", f.machine, f.path, err)
	}
	return n, err
}

func (f *mountTelaFile) Seek(offset int64, whence int) (int64, error) {
	if f.writable {
		return 0, nil
	}
	if err := f.ensureLoaded(); err != nil {
		log.Printf("[mount] Seek %s/%s: ensureLoaded error: %v", f.machine, f.path, err)
		return 0, err
	}
	pos, err := f.tmpFile.Seek(offset, whence)
	log.Printf("[mount] Seek %s/%s: offset=%d whence=%d -> pos=%d err=%v", f.machine, f.path, offset, whence, pos, err)
	return pos, err
}

func (f *mountTelaFile) Readdir(count int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

// ensureLoaded downloads the file content to a temp file on first read access.
// The temp file is cleaned up in Close().
func (f *mountTelaFile) ensureLoaded() error {
	if f.loaded {
		return nil
	}
	f.loaded = true

	log.Printf("[mount] ensureLoaded: downloading %s/%s", f.machine, f.path)

	tmp, err := os.CreateTemp("", "tela-mount-read-*")
	if err != nil {
		log.Printf("[mount] ensureLoaded: temp file create failed: %v", err)
		return err
	}

	if err := mountDownloadToFile(f.machine, f.path, tmp); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		log.Printf("[mount] download %s/%s failed: %v", f.machine, f.path, err)
		return err
	}

	size, _ := tmp.Seek(0, io.SeekEnd)
	log.Printf("[mount] ensureLoaded: %s/%s downloaded %d bytes to %s", f.machine, f.path, size, tmp.Name())
	tmp.Seek(0, io.SeekStart)
	f.tmpFile = tmp
	return nil
}

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

func (i *mountDirInfo) Name() string       { return i.name }
func (i *mountDirInfo) Size() int64        { return 0 }
func (i *mountDirInfo) Mode() os.FileMode  { return os.ModeDir | 0755 }
func (i *mountDirInfo) ModTime() time.Time { return i.modTime }
func (i *mountDirInfo) IsDir() bool        { return true }
func (i *mountDirInfo) Sys() interface{}   { return nil }

type mountFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (i *mountFileInfo) Name() string       { return i.name }
func (i *mountFileInfo) Size() int64        { return i.size }
func (i *mountFileInfo) Mode() os.FileMode  { return 0644 }
func (i *mountFileInfo) ModTime() time.Time { return i.modTime }
func (i *mountFileInfo) IsDir() bool        { return false }
func (i *mountFileInfo) Sys() interface{}   { return nil }

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
