/*
tela files -- file sharing client.

Purpose:

	Provides CLI access to file shares on connected machines. Requires
	an active tela connect session. Communicates with the running tela
	process via its control API, which proxies file share requests
	through the WireGuard tunnel to telad port 17377.

Usage:

	tela files ls -machine barn [path]
	tela files get -machine barn <remote-path> [-o local-path]
	tela files put -machine barn <local-path> [remote-name]
	tela files rm -machine barn <remote-path>
	tela files info -machine barn
*/
package client

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// FileSharePort must match the constant in cmd/telad/fileshare.go.
const fileSharePort = 17377

// ── Tunnel registry ─────────────────────────────────────────────────
// Stores per-machine netstack references so the control API (and tela
// files in-process) can dial through the tunnel.

type tunnelInfo struct {
	tnet    *netstack.Net
	agentIP string
}

var (
	tunnelRegistryMu sync.Mutex
	tunnelRegistry   = map[string]*tunnelInfo{} // machineID → info
)

func registerTunnel(machine string, tnet *netstack.Net, agentIP string) {
	tunnelRegistryMu.Lock()
	defer tunnelRegistryMu.Unlock()
	tunnelRegistry[machine] = &tunnelInfo{tnet: tnet, agentIP: agentIP}

	// Flush the mount directory cache so stale entries from the
	// previous session are not served after a reconnect.
	mountDirCacheMu.Lock()
	for k := range mountDirCache {
		if strings.HasPrefix(k, machine+":") {
			delete(mountDirCache, k)
		}
	}
	mountDirCacheMu.Unlock()
}

func unregisterTunnel(machine string) {
	tunnelRegistryMu.Lock()
	defer tunnelRegistryMu.Unlock()
	delete(tunnelRegistry, machine)
}

func lookupTunnel(machine string) *tunnelInfo {
	tunnelRegistryMu.Lock()
	defer tunnelRegistryMu.Unlock()
	return tunnelRegistry[machine]
}

// listTunnelMachines returns the machine IDs of all registered tunnels.
func listTunnelMachines() []string {
	tunnelRegistryMu.Lock()
	defer tunnelRegistryMu.Unlock()
	out := make([]string, 0, len(tunnelRegistry))
	for m := range tunnelRegistry {
		out = append(out, m)
	}
	return out
}

// startFileEventSubscription opens subscribe connections to all shares on a
// machine and forwards events through the control WebSocket. One goroutine
// is started per share. All goroutines are stopped when the stop channel
// is closed.
func startFileEventSubscription(machine string, stop chan struct{}) {
	// Discover available shares before subscribing.
	conn, err := tryDialFileShare(machine)
	if err != nil {
		return // no file share listener on this machine
	}
	listReq, _ := json.Marshal(fsRequest{Op: "list-shares"})
	listReq = append(listReq, '\n')
	conn.Write(listReq)
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadBytes('\n')
	conn.Close()
	if err != nil {
		return
	}
	var listResp fsResponse
	if json.Unmarshal(respLine, &listResp) != nil || !listResp.OK {
		return
	}

	for _, share := range listResp.Shares {
		go subscribeShare(machine, share.Name, stop)
	}
}

// subscribeShare subscribes to file events for one named share.
func subscribeShare(machine, shareName string, stop chan struct{}) {
	conn, err := tryDialFileShare(machine)
	if err != nil {
		return
	}

	req, _ := json.Marshal(fsRequest{Op: "subscribe", Share: shareName})
	req = append(req, '\n')
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return
	}

	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return
	}
	var resp fsResponse
	if json.Unmarshal(respLine, &resp) != nil || !resp.OK {
		conn.Close()
		return
	}

	log.Printf("[fileshare] subscribed to events from %s/%s", machine, shareName)

	defer conn.Close()

	go func() {
		<-stop
		conn.Close()
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var raw map[string]interface{}
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		raw["machine"] = machine
		raw["share"] = shareName
		emitEvent(raw)
	}
}

// dialFileShare dials the file share port on a connected machine's tunnel.
// fileShareRequest sends a file share request, trying the in-process tunnel first,
// then falling back to the control API of a running tela instance.
func fileShareRequest(machine string, req fsRequest) (*fsResponse, *bufio.Reader, error) {
	conn, err := dialFileShare(machine)
	if err == nil {
		resp, reader, sendErr := sendRequest(conn, req)
		if sendErr != nil {
			conn.Close()
			return nil, nil, sendErr
		}
		return resp, reader, nil
	}

	// Fall back to control API
	return controlFileShareRequest(machine, req)
}

// controlFileShareRequest sends a file share operation through the control API
// of a running tela process. This works even when called from a separate process.
func controlFileShareRequest(machine string, req fsRequest) (*fsResponse, *bufio.Reader, error) {
	controlPath := filepath.Join(telaConfigDir(), "run", "control.json")
	data, err := os.ReadFile(controlPath)
	if err != nil {
		return nil, nil, fmt.Errorf("no running tela instance found (no control file)")
	}
	var info struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &info) != nil || info.Port == 0 {
		return nil, nil, fmt.Errorf("invalid control file")
	}

	reqBody, _ := json.Marshal(req)
	reqBody = append(reqBody, '\n')
	url := fmt.Sprintf("http://127.0.0.1:%d/files/%s", info.Port, machine)

	httpReq, _ := http.NewRequest("POST", url, strings.NewReader(string(reqBody)))
	httpReq.Header.Set("Authorization", "Bearer "+info.Token)
	httpReq.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpReq = httpReq.WithContext(ctx)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("control API request failed: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("read response failed: %w", err)
	}

	if resp.StatusCode != 200 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	reader := bufio.NewReader(strings.NewReader(string(body)))
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, nil, fmt.Errorf("empty response")
	}

	var fsResp fsResponse
	if err := json.Unmarshal(line, &fsResp); err != nil {
		return nil, nil, fmt.Errorf("invalid response: %w", err)
	}

	return &fsResp, reader, nil
}

// dialFileShare connects to a machine's file share port through the tunnel.
// It retries with increasing timeouts because the file share listener on
// telad may not be ready immediately after the WireGuard tunnel is up.
func dialFileShare(machine string) (net.Conn, error) {
	ti := lookupTunnel(machine)
	if ti == nil {
		return nil, fmt.Errorf("no active tunnel for machine %q (is tela connect running?)", machine)
	}
	addr := netip.AddrPortFrom(netip.MustParseAddr(ti.agentIP), fileSharePort)

	delays := []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second}
	var lastErr error
	for _, d := range delays {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		conn, err := ti.tnet.DialContextTCPAddrPort(ctx, addr)
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		log.Printf("[fileshare] dial %s:%d timed out after %s, retrying...", machine, fileSharePort, d)
	}
	return nil, fmt.Errorf("cannot connect to file share on %s: %w", machine, lastErr)
}

// tryDialFileShare attempts a single connection to a machine's file share
// port with a short timeout. Used for best-effort background operations
// (event subscription) where failure means the machine has no file share.
func tryDialFileShare(machine string) (net.Conn, error) {
	ti := lookupTunnel(machine)
	if ti == nil {
		return nil, fmt.Errorf("no tunnel")
	}
	addr := netip.AddrPortFrom(netip.MustParseAddr(ti.agentIP), fileSharePort)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ti.tnet.DialContextTCPAddrPort(ctx, addr)
}

// ── Protocol types (must match cmd/telad/fileshare.go) ──────────────

type fsRequest struct {
	Op       string `json:"op"`
	Share    string `json:"share,omitempty"` // target share name
	Path     string `json:"path"`
	Size     int64  `json:"size,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	NewName  string `json:"newName,omitempty"`
	NewPath  string `json:"newPath,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// fsShareInfo describes one share returned by list-shares.
type fsShareInfo struct {
	Name        string `json:"name"`
	Writable    bool   `json:"writable,omitempty"`
	AllowDelete bool   `json:"allowDelete,omitempty"`
}

type fsResponse struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Entries  []fsEntry     `json:"entries,omitempty"`
	Shares   []fsShareInfo `json:"shares,omitempty"` // for list-shares
	Size     int64         `json:"size,omitempty"`
	ModTime  string        `json:"modTime,omitempty"`
	Checksum string        `json:"checksum,omitempty"`
	Total    int           `json:"total,omitempty"`
}

type fsEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

// ── CLI: tela files ─────────────────────────────────────────────────

func cmdFiles(args []string) {
	if len(args) == 0 {
		printFilesUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "ls":
		cmdFilesLs(args[1:])
	case "get":
		cmdFilesGet(args[1:])
	case "put":
		cmdFilesPut(args[1:])
	case "rm":
		cmdFilesRm(args[1:])
	case "mkdir":
		cmdFilesMkdir(args[1:])
	case "rename":
		cmdFilesRename(args[1:])
	case "mv":
		cmdFilesMv(args[1:])
	case "info":
		cmdFilesInfo(args[1:])
	case "help", "-h", "--help":
		printFilesUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown files subcommand: %s\n", args[0])
		printFilesUsage()
		os.Exit(1)
	}
}

func printFilesUsage() {
	fmt.Fprintf(os.Stderr, `tela files -- File sharing with connected machines

Requires an active tela connect session with file sharing enabled on
the target machine.

Subcommands:
  ls     [-machine <id>] -share <name> [path]             List files
  get    [-machine <id>] -share <name> <path> [-o out]    Download a file
  put    [-machine <id>] -share <name> <local> [remote]   Upload a file
  rm     [-machine <id>] -share <name> <path>             Delete a file
  mkdir  [-machine <id>] -share <name> <path>             Create a directory
  rename [-machine <id>] -share <name> <path> <new-name>  Rename a file or directory
  mv     [-machine <id>] -share <name> <src> <dest>       Move a file or directory
  info   [-machine <id>]                                   Show file share status

`)
}

// sendRequest sends a file share request over a connection and reads
// the JSON response line.
func sendRequest(conn net.Conn, req fsRequest) (*fsResponse, *bufio.Reader, error) {
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, nil, fmt.Errorf("send failed: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("read response failed: %w", err)
	}

	var resp fsResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, nil, fmt.Errorf("invalid response: %w", err)
	}

	return &resp, reader, nil
}

func cmdFilesLs(args []string) {
	fs := flag.NewFlagSet("files ls", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -machine is required")
		os.Exit(1)
	}
	if *share == "" {
		fmt.Fprintln(os.Stderr, "Error: -share is required")
		os.Exit(1)
	}

	path := ""
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "list", Share: *share, Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "NAME\tSIZE\tMODIFIED\n")
	for _, e := range resp.Entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		size := formatSize(e.Size)
		if e.IsDir {
			size = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, size, e.ModTime)
	}
	tw.Flush()
}

func cmdFilesGet(args []string) {
	fs := flag.NewFlagSet("files get", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	output := fs.String("o", "", "Output file path (default: same name in current dir)")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files get -machine <id> -share <name> <remote-path> [-o local-path]")
		os.Exit(1)
	}
	remotePath := fs.Arg(0)

	resp, reader, err := fileShareRequest(*machine, fsRequest{Op: "read", Share: *share, Path: remotePath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	localPath := *output
	if localPath == "" {
		localPath = filepath.Base(remotePath)
	}

	f, err := os.Create(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", localPath, err)
		os.Exit(1)
	}
	defer f.Close()

	h := sha256.New()
	var totalReceived int64

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading chunk: %v\n", err)
			os.Exit(1)
		}
		line = strings.TrimRight(line, "\n\r")

		if !strings.HasPrefix(line, "CHUNK ") {
			fmt.Fprintf(os.Stderr, "Protocol error: expected CHUNK header, got: %s\n", line)
			os.Exit(1)
		}

		chunkSize, err := strconv.ParseInt(strings.TrimPrefix(line, "CHUNK "), 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Protocol error: invalid chunk size\n")
			os.Exit(1)
		}

		if chunkSize == 0 {
			break
		}

		n, err := io.CopyN(io.MultiWriter(f, h), reader, chunkSize)
		if err != nil || n != chunkSize {
			fmt.Fprintf(os.Stderr, "Error reading chunk data\n")
			os.Exit(1)
		}
		totalReceived += n
	}

	// Verify checksum
	if resp.Checksum != "" {
		computed := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if computed != resp.Checksum {
			os.Remove(localPath)
			fmt.Fprintf(os.Stderr, "Error: checksum mismatch (file deleted)\n")
			os.Exit(1)
		}
	}

	fmt.Printf("%s (%s)\n", localPath, formatSize(totalReceived))
}

func cmdFilesPut(args []string) {
	fs := flag.NewFlagSet("files put", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files put -machine <id> -share <name> <local-path> [remote-name]")
		os.Exit(1)
	}

	localPath := fs.Arg(0)
	remoteName := filepath.Base(localPath)
	if fs.NArg() > 1 {
		remoteName = fs.Arg(1)
	}

	info, err := os.Stat(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if info.IsDir() {
		fmt.Fprintln(os.Stderr, "Error: cannot upload directories")
		os.Exit(1)
	}

	f, err := os.Open(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// Compute checksum first
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}
	checksum := "sha256:" + hex.EncodeToString(h.Sum(nil))
	f.Seek(0, io.SeekStart)

	conn, err := dialFileShare(*machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send write request header
	req := fsRequest{Op: "write", Share: *share, Path: remoteName, Size: info.Size(), Checksum: checksum}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	conn.Write(data)

	// Send chunks
	buf := make([]byte, 16384)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			header := fmt.Sprintf("CHUNK %d\n", n)
			conn.Write([]byte(header))
			conn.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
	}
	conn.Write([]byte("CHUNK 0\n"))

	// Read response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}
	var resp fsResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid response: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Uploaded %s (%s)\n", remoteName, formatSize(info.Size()))
}

func cmdFilesRm(args []string) {
	fs := flag.NewFlagSet("files rm", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files rm -machine <id> -share <name> <remote-path>")
		os.Exit(1)
	}
	remotePath := fs.Arg(0)

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "delete", Share: *share, Path: remotePath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Deleted %s\n", remotePath)
}

func cmdFilesMkdir(args []string) {
	fs := flag.NewFlagSet("files mkdir", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files mkdir -machine <id> -share <name> <path>")
		os.Exit(1)
	}
	dirPath := fs.Arg(0)

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "mkdir", Share: *share, Path: dirPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Created %s\n", dirPath)
}

func cmdFilesRename(args []string) {
	fs := flag.NewFlagSet("files rename", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela files rename -machine <id> -share <name> <path> <new-name>")
		os.Exit(1)
	}
	oldPath := fs.Arg(0)
	newName := fs.Arg(1)

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "rename", Share: *share, Path: oldPath, NewName: newName})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Renamed %s -> %s\n", oldPath, newName)
}

func cmdFilesMv(args []string) {
	fs := flag.NewFlagSet("files mv", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	share := fs.String("share", envOrDefault("TELA_SHARE", ""), "Share name")
	fs.Parse(args)

	if *machine == "" || *share == "" || fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tela files mv -machine <id> -share <name> <source> <destination>")
		os.Exit(1)
	}
	srcPath := fs.Arg(0)
	dstPath := fs.Arg(1)

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "move", Share: *share, Path: srcPath, NewPath: dstPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Moved %s -> %s\n", srcPath, dstPath)
}

func cmdFilesInfo(args []string) {
	fs := flag.NewFlagSet("files info", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	fs.Parse(args)

	if *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -machine is required")
		os.Exit(1)
	}

	resp, _, err := fileShareRequest(*machine, fsRequest{Op: "list-shares"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Machine: %s\n", *machine)
	fmt.Printf("Shares:  %d\n", len(resp.Shares))
	for _, s := range resp.Shares {
		access := "read-only"
		if s.Writable {
			access = "read-write"
			if s.AllowDelete {
				access = "read-write (delete allowed)"
			}
		}
		fmt.Printf("  %s  %s\n", s.Name, access)
	}
}

func formatSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
