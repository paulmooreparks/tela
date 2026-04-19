// channels_server.go -- self-hosted release channel server inside telahubd.
//
// Replaces the standalone telachand daemon. When channels.enabled is true
// in the hub config, the hub serves:
//
//	GET /channels/{name}.json     -- manifest for channel {name}
//	GET /channels/files/{binary}  -- binary download
//
// The on-disk layout under channels.data matches what telachand used:
//
//	{data}/{name}.json    -- manifest written by `telahubd channels publish`
//	{data}/files/{binary} -- binary drops produced by the build pipeline
//
// Manifest endpoints have no auth: they are public download URLs by design.
// The rest of telahubd is unaffected; admin endpoints, status, the WS upgrade
// path, and static file serving all keep their existing CORS and auth
// behaviour.

package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulmooreparks/tela/internal/channel"
)

// handleChannels routes a request below /channels/ to either the manifest
// handler or the file handler. It is registered once in Run() when
// cfg.Channels.Enabled is true.
//
// Path forms recognised:
//
//	/channels/                 -> health JSON
//	/channels/{name}.json      -> manifest
//	/channels/files/{binary}   -> binary file
//
// Anything else under /channels/ returns 404. Methods other than GET/HEAD
// return 405 with a CORS-safe response so cross-origin update fetchers can
// surface the error to the user.
func handleChannels(w http.ResponseWriter, r *http.Request) {
	channelsCorsHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/channels")
	if rest == "" || rest == "/" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"service": "telahubd-channels",
			"version": version,
		})
		return
	}

	rest = strings.TrimPrefix(rest, "/")

	globalCfgMu.RLock()
	dataDir := globalCfg.Channels.Data
	globalCfgMu.RUnlock()
	if dataDir == "" {
		http.Error(w, "channels.data not configured", http.StatusInternalServerError)
		return
	}

	// /channels/files/{binary}
	if strings.HasPrefix(rest, "files/") {
		serveChannelFile(w, r, dataDir, strings.TrimPrefix(rest, "files/"))
		return
	}

	// /channels/{name}.json
	if strings.HasSuffix(rest, ".json") && !strings.ContainsRune(rest, '/') {
		name := strings.TrimSuffix(rest, ".json")
		if !channel.IsValid(name) {
			http.NotFound(w, r)
			return
		}
		serveChannelManifest(w, r, dataDir, name)
		return
	}

	http.NotFound(w, r)
}

// serveChannelManifest writes the manifest file for the named channel.
// 404 when the file does not exist; the caller checks IsValid first.
func serveChannelManifest(w http.ResponseWriter, r *http.Request, dataDir, name string) {
	path := filepath.Join(dataDir, name+".json")
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, path)
}

// serveChannelFile streams a binary from {dataDir}/files/{name}. Path traversal
// (../) is rejected; only flat file names within files/ are served.
func serveChannelFile(w http.ResponseWriter, r *http.Request, dataDir, name string) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(dataDir, "files", name)
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Long-cache binary downloads. Manifests are small JSON and are
	// re-fetched on every update check, so they get no cache header and
	// pick up Last-Modified from http.ServeFile.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
}

// channelsCorsHeaders permits any origin to GET manifest and binary files.
// This matches telachand's behaviour and the public-by-design intent of
// release channel endpoints; tightening would break browser-side update
// checks served from a different origin.
func channelsCorsHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Max-Age", "86400")
}
