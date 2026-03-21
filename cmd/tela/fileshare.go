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
package main

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

// startFileEventSubscription opens a subscribe connection to telad's file
// share port and forwards events through the control WebSocket. Runs until
// the connection closes or the stop channel is signalled.
func startFileEventSubscription(machine string, stop chan struct{}) {
	conn, err := dialFileShare(machine)
	if err != nil {
		return // no file share on this machine
	}

	// Send subscribe request
	req, _ := json.Marshal(fsRequest{Op: "subscribe"})
	req = append(req, '\n')
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return
	}

	// Read the confirmation response
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

	log.Printf("[fileshare] subscribed to events from %s", machine)

	// Forward events until stopped
	go func() {
		defer conn.Close()

		// Close connection when stop is signalled
		go func() {
			<-stop
			conn.Close()
		}()

		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}

			// Parse the event and add the machine name
			var raw map[string]interface{}
			if json.Unmarshal(line, &raw) != nil {
				continue
			}
			raw["machine"] = machine
			emitEvent(raw)
		}
	}()
}

// dialFileShare dials the file share port on a connected machine's tunnel.
func dialFileShare(machine string) (net.Conn, error) {
	ti := lookupTunnel(machine)
	if ti == nil {
		return nil, fmt.Errorf("no active tunnel for machine %q (is tela connect running?)", machine)
	}
	addr := netip.AddrPortFrom(netip.MustParseAddr(ti.agentIP), fileSharePort)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := ti.tnet.DialContextTCPAddrPort(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to file share on %s: %w", machine, err)
	}
	return conn, nil
}

// ── Protocol types (must match cmd/telad/fileshare.go) ──────────────

type fsRequest struct {
	Op       string `json:"op"`
	Path     string `json:"path"`
	Size     int64  `json:"size,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

type fsResponse struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Entries  []fsEntry `json:"entries,omitempty"`
	Size     int64     `json:"size,omitempty"`
	ModTime  string    `json:"modTime,omitempty"`
	Checksum string    `json:"checksum,omitempty"`
}

type fsEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

// ── Control API file share proxy ────────────────────────────────────
// These are called from control.go when the /files endpoint is hit.

// controlFileShareHandler handles proxied file share requests from the
// control API. It dials through the tunnel and proxies the JSON-line
// protocol.
func controlFileShareHandler(machine string, reqBody io.Reader, w io.Writer) error {
	conn, err := dialFileShare(machine)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Proxy request
	if _, err := io.Copy(conn, reqBody); err != nil {
		return fmt.Errorf("send failed: %w", err)
	}

	// Proxy response
	if _, err := io.Copy(w, conn); err != nil {
		return fmt.Errorf("recv failed: %w", err)
	}
	return nil
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
  ls    [-machine <id>] [path]           List files
  get   [-machine <id>] <path> [-o out]  Download a file
  put   [-machine <id>] <local> [remote] Upload a file
  rm    [-machine <id>] <path>           Delete a file
  info  [-machine <id>]                  Show file share status

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
	fs.Parse(args)

	if *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -machine is required")
		os.Exit(1)
	}

	path := ""
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	conn, err := dialFileShare(*machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	resp, _, err := sendRequest(conn, fsRequest{Op: "list", Path: path})
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
	output := fs.String("o", "", "Output file path (default: same name in current dir)")
	fs.Parse(args)

	if *machine == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files get -machine <id> <remote-path> [-o local-path]")
		os.Exit(1)
	}
	remotePath := fs.Arg(0)

	conn, err := dialFileShare(*machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	resp, reader, err := sendRequest(conn, fsRequest{Op: "read", Path: remotePath})
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
	fs.Parse(args)

	if *machine == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files put -machine <id> <local-path> [remote-name]")
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
	req := fsRequest{Op: "write", Path: remoteName, Size: info.Size(), Checksum: checksum}
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
	fs.Parse(args)

	if *machine == "" || fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tela files rm -machine <id> <remote-path>")
		os.Exit(1)
	}
	remotePath := fs.Arg(0)

	conn, err := dialFileShare(*machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	resp, _, err := sendRequest(conn, fsRequest{Op: "delete", Path: remotePath})
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

func cmdFilesInfo(args []string) {
	fs := flag.NewFlagSet("files info", flag.ExitOnError)
	machine := fs.String("machine", envOrDefault("TELA_MACHINE", ""), "Machine ID")
	fs.Parse(args)

	if *machine == "" {
		fmt.Fprintln(os.Stderr, "Error: -machine is required")
		os.Exit(1)
	}

	conn, err := dialFileShare(*machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// List root to verify connectivity and show basic info
	resp, _, err := sendRequest(conn, fsRequest{Op: "list", Path: ""})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fileCount := 0
	dirCount := 0
	var totalSize int64
	for _, e := range resp.Entries {
		if e.IsDir {
			dirCount++
		} else {
			fileCount++
			totalSize += e.Size
		}
	}

	fmt.Printf("Machine:     %s\n", *machine)
	fmt.Printf("Files:       %d\n", fileCount)
	fmt.Printf("Directories: %d\n", dirCount)
	fmt.Printf("Total size:  %s\n", formatSize(totalSize))
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
