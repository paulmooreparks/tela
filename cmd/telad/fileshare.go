/*
telad file sharing -- sandboxed file transfer over the WireGuard tunnel.

Purpose:

	When enabled in the machine config, telad listens on TCP port 17377
	inside the gVisor netstack. Authorized clients can list, download,
	upload, and delete files within a single declared directory.

	All operations are confined to the sandbox directory. Path traversal,
	symlinks, and system directories are rejected. File sharing is off
	by default and must be explicitly enabled per machine.

Security:

	- Path validation on every operation (no caching).
	- Symlinks are never followed.
	- System directories are rejected at startup.
	- Extension filtering (allowlist and blocklist).
	- Per-file and total size limits enforced before writing.
	- Checksums validated after upload.
	- Connection limit and idle timeout.

Invariants:

	- File sharing must not be enabled without explicit config.
	- No file operation may escape the sandbox directory.
	- The hub never sees file contents (zero-knowledge relay).
*/
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// FileSharePort is the fixed TCP port for file sharing inside the tunnel.
const FileSharePort = 17377

// Maximum concurrent file share connections per session.
const maxFileShareConns = 4

// Idle timeout for a file share connection.
const fileShareIdleTimeout = 60 * time.Second

// Transfer stall timeout: if no chunk data arrives for this long, abort.
const fileShareStallTimeout = 30 * time.Second

// Maximum chunk size for chunked transfer (16 KB).
const maxChunkSize = 16384

// fileShareConfig is the YAML schema for per-machine file sharing.
type fileShareConfig struct {
	Enabled           bool     `yaml:"enabled"`
	Directory         string   `yaml:"directory"`
	Writable          bool     `yaml:"writable,omitempty"`
	MaxFileSize       string   `yaml:"maxFileSize,omitempty"`       // "50MB", "1GB", etc.
	MaxTotalSize      string   `yaml:"maxTotalSize,omitempty"`
	AllowDelete       bool     `yaml:"allowDelete,omitempty"`
	AllowedExtensions []string `yaml:"allowedExtensions,omitempty"`
	BlockedExtensions []string `yaml:"blockedExtensions,omitempty"`
}

// fileShareCapability is advertised in control messages.
type fileShareCapability struct {
	Enabled           bool     `json:"enabled"`
	Writable          bool     `json:"writable"`
	AllowDelete       bool     `json:"allowDelete"`
	MaxFileSize       int64    `json:"maxFileSize"`
	BlockedExtensions []string `json:"blockedExtensions,omitempty"`
	AllowedExtensions []string `json:"allowedExtensions,omitempty"`
}

// parsedFileShareConfig holds validated, ready-to-use file share settings.
type parsedFileShareConfig struct {
	enabled           bool
	directory         string // absolute, cleaned path
	writable          bool
	allowDelete       bool
	maxFileSize       int64
	maxTotalSize      int64
	allowedExtensions map[string]bool // lowercase, with leading dot
	blockedExtensions map[string]bool
}

// buildCapabilities returns a capabilities struct for control messages,
// or nil if no capabilities are active.
func buildCapabilities(fsCfg *parsedFileShareConfig) *capabilities {
	if fsCfg == nil || !fsCfg.enabled {
		return nil
	}
	cap := &fileShareCapability{
		Enabled:     true,
		Writable:    fsCfg.writable,
		AllowDelete: fsCfg.allowDelete,
		MaxFileSize: fsCfg.maxFileSize,
	}
	for ext := range fsCfg.blockedExtensions {
		cap.BlockedExtensions = append(cap.BlockedExtensions, ext)
	}
	for ext := range fsCfg.allowedExtensions {
		cap.AllowedExtensions = append(cap.AllowedExtensions, ext)
	}
	return &capabilities{FileShare: cap}
}

// Default blocked extensions when none are configured.
var defaultBlockedExtensions = []string{".exe", ".bat", ".cmd", ".ps1", ".sh"}

// System directories that must never be used as a share root.
var systemDirs []string

func init() {
	if runtime.GOOS == "windows" {
		systemDirs = []string{
			`C:\`,
			`C:\Windows`,
			`C:\Windows\System32`,
			`C:\Program Files`,
			`C:\Program Files (x86)`,
		}
	} else {
		systemDirs = []string{"/", "/etc", "/bin", "/sbin", "/usr", "/var", "/tmp", "/root"}
	}
}

// parseFileShareConfig validates and normalizes a fileShareConfig.
func parseFileShareConfig(cfg fileShareConfig) (*parsedFileShareConfig, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Directory == "" {
		return nil, fmt.Errorf("fileShare.directory is required when enabled")
	}

	absDir, err := filepath.Abs(cfg.Directory)
	if err != nil {
		return nil, fmt.Errorf("fileShare.directory: %w", err)
	}
	absDir = filepath.Clean(absDir)

	// Reject system directories.
	for _, sysDir := range systemDirs {
		if strings.EqualFold(absDir, sysDir) {
			return nil, fmt.Errorf("fileShare.directory must not be a system directory: %s", absDir)
		}
	}

	p := &parsedFileShareConfig{
		enabled:   true,
		directory: absDir,
		writable:  cfg.Writable,
		allowDelete: cfg.AllowDelete,
	}

	// allowDelete requires writable
	if p.allowDelete && !p.writable {
		return nil, fmt.Errorf("fileShare.allowDelete requires writable: true")
	}

	// Parse size limits.
	if cfg.MaxFileSize != "" {
		n, err := parseSize(cfg.MaxFileSize)
		if err != nil {
			return nil, fmt.Errorf("fileShare.maxFileSize: %w", err)
		}
		p.maxFileSize = n
	} else {
		p.maxFileSize = 50 * 1024 * 1024 // 50MB default
	}

	if cfg.MaxTotalSize != "" {
		n, err := parseSize(cfg.MaxTotalSize)
		if err != nil {
			return nil, fmt.Errorf("fileShare.maxTotalSize: %w", err)
		}
		p.maxTotalSize = n
	}

	// Parse extension filters.
	p.allowedExtensions = make(map[string]bool)
	for _, ext := range cfg.AllowedExtensions {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		p.allowedExtensions[strings.ToLower(ext)] = true
	}

	p.blockedExtensions = make(map[string]bool)
	blocked := cfg.BlockedExtensions
	if len(blocked) == 0 && len(cfg.AllowedExtensions) == 0 {
		blocked = defaultBlockedExtensions
	}
	for _, ext := range blocked {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		p.blockedExtensions[strings.ToLower(ext)] = true
	}

	return p, nil
}

// parseSize parses a human-readable size string like "50MB", "1GB", "100KB".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	var multiplier int64 = 1
	if strings.HasSuffix(s, "GB") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	} else if strings.HasSuffix(s, "MB") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "KB") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size: %q", s)
	}
	return n * multiplier, nil
}

// isExtensionAllowed checks if a filename's extension is permitted.
func (p *parsedFileShareConfig) isExtensionAllowed(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		// No extension: allowed unless allowedExtensions is set (whitelist mode).
		return len(p.allowedExtensions) == 0
	}
	if p.blockedExtensions[ext] {
		return false
	}
	if len(p.allowedExtensions) > 0 {
		return p.allowedExtensions[ext]
	}
	return true
}

// validatePath checks that a relative path is safe and returns the absolute path.
// Returns an error if the path escapes the sandbox, is a symlink, or contains
// dangerous components (Windows UNC, drive letters, alternate data streams).
func (p *parsedFileShareConfig) validatePath(relPath string) (string, error) {
	// Reject obviously dangerous patterns before any filesystem access.
	if strings.Contains(relPath, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(relPath, `\\`) || strings.HasPrefix(relPath, "//") {
			return "", fmt.Errorf("UNC paths not allowed")
		}
		if len(relPath) >= 2 && relPath[1] == ':' {
			return "", fmt.Errorf("drive-letter paths not allowed")
		}
		if strings.Contains(relPath, ":") {
			return "", fmt.Errorf("alternate data streams not allowed")
		}
	}

	cleaned := filepath.Clean(relPath)
	joined := filepath.Join(p.directory, cleaned)

	// Verify the result is still within the sandbox.
	rel, err := filepath.Rel(p.directory, joined)
	if err != nil {
		return "", fmt.Errorf("path validation failed: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes sandbox")
	}

	// Check for symlinks using Lstat (does not follow symlinks).
	info, err := os.Lstat(joined)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlinks not allowed")
	}
	// If the file doesn't exist, that's fine (for write/delete validation
	// the parent directory is what matters). But check parent for symlinks.
	if err != nil && os.IsNotExist(err) {
		parentDir := filepath.Dir(joined)
		parentInfo, parentErr := os.Lstat(parentDir)
		if parentErr == nil && parentInfo.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks not allowed")
		}
	}

	return joined, nil
}

// dirTotalSize returns the total size of all regular files in a directory tree.
func dirTotalSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !info.IsDir() && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// ensureShareDir creates the share directory if it doesn't exist.
func ensureShareDir(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("fileShare.directory exists but is not a directory: %s", dir)
		}
		return nil
	}
	if os.IsNotExist(err) {
		perm := os.FileMode(0700)
		if runtime.GOOS == "windows" {
			perm = 0755
		}
		return os.MkdirAll(dir, perm)
	}
	return err
}

// ── Protocol types ──────────────────────────────────────────────────

type fsRequest struct {
	Op       string `json:"op"`
	Path     string `json:"path"`
	Size     int64  `json:"size,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	NewName  string `json:"newName,omitempty"` // for rename
	NewPath  string `json:"newPath,omitempty"` // for move
}

type fsResponse struct {
	OK       bool        `json:"ok"`
	Error    string      `json:"error,omitempty"`
	Entries  []fsEntry   `json:"entries,omitempty"`
	Size     int64       `json:"size,omitempty"`
	ModTime  string      `json:"modTime,omitempty"`
	Checksum string      `json:"checksum,omitempty"`
}

type fsEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

// ── File share server ───────────────────────────────────────────────

// startFileShareListener starts the file share TCP listener inside the
// netstack. It returns a cleanup function that closes the listener.
func startFileShareListener(lg *log.Logger, tnet *netstack.Net, agentIP string, cfg *parsedFileShareConfig) func() {
	if cfg == nil || !cfg.enabled {
		return func() {}
	}

	listenAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), FileSharePort)
	listener, err := tnet.ListenTCPAddrPort(listenAddr)
	if err != nil {
		lg.Printf("[fileshare] listen failed on %s: %v", listenAddr, err)
		return func() {}
	}

	mode := "read-only"
	if cfg.writable {
		mode = "read-write"
	}
	lg.Printf("[fileshare] listening on %s (%s, dir=%s)", listenAddr, mode, cfg.directory)

	var connCount int32
	var connMu sync.Mutex

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}

			connMu.Lock()
			if int(connCount) >= maxFileShareConns {
				connMu.Unlock()
				lg.Printf("[fileshare] connection limit reached, rejecting")
				conn.Close()
				continue
			}
			connCount++
			connMu.Unlock()

			go func(c net.Conn) {
				defer func() {
					c.Close()
					connMu.Lock()
					connCount--
					connMu.Unlock()
				}()
				handleFileShareConn(lg, c, cfg)
			}(conn)
		}
	}()

	return func() { listener.Close() }
}

// handleFileShareConn processes file share requests on a single connection.
func handleFileShareConn(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig) {
	// Use a single bufio.Reader for the entire connection lifetime.
	// json.Decoder buffers ahead and would consume chunk data meant for
	// the write handler, so we read JSON lines manually instead.
	reader := bufio.NewReader(conn)

	for {
		conn.SetDeadline(time.Now().Add(fileShareIdleTimeout))

		line, err := reader.ReadBytes('\n')
		if err != nil {
			return // connection closed or timeout
		}

		var req fsRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeResponse(conn, fsResponse{OK: false, Error: "invalid request"})
			continue
		}

		switch req.Op {
		case "list":
			handleList(lg, conn, cfg, req)
		case "read":
			handleRead(lg, conn, cfg, req)
		case "write":
			handleWrite(lg, conn, reader, cfg, req)
		case "delete":
			handleDelete(lg, conn, cfg, req)
		case "mkdir":
			handleMkdir(lg, conn, cfg, req)
		case "rename":
			handleRename(lg, conn, cfg, req)
		case "move":
			handleMove(lg, conn, cfg, req)
		case "subscribe":
			handleSubscribe(lg, conn, cfg)
			return // subscribe takes over the connection
		default:
			writeResponse(conn, fsResponse{OK: false, Error: "unknown operation: " + req.Op})
		}
	}
}

func writeResponse(conn net.Conn, resp fsResponse) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}

// ── LIST ────────────────────────────────────────────────────────────

func handleList(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	dirPath := cfg.directory
	if req.Path != "" {
		var err error
		dirPath, err = cfg.validatePath(req.Path)
		if err != nil {
			writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
			return
		}
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot read directory"})
		return
	}

	var result []fsEntry
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		result = append(result, fsEntry{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
			IsDir:   e.IsDir(),
		})
	}

	lg.Printf("[fileshare] list %q: %d entries", req.Path, len(result))
	writeResponse(conn, fsResponse{OK: true, Entries: result})
}

// ── READ ────────────────────────────────────────────────────────────

func handleRead(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	if req.Path == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path required"})
		return
	}

	absPath, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
		return
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "file not found"})
		return
	}
	if info.IsDir() {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot read a directory"})
		return
	}
	if !info.Mode().IsRegular() {
		writeResponse(conn, fsResponse{OK: false, Error: "not a regular file"})
		return
	}

	f, err := os.Open(absPath)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot open file"})
		return
	}
	defer f.Close()

	// Compute checksum
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "read error"})
		return
	}
	checksum := "sha256:" + hex.EncodeToString(h.Sum(nil))

	// Seek back to start for transfer
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "seek error"})
		return
	}

	// Send header
	writeResponse(conn, fsResponse{
		OK:       true,
		Size:     info.Size(),
		ModTime:  info.ModTime().UTC().Format(time.RFC3339),
		Checksum: checksum,
	})

	// Send chunked data using a buffered writer so each chunk
	// (header + data) is sent as a single TCP segment when possible.
	bw := bufio.NewWriterSize(conn, maxChunkSize+64)
	buf := make([]byte, maxChunkSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			fmt.Fprintf(bw, "CHUNK %d\n", n)
			bw.Write(buf[:n])
			bw.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			lg.Printf("[fileshare] read error on %q: %v", req.Path, err)
			break
		}
	}
	fmt.Fprint(bw, "CHUNK 0\n")
	bw.Flush()

	lg.Printf("[fileshare] read %q: %d bytes", req.Path, info.Size())
}

// ── WRITE ───────────────────────────────────────────────────────────

func handleWrite(lg *log.Logger, conn net.Conn, reader *bufio.Reader, cfg *parsedFileShareConfig, req fsRequest) {
	if !cfg.writable {
		writeResponse(conn, fsResponse{OK: false, Error: "file sharing is read-only"})
		return
	}
	if req.Path == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path required"})
		return
	}
	if req.Size <= 0 {
		writeResponse(conn, fsResponse{OK: false, Error: "size required"})
		return
	}

	absPath, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
		return
	}

	// Check extension
	if !cfg.isExtensionAllowed(req.Path) {
		writeResponse(conn, fsResponse{OK: false, Error: "file extension not allowed"})
		return
	}

	// Check file size limit
	if req.Size > cfg.maxFileSize {
		writeResponse(conn, fsResponse{OK: false, Error: fmt.Sprintf("file too large (max %d bytes)", cfg.maxFileSize)})
		return
	}

	// Check total size limit
	if cfg.maxTotalSize > 0 {
		currentTotal, _ := dirTotalSize(cfg.directory)
		if currentTotal+req.Size > cfg.maxTotalSize {
			writeResponse(conn, fsResponse{OK: false, Error: "would exceed total size limit"})
			return
		}
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot create directory"})
		return
	}

	// Write to a temp file, then rename on success
	tmpFile, err := os.CreateTemp(parentDir, ".tela-upload-*")
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot create temp file"})
		return
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // clean up on failure; no-op if renamed
	}()

	// Read chunked data (using the shared reader from handleFileShareConn)
	h := sha256.New()
	var totalReceived int64

	for {
		conn.SetDeadline(time.Now().Add(fileShareStallTimeout))

		line, err := reader.ReadString('\n')
		if err != nil {
			writeResponse(conn, fsResponse{OK: false, Error: "transfer stalled"})
			return
		}
		line = strings.TrimRight(line, "\n\r")

		if !strings.HasPrefix(line, "CHUNK ") {
			writeResponse(conn, fsResponse{OK: false, Error: "expected CHUNK header"})
			return
		}

		chunkSize, err := strconv.ParseInt(strings.TrimPrefix(line, "CHUNK "), 10, 64)
		if err != nil {
			writeResponse(conn, fsResponse{OK: false, Error: "invalid chunk size"})
			return
		}

		if chunkSize == 0 {
			break // end of transfer
		}

		if chunkSize > maxChunkSize {
			writeResponse(conn, fsResponse{OK: false, Error: "chunk too large"})
			return
		}

		// Read exactly chunkSize bytes
		lr := io.LimitReader(reader, chunkSize)
		n, err := io.Copy(io.MultiWriter(tmpFile, h), lr)
		if err != nil || n != chunkSize {
			writeResponse(conn, fsResponse{OK: false, Error: "incomplete chunk"})
			return
		}
		totalReceived += n

		if totalReceived > req.Size {
			writeResponse(conn, fsResponse{OK: false, Error: "received more data than declared size"})
			return
		}
	}

	if totalReceived != req.Size {
		writeResponse(conn, fsResponse{OK: false, Error: fmt.Sprintf("size mismatch: expected %d, got %d", req.Size, totalReceived)})
		return
	}

	// Verify checksum
	if req.Checksum != "" {
		computed := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if computed != req.Checksum {
			writeResponse(conn, fsResponse{OK: false, Error: "checksum mismatch"})
			return
		}
	}

	// Close temp file before rename
	tmpFile.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, absPath); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "failed to finalize file"})
		return
	}

	lg.Printf("[fileshare] write %q: %d bytes", req.Path, totalReceived)
	writeResponse(conn, fsResponse{OK: true, Size: totalReceived})
}

// ── DELETE ──────────────────────────────────────────────────────────

func handleDelete(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	if !cfg.writable || !cfg.allowDelete {
		writeResponse(conn, fsResponse{OK: false, Error: "delete not allowed"})
		return
	}
	if req.Path == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path required"})
		return
	}

	absPath, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
		return
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "file not found"})
		return
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		writeResponse(conn, fsResponse{OK: false, Error: "not a regular file or directory"})
		return
	}

	// Check extension (files only)
	if !info.IsDir() && !cfg.isExtensionAllowed(req.Path) {
		writeResponse(conn, fsResponse{OK: false, Error: "file extension not allowed"})
		return
	}

	if err := os.Remove(absPath); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "delete failed"})
		return
	}

	lg.Printf("[fileshare] delete %q", req.Path)
	writeResponse(conn, fsResponse{OK: true})
}

// ── MKDIR ───────────────────────────────────────────────────────────

func handleMkdir(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	if !cfg.writable {
		writeResponse(conn, fsResponse{OK: false, Error: "file sharing is read-only"})
		return
	}
	if req.Path == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path required"})
		return
	}

	absPath, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
		return
	}

	if err := os.Mkdir(absPath, 0700); err != nil {
		if os.IsExist(err) {
			writeResponse(conn, fsResponse{OK: false, Error: "directory already exists"})
		} else {
			writeResponse(conn, fsResponse{OK: false, Error: "mkdir failed: " + err.Error()})
		}
		return
	}

	lg.Printf("[fileshare] mkdir %q", req.Path)
	writeResponse(conn, fsResponse{OK: true})
}

// ── RENAME ──────────────────────────────────────────────────────────

func handleRename(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	if !cfg.writable {
		writeResponse(conn, fsResponse{OK: false, Error: "file sharing is read-only"})
		return
	}
	if req.Path == "" || req.NewName == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path and newName required"})
		return
	}

	// Validate the source path
	absPath, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: err.Error()})
		return
	}

	// NewName must be a bare filename, not a path
	if strings.Contains(req.NewName, "/") || strings.Contains(req.NewName, "\\") {
		writeResponse(conn, fsResponse{OK: false, Error: "newName must be a filename, not a path"})
		return
	}

	// Build the new path in the same directory
	newAbs := filepath.Join(filepath.Dir(absPath), req.NewName)

	// Validate the new path is still in the sandbox
	rel, err := filepath.Rel(cfg.directory, newAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		writeResponse(conn, fsResponse{OK: false, Error: "rename would escape sandbox"})
		return
	}

	// Check that source exists
	if _, err := os.Lstat(absPath); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "source not found"})
		return
	}

	// Check that destination doesn't exist
	if _, err := os.Lstat(newAbs); err == nil {
		writeResponse(conn, fsResponse{OK: false, Error: "destination already exists"})
		return
	}

	if err := os.Rename(absPath, newAbs); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "rename failed: " + err.Error()})
		return
	}

	lg.Printf("[fileshare] rename %q -> %q", req.Path, req.NewName)
	writeResponse(conn, fsResponse{OK: true})
}

// ── MOVE ────────────────────────────────────────────────────────────

func handleMove(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig, req fsRequest) {
	if !cfg.writable {
		writeResponse(conn, fsResponse{OK: false, Error: "file sharing is read-only"})
		return
	}
	if req.Path == "" || req.NewPath == "" {
		writeResponse(conn, fsResponse{OK: false, Error: "path and newPath required"})
		return
	}

	// Validate source path
	srcAbs, err := cfg.validatePath(req.Path)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "source: " + err.Error()})
		return
	}

	// Validate destination path
	dstAbs, err := cfg.validatePath(req.NewPath)
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "destination: " + err.Error()})
		return
	}

	// Check source exists
	if _, err := os.Lstat(srcAbs); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "source not found"})
		return
	}

	// If destination is an existing directory, move into it
	if info, err := os.Lstat(dstAbs); err == nil && info.IsDir() {
		dstAbs = filepath.Join(dstAbs, filepath.Base(srcAbs))
		// Re-validate the final path
		rel, err := filepath.Rel(cfg.directory, dstAbs)
		if err != nil || strings.HasPrefix(rel, "..") {
			writeResponse(conn, fsResponse{OK: false, Error: "move would escape sandbox"})
			return
		}
	}

	// Check destination doesn't already exist (unless it's a directory we're moving into)
	if _, err := os.Lstat(dstAbs); err == nil {
		writeResponse(conn, fsResponse{OK: false, Error: "destination already exists"})
		return
	}

	// Ensure destination parent directory exists
	dstDir := filepath.Dir(dstAbs)
	if _, err := os.Stat(dstDir); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "destination directory does not exist"})
		return
	}

	// Cannot move a directory into itself
	if strings.HasPrefix(dstAbs, srcAbs+string(filepath.Separator)) {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot move a directory into itself"})
		return
	}

	if err := os.Rename(srcAbs, dstAbs); err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "move failed: " + err.Error()})
		return
	}

	lg.Printf("[fileshare] move %q -> %q", req.Path, req.NewPath)
	writeResponse(conn, fsResponse{OK: true})
}

// ── SUBSCRIBE (live file change events) ─────────────────────────────

// fsEvent is a file change notification sent to subscribed clients.
type fsEvent struct {
	Type    string `json:"type"`    // "file_created", "file_modified", "file_deleted", "file_renamed"
	Path    string `json:"path"`    // relative path within the share
	Name    string `json:"name"`    // file/directory name
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"modTime,omitempty"`
}

func handleSubscribe(lg *log.Logger, conn net.Conn, cfg *parsedFileShareConfig) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "cannot create file watcher: " + err.Error()})
		return
	}
	defer watcher.Close()

	// Watch the share directory and all subdirectories.
	err = filepath.Walk(cfg.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		writeResponse(conn, fsResponse{OK: false, Error: "watch setup failed: " + err.Error()})
		return
	}

	// Confirm subscription.
	writeResponse(conn, fsResponse{OK: true})
	lg.Printf("[fileshare] subscribe: client watching %s", cfg.directory)

	// Stream events until the connection closes or an error occurs.
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Compute relative path within the share.
			relPath, err := filepath.Rel(cfg.directory, event.Name)
			if err != nil || strings.HasPrefix(relPath, "..") {
				continue
			}
			// Normalize to forward slashes for the protocol.
			relPath = filepath.ToSlash(relPath)
			name := filepath.Base(event.Name)

			// Skip temp files from our own uploads.
			if strings.HasPrefix(name, ".tela-upload-") {
				continue
			}

			var evtType string
			switch {
			case event.Op&fsnotify.Create != 0:
				evtType = "file_created"
			case event.Op&fsnotify.Write != 0:
				evtType = "file_modified"
			case event.Op&fsnotify.Remove != 0:
				evtType = "file_deleted"
			case event.Op&fsnotify.Rename != 0:
				evtType = "file_renamed"
			default:
				continue
			}

			fe := fsEvent{
				Type: evtType,
				Path: relPath,
				Name: name,
			}

			// For create/modify, stat the file for metadata.
			if evtType == "file_created" || evtType == "file_modified" {
				if info, err := os.Lstat(event.Name); err == nil {
					fe.IsDir = info.IsDir()
					fe.Size = info.Size()
					fe.ModTime = info.ModTime().UTC().Format(time.RFC3339)

					// If a new directory was created, watch it too.
					if info.IsDir() && evtType == "file_created" {
						watcher.Add(event.Name)
					}
				}
			}

			data, _ := json.Marshal(fe)
			data = append(data, '\n')
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(data); err != nil {
				lg.Printf("[fileshare] subscribe: write failed, closing")
				return
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			lg.Printf("[fileshare] watch error: %v", err)
		}
	}
}
