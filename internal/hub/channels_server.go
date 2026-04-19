// channels_server.go -- self-hosted release channel server inside telahubd.
//
// Replaces the standalone telachand daemon. When channels.enabled is true
// in the hub config, the hub serves:
//
//	GET /channels/                       -- health JSON
//	GET /channels/{name}.json            -- manifest for channel {name}
//	GET /channels/files/                 -- browsable index of all channels
//	GET /channels/files/{channel}/       -- browsable file index for one channel
//	GET /channels/files/{channel}/{bin}  -- binary download
//
// The on-disk layout under channels.data is:
//
//	{data}/{name}.json             -- manifest written by `telahubd channels publish`
//	{data}/files/{channel}/{bin}   -- binary drops for that channel
//
// Every channel gets its own subdirectory under files/ so two channels can
// hold different binaries under the same filename without colliding.
//
// Manifest, binary, and listing endpoints all have no auth: they are public
// download URLs by design. The rest of telahubd is unaffected; admin
// endpoints, status, the WS upgrade path, and static file serving all keep
// their existing CORS and auth behaviour.

package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulmooreparks/tela/internal/channel"
)

// handleChannels routes a request below /channels/ to the manifest handler,
// the health handler, or the file server. It is registered once in Run()
// when cfg.Channels.Enabled is true.
//
// Methods other than GET/HEAD/OPTIONS return 405 with a CORS-safe response
// so cross-origin update fetchers can surface the error to the user.
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

	// /channels/files/... is handled by a filesystem-backed file server.
	// http.FileServer covers directory listings, Range requests, ETag, and
	// rejects path traversal when used with http.Dir. We wrap it so we can
	// set a long cache-control header on binary downloads (not directory
	// listings).
	if rest == "files" || strings.HasPrefix(rest, "files/") {
		serveChannelFiles(w, r, dataDir)
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

// serveChannelFiles handles /channels/files/... by delegating to
// http.FileServer rooted at {data}/files/. The FileServer handles directory
// listings (for /channels/files/ and /channels/files/{channel}/) and file
// serving (for /channels/files/{channel}/{binary}) uniformly; http.Dir
// rejects path traversal attempts. We set a long immutable cache header on
// the file-download case only, so directory listings always reflect the
// current contents.
func serveChannelFiles(w http.ResponseWriter, r *http.Request, dataDir string) {
	filesDir := filepath.Join(dataDir, "files")
	// Determine whether the request will resolve to a file or a directory.
	// http.FileServer sets its own Content-Type and handles the stat; we
	// just peek at the filesystem first so we can decide on the
	// Cache-Control header. When the path does not exist, FileServer
	// returns 404 -- let it do that.
	rel := strings.TrimPrefix(r.URL.Path, "/channels/files/")
	rel = strings.TrimPrefix(rel, "/") // handle exact "/channels/files"
	local := filepath.Join(filesDir, filepath.FromSlash(rel))
	if st, err := os.Stat(local); err == nil && !st.IsDir() {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.StripPrefix("/channels/files/", http.FileServer(http.Dir(filesDir))).ServeHTTP(w, r)
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
