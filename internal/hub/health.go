// health.go -- "telahubd health" subcommand.
//
// A lightweight health probe meant to be invoked from inside the running
// container by Docker's HEALTHCHECK directive. The distroless runtime
// image carries no curl or wget, so the binary itself supplies the probe.
//
// Exits 0 when the local hub responds to GET /.well-known/tela with a
// well-formed JSON document carrying the expected "protocolVersion"
// field. Exits non-zero in every other case (connection refused,
// timeout, non-2xx, malformed JSON, missing field).
//
// Intentionally does NOT exercise any authenticated path. A healthy hub
// that happens to be in auth-enabled mode still returns 200 on the
// well-known endpoint; requiring auth for the health probe would force
// operators to bake a token into the HEALTHCHECK, which is worse.

package hub

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// cmdHealth is the 'telahubd health' entry point.
func cmdHealth(args []string) {
	fs := flag.NewFlagSet("telahubd health", flag.ExitOnError)
	portFlag := fs.Int("port", 0, "Port the hub is listening on (default: TELAHUBD_PORT env var, else 80)")
	timeoutFlag := fs.Duration("timeout", 3*time.Second, "Overall probe timeout")
	urlFlag := fs.String("url", "", "Full URL override (default: http://127.0.0.1:<port>/.well-known/tela)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: telahubd health [-port <n>] [-timeout <duration>] [-url <full-url>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Probes the local hub's /.well-known/tela endpoint and exits 0 when")
		fmt.Fprintln(os.Stderr, "the hub is healthy. Used by the Dockerfile's HEALTHCHECK directive.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	url := *urlFlag
	if url == "" {
		port := *portFlag
		if port == 0 {
			if s := strings.TrimSpace(os.Getenv("TELAHUBD_PORT")); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					port = n
				}
			}
		}
		if port == 0 {
			port = 80
		}
		url = fmt.Sprintf("http://127.0.0.1:%d/.well-known/tela", port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health: build request: %v\n", err)
		os.Exit(1)
	}

	// Fresh transport each call so we never reuse a stale connection and
	// so the loopback probe is not affected by any proxy env the
	// container inherits.
	client := &http.Client{
		Timeout: *timeoutFlag,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: *timeoutFlag,
			}).DialContext,
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "health: %s returned HTTP %d\n", url, resp.StatusCode)
		os.Exit(1)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		fmt.Fprintf(os.Stderr, "health: read body: %v\n", err)
		os.Exit(1)
	}

	// /.well-known/tela is documented to carry a protocolVersion field.
	// Check for its presence as a cheap sanity test; a proxy returning a
	// plain 200 with HTML would otherwise look healthy.
	var parsed struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "health: %s returned non-JSON body: %v\n", url, err)
		os.Exit(1)
	}
	if strings.TrimSpace(parsed.ProtocolVersion) == "" {
		fmt.Fprintf(os.Stderr, "health: %s response missing protocolVersion field\n", url)
		os.Exit(1)
	}

	fmt.Printf("ok: telahubd %s listening at %s (protocol %s)\n", version, url, parsed.ProtocolVersion)
}
