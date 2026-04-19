// channels_admin.go -- admin API for the self-hosted release channel server.
//
// Two endpoints, both require owner/admin auth:
//
//	PUT  /api/admin/channels/files/{name}   Upload or overwrite a binary
//	POST /api/admin/channels/publish        Write the channel manifest
//
// Together they let a build pipeline ship new binaries to an owlsnest-
// style self-hosted hub over HTTPS with no tunnel or SSH: upload each
// artifact to the files endpoint, then POST /publish to hash everything
// in files/ and regenerate {channel}.json.

package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/paulmooreparks/tela/internal/channel"
)

// maxChannelFileSize caps a single uploaded binary at 500 MiB. TelaVisor
// is the largest Tela binary we ship and sits well under 100 MiB, so
// 500 MiB leaves plenty of headroom while preventing a misbehaving
// client from exhausting disk with a single request.
const maxChannelFileSize = 500 * 1024 * 1024

// channelFileNameOK validates the {name} path segment on the upload
// endpoint. Only flat file names are accepted: no slashes, no leading
// dots, no parent-dir traversals. The same character set that release
// artifacts use (letters, digits, dots, dashes, underscores).
var channelFileNameOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// handleAdminChannelsFiles serves PUT /api/admin/channels/files/{channel}/{name}.
// Streams the request body into {channels.data}/files/{channel}/{name}.tmp,
// fsyncs, and renames into place so a reader racing with an upload either
// sees the previous byte-complete file or the new one, never a partial.
// Every channel has its own file subdirectory so two channels can hold
// binaries with the same name (e.g. telad-linux-amd64) without collision.
func handleAdminChannelsFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPut {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed; use PUT"})
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/channels/files/")
	slash := strings.IndexRune(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "expected /api/admin/channels/files/{channel}/{name}"})
		return
	}
	channelName := rest[:slash]
	name := rest[slash+1:]
	if !channel.IsValid(channelName) {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid channel name"})
		return
	}
	if !channelFileNameOK.MatchString(name) {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid file name"})
		return
	}

	globalCfgMu.RLock()
	enabled := globalCfg.Channels.Enabled
	dataDir := globalCfg.Channels.Data
	globalCfgMu.RUnlock()
	if !enabled {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusConflict, map[string]string{"error": "channels.enabled is false"})
		return
	}
	if dataDir == "" {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "channels.data is not configured"})
		return
	}

	filesDir := filepath.Join(dataDir, "files", channelName)
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "create files dir: " + err.Error()})
		return
	}

	dstPath := filepath.Join(filesDir, name)
	tmpPath := dstPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "open temp: " + err.Error()})
		return
	}
	defer func() {
		// If we error out before the rename, clean up the tmp file.
		_ = os.Remove(tmpPath)
	}()

	body := http.MaxBytesReader(w, r.Body, maxChannelFileSize)
	n, err := io.Copy(tmp, body)
	if err != nil {
		tmp.Close()
		adminCorsHeaders(w, r)
		// MaxBytesReader surfaces its own error type; surface a 413 when
		// we hit the size cap so the client can tell the difference
		// between "network died" and "file too big".
		if strings.Contains(err.Error(), "http: request body too large") {
			writeAdminJSON(w, r, http.StatusRequestEntityTooLarge, map[string]string{
				"error":   fmt.Sprintf("file exceeds %d bytes", maxChannelFileSize),
				"maxSize": fmt.Sprintf("%d", maxChannelFileSize),
			})
			return
		}
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "write body: " + err.Error()})
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "sync temp: " + err.Error()})
		return
	}
	if err := tmp.Close(); err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "close temp: " + err.Error()})
		return
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "rename: " + err.Error()})
		return
	}

	log.Printf("[hub] channels upload: %s/%s (%d bytes)", channelName, name, n)
	adminCorsHeaders(w, r)
	writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"channel": channelName,
		"name":    name,
		"size":    n,
		"path":    dstPath,
	})
}

// handleAdminChannelsPublish serves POST /api/admin/channels/publish. It
// invokes the same publishChannelManifest helper the CLI uses and returns
// the generated manifest in the response body.
func handleAdminChannelsPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed; use POST"})
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	var req struct {
		Channel string `json:"channel"`
		Tag     string `json:"tag"`
		BaseURL string `json:"baseUrl,omitempty"` // optional manifest downloadBase override
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	req.Channel = strings.TrimSpace(strings.ToLower(req.Channel))
	req.Tag = strings.TrimSpace(req.Tag)
	if req.Tag == "" {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "tag is required"})
		return
	}
	if !channel.IsValid(req.Channel) {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid channel name"})
		return
	}

	globalCfgMu.RLock()
	enabled := globalCfg.Channels.Enabled
	dataDir := globalCfg.Channels.Data
	publicURL := globalCfg.Channels.PublicURL
	globalCfgMu.RUnlock()
	if !enabled {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusConflict, map[string]string{"error": "channels.enabled is false"})
		return
	}
	if dataDir == "" {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "channels.data is not configured"})
		return
	}
	publicBase := strings.TrimSpace(req.BaseURL)
	if publicBase == "" {
		publicBase = publicURL
	}
	if publicBase == "" {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "channels.publicURL is not configured and no baseUrl override was supplied"})
		return
	}
	downloadBase := strings.TrimRight(publicBase, "/") + "/files/" + req.Channel + "/"

	m, manifestPath, err := publishChannelManifest(dataDir, req.Channel, req.Tag, downloadBase, nil)
	if err != nil {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[hub] channels publish: %s %s -> %s", req.Channel, req.Tag, manifestPath)

	adminCorsHeaders(w, r)
	writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"channel":      m.Channel,
		"tag":          m.Tag,
		"publishedAt":  m.PublishedAt,
		"downloadBase": m.DownloadBase,
		"binaries":     m.Binaries,
		"manifestPath": manifestPath,
	})
}
