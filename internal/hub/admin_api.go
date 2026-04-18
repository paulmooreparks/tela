// admin_api.go -- REST API for remote hub administration.
//
// All endpoints require Authorization: Bearer <owner-or-admin-token>.
// Changes take effect immediately (hot-reload) and are persisted to the
// YAML config file so they survive restarts.
//
// Resources:
//
//   Access (the unified RBAC view -- preferred for all permission work):
//     GET    /api/admin/access                                List all identities and permissions
//     GET    /api/admin/access/{id}                           Get one access entry
//     PATCH  /api/admin/access/{id}                           Rename: {"id":"new-name"}
//     DELETE /api/admin/access/{id}                           Remove identity entirely
//     PUT    /api/admin/access/{id}/machines/{machineId}      Set permissions  {"permissions":["connect","manage"]}
//     DELETE /api/admin/access/{id}/machines/{machineId}      Revoke all permissions on a machine
//
//   Tokens:
//     GET    /api/admin/tokens                                List all token identities
//     POST   /api/admin/tokens                                Create a new token identity (returned once)
//     POST   /api/admin/rotate/{id}                           Regenerate the token for an identity
//
//   (Token deletion goes through DELETE /api/admin/access/{id}, which also
//    scrubs the token from every machine ACL.)
//
//   Portals:
//     GET    /api/admin/portals                               List portal registrations
//     POST   /api/admin/portals                               Add or update a portal registration
//     DELETE /api/admin/portals?name=<n>                      Remove a portal registration
//
//   Pairing:
//     POST   /api/admin/pair-code                             Generate a one-time pairing code
//
//   Agents (mediated agent management; the hub forwards to telad):
//     GET    /api/admin/agents                                List registered agents
//     POST   /api/admin/agents/{machine}/{action}             Send a management action (config-get, config-set, logs, restart, update)
//
//   Hub lifecycle:
//     GET    /api/admin/logs?lines=N                          Recent hub log lines
//     POST   /api/admin/restart                               Graceful restart
//     POST   /api/admin/update                                Self-update from GitHub releases

package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

// hubChannelFetcher is the package-level cache for channel manifests used
// by hub self-update. It is shared across requests so the 5-minute cache
// actually cuts down on outbound requests.
var hubChannelFetcher = &channel.Fetcher{}

// hubChannel returns the configured channel name (defaulting to dev) and
// the resolved base URL. The base is looked up via channel.ResolveBase
// against this hub's sources map, falling back to channel.DefaultBases
// for built-in channel names.
func hubChannel() (string, string) {
	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()
	if globalCfg == nil {
		name := channel.Normalize("")
		return name, channel.ResolveBase(name, nil)
	}
	name := channel.Normalize(globalCfg.Update.Channel)
	return name, channel.ResolveBase(name, globalCfg.Update.Sources)
}

// requireOwnerOrAdmin checks the Authorization header and returns the
// caller token. Admin API always requires owner/admin auth, even on
// open hubs -- an open hub means "open for relay traffic", not "open
// for management".
func requireOwnerOrAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	callerToken := tokenFromRequest(r)
	if !globalAuth.isOwnerOrAdmin(callerToken) {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return "", false
	}
	return callerToken, true
}

// persistConfig writes the current globalCfg to disk and hot-reloads globalAuth.
func persistConfig() error {
	globalAuth.reload(&globalCfg.Auth)
	if globalCfgPath != "" {
		return writeHubConfig(globalCfgPath, globalCfg)
	}
	return nil
}

// findTokenEntryInCfg returns a pointer to the tokenEntry with the given ID, or nil.
func findTokenEntryInCfg(id string) *tokenEntry {
	for i := range globalCfg.Auth.Tokens {
		if globalCfg.Auth.Tokens[i].ID == id {
			return &globalCfg.Auth.Tokens[i]
		}
	}
	return nil
}

// ── GET /api/admin/tokens ──────────────────────────────────────────

type adminTokenView struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	TokenPreview string `json:"tokenPreview"`
}

// ── POST /api/admin/tokens ─────────────────────────────────────────

type adminAddRequest struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"` // "owner" | "admin" | "" (user)
}

type adminAddResponse struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Token string `json:"token"` // full token -- shown once
}

// ── handleAdminTokens dispatches GET (list) and POST (create).
// Token deletion goes through DELETE /api/admin/access/{id}, which
// also scrubs the token from every machine ACL in one step.

func handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		adminListTokens(w, r)
	case http.MethodPost:
		adminAddToken(w, r)
	default:
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func adminListTokens(w http.ResponseWriter, r *http.Request) {
	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()

	tokens := make([]adminTokenView, 0, len(globalCfg.Auth.Tokens))
	for _, t := range globalCfg.Auth.Tokens {
		role := t.HubRole
		if role == "" {
			role = "user"
		}
		preview := t.Token
		if len(preview) > 8 {
			preview = preview[:8] + "..."
		}
		tokens = append(tokens, adminTokenView{
			ID:           t.ID,
			Role:         role,
			TokenPreview: preview,
		})
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
}

func adminAddToken(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminAddRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id is required"})
		return
	}
	if req.Role != "" && req.Role != "owner" && req.Role != "admin" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "role must be 'owner', 'admin', or omitted"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	if findTokenEntryInCfg(req.ID) != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity already exists"})
		return
	}

	token, err := generateToken()
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "token generation failed"})
		return
	}

	globalCfg.Auth.Tokens = append(globalCfg.Auth.Tokens, tokenEntry{
		ID:      req.ID,
		Token:   token,
		HubRole: req.Role,
	})

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: added identity %q (role: %s)", req.ID, req.Role)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(adminAddResponse{
		ID:    req.ID,
		Role:  req.Role,
		Token: token,
	})
}

// ── POST /api/admin/rotate/<id> ────────────────────────────────────

func handleAdminRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	// Extract id from path: /api/admin/rotate/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/rotate/")
	if id == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id is required in URL path"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	oldToken := entry.Token
	newToken, err := generateToken()
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "token generation failed"})
		return
	}
	entry.Token = newToken

	// Cascade token change through machine ACLs
	for name, acl := range globalCfg.Auth.Machines {
		changed := false
		if acl.RegisterToken == oldToken {
			acl.RegisterToken = newToken
			changed = true
		}
		for i, ct := range acl.ConnectTokens {
			if ct == oldToken {
				acl.ConnectTokens[i] = newToken
				changed = true
			}
		}
		if changed {
			globalCfg.Auth.Machines[name] = acl
		}
	}

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	// If the console-viewer token was rotated, sync to portals
	if id == "console-viewer" {
		go syncViewerTokenToPortals()
	}

	log.Printf("[hub] admin: rotated token for %q", id)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "rotated",
		"id":     id,
		"token":  newToken,
	})
}

// ── GET/POST/DELETE /api/admin/portals ────────────────────────────

type adminPortalView struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	HubDirectory string `json:"hubDirectory"`
	HasSyncToken bool   `json:"hasSyncToken"`
}

type adminPortalAddRequest struct {
	Name        string `json:"name"`
	PortalURL   string `json:"portalUrl"`
	PortalToken string `json:"portalToken,omitempty"` // admin API token (used for registration, not persisted)
	HubURL      string `json:"hubUrl"`
}

func handleAdminPortals(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	// Support DELETE /api/admin/portals/{name} (RESTful) alongside
	// legacy DELETE /api/admin/portals?name=<n> (query param).
	pathName := strings.TrimPrefix(r.URL.Path, "/api/admin/portals/")
	if pathName == r.URL.Path || pathName == "" {
		pathName = ""
	}
	if r.Method == http.MethodDelete && pathName != "" {
		r.URL.RawQuery = "name=" + url.QueryEscape(pathName)
	}

	switch r.Method {
	case http.MethodGet:
		adminListPortals(w, r)
	case http.MethodPost:
		adminAddPortal(w, r)
	case http.MethodDelete:
		adminRemovePortal(w, r)
	default:
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func adminListPortals(w http.ResponseWriter, r *http.Request) {
	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()

	portals := make([]adminPortalView, 0)
	for name, p := range globalCfg.Portals {
		portals = append(portals, adminPortalView{
			Name:         name,
			URL:          p.URL,
			HubDirectory: p.HubDirectory,
			HasSyncToken: p.SyncToken != "",
		})
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"portals": portals})
}

func adminAddPortal(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminPortalAddRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Name == "" || req.PortalURL == "" || req.HubURL == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "name, portalUrl, and hubUrl are required"})
		return
	}

	// Determine hub name from config
	globalCfgMu.RLock()
	regHubName := globalCfg.Name
	globalCfgMu.RUnlock()
	if regHubName == "" {
		regHubName = hubName
	}
	if regHubName == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "hub name not configured (set HUB_NAME or name in config)"})
		return
	}

	viewerToken := globalAuth.consoleViewerToken()

	result, err := registerWithPortal(req.PortalURL, req.PortalToken, regHubName, req.HubURL, viewerToken)
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	globalCfgMu.Lock()
	if globalCfg.Portals == nil {
		globalCfg.Portals = make(map[string]portalEntry)
	}
	globalCfg.Portals[req.Name] = result.Entry
	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}
	globalCfgMu.Unlock()

	status := "created"
	if result.Updated {
		status = "updated"
	}

	log.Printf("[hub] admin: portal %q %s (%s)", req.Name, status, result.Entry.URL)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":       status,
		"name":         req.Name,
		"url":          result.Entry.URL,
		"hubDirectory": result.Entry.HubDirectory,
		"hasSyncToken": result.Entry.SyncToken != "",
	})
}

func adminRemovePortal(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "name query param is required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	if globalCfg.Portals == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "portal not found"})
		return
	}

	if _, exists := globalCfg.Portals[name]; !exists {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "portal not found"})
		return
	}

	delete(globalCfg.Portals, name)

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: removed portal %q", name)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed", "name": name})
}

// ── Unified Access API ───────────────────────────────────────────
//
// RESTful resource: an access entry joins a token identity with its
// effective per-machine permissions.

type accessEntry struct {
	ID           string          `json:"id"`
	Role         string          `json:"role"`
	TokenPreview string          `json:"tokenPreview"`
	Machines     []machineAccess `json:"machines"`
}

type machineAccess struct {
	MachineID   string   `json:"machineId"`
	Permissions []string `json:"permissions"`
}

// handleAdminAccess dispatches /api/admin/access and /api/admin/access/{id}...
func handleAdminAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	// Parse path: "" = list, "{id}" = single, "{id}/machines/{machineId}" = sub-resource
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/access")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		// GET /api/admin/access
		if r.Method != http.MethodGet {
			adminCorsHeaders(w, r)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		adminListAccess(w, r)
		return
	}

	// Check for /machines/ sub-resource
	parts := strings.SplitN(path, "/machines/", 2)
	id := parts[0]

	if len(parts) == 2 {
		// /api/admin/access/{id}/machines/{machineId}
		machineID := parts[1]
		if machineID == "" {
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "machineId required"})
			return
		}
		switch r.Method {
		case http.MethodPut:
			adminSetMachineAccess(w, r, id, machineID)
		case http.MethodDelete:
			adminRevokeMachineAccess(w, r, id, machineID)
		default:
			adminCorsHeaders(w, r)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/admin/access/{id}
	switch r.Method {
	case http.MethodGet:
		adminGetAccess(w, r, id)
	case http.MethodPatch:
		adminPatchAccess(w, r, id)
	case http.MethodDelete:
		adminDeleteAccess(w, r, id)
	default:
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// buildAccessEntry creates an accessEntry for a given token entry by scanning all ACLs.
// Caller must hold globalCfgMu.RLock.
func buildAccessEntry(t tokenEntry) accessEntry {
	role := t.HubRole
	if role == "" {
		role = "user"
	}
	preview := t.Token
	if len(preview) > 8 {
		preview = preview[:8] + "..."
	}

	entry := accessEntry{
		ID:           t.ID,
		Role:         role,
		TokenPreview: preview,
		Machines:     []machineAccess{},
	}

	// Owner/admin: implicit all-access
	if role == "owner" || role == "admin" {
		entry.Machines = append(entry.Machines, machineAccess{
			MachineID:   "*",
			Permissions: []string{"register", "connect", "manage"},
		})
		return entry
	}

	// Scan all machine ACLs for this token
	for machineID, acl := range globalCfg.Auth.Machines {
		var perms []string
		if acl.RegisterToken == t.Token {
			perms = append(perms, "register")
		}
		for _, ct := range acl.ConnectTokens {
			if ct == t.Token {
				perms = append(perms, "connect")
				break
			}
		}
		for _, mt := range acl.ManageTokens {
			if mt == t.Token {
				perms = append(perms, "manage")
				break
			}
		}
		if len(perms) > 0 {
			entry.Machines = append(entry.Machines, machineAccess{
				MachineID:   machineID,
				Permissions: perms,
			})
		}
	}

	return entry
}

func adminListAccess(w http.ResponseWriter, r *http.Request) {
	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()

	entries := make([]accessEntry, 0, len(globalCfg.Auth.Tokens))
	for _, t := range globalCfg.Auth.Tokens {
		entries = append(entries, buildAccessEntry(t))
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"access": entries})
}

func adminGetAccess(w http.ResponseWriter, r *http.Request, id string) {
	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()

	for _, t := range globalCfg.Auth.Tokens {
		if t.ID == id {
			adminCorsHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(buildAccessEntry(t))
			return
		}
	}

	writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
}

func adminPatchAccess(w http.ResponseWriter, r *http.Request, id string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var patch struct {
		ID string `json:"id"` // rename
	}
	if err := json.Unmarshal(body, &patch); err != nil || patch.ID == "" {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "id field required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	if patch.ID != id {
		// Check for conflict
		if findTokenEntryInCfg(patch.ID) != nil {
			writeAdminJSON(w, r, http.StatusConflict, map[string]string{"error": "identity already exists: " + patch.ID})
			return
		}
		oldID := entry.ID
		entry.ID = patch.ID
		if err := persistConfig(); err != nil {
			log.Printf("[hub] admin: persist error: %v", err)
		}
		log.Printf("[hub] admin: renamed %q to %q", oldID, patch.ID)
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated", "id": patch.ID})
}

func adminDeleteAccess(w http.ResponseWriter, r *http.Request, id string) {
	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	token := entry.Token

	// Remove from token list
	filtered := globalCfg.Auth.Tokens[:0]
	for _, t := range globalCfg.Auth.Tokens {
		if t.ID != id {
			filtered = append(filtered, t)
		}
	}
	globalCfg.Auth.Tokens = filtered

	// Scrub from all machine ACLs
	for machineID, acl := range globalCfg.Auth.Machines {
		if acl.RegisterToken == token {
			acl.RegisterToken = ""
		}
		acl.ConnectTokens = removeToken(acl.ConnectTokens, token)
		acl.ManageTokens = removeToken(acl.ManageTokens, token)
		globalCfg.Auth.Machines[machineID] = acl
	}

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: removed access entry %q", id)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed", "id": id})
}

func adminSetMachineAccess(w http.ResponseWriter, r *http.Request, id, machineID string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	if globalCfg.Auth.Machines == nil {
		globalCfg.Auth.Machines = make(map[string]machineACL)
	}
	acl := globalCfg.Auth.Machines[machineID]
	token := entry.Token

	// Remove all existing permissions for this token on this machine first
	if acl.RegisterToken == token {
		acl.RegisterToken = ""
	}
	acl.ConnectTokens = removeToken(acl.ConnectTokens, token)
	acl.ManageTokens = removeToken(acl.ManageTokens, token)

	// Apply requested permissions
	for _, perm := range req.Permissions {
		switch perm {
		case "register":
			acl.RegisterToken = token
		case "connect":
			acl.ConnectTokens = append(acl.ConnectTokens, token)
		case "manage":
			acl.ManageTokens = append(acl.ManageTokens, token)
		}
	}

	globalCfg.Auth.Machines[machineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: set %q permissions on %q to %v", id, machineID, req.Permissions)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "updated",
		"id":          id,
		"machineId":   machineID,
		"permissions": req.Permissions,
	})
}

func adminRevokeMachineAccess(w http.ResponseWriter, r *http.Request, id, machineID string) {
	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	acl, exists := globalCfg.Auth.Machines[machineID]
	if !exists {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "no ACL for machine"})
		return
	}

	token := entry.Token
	if acl.RegisterToken == token {
		acl.RegisterToken = ""
	}
	acl.ConnectTokens = removeToken(acl.ConnectTokens, token)
	acl.ManageTokens = removeToken(acl.ManageTokens, token)
	globalCfg.Auth.Machines[machineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: revoked all %q permissions on %q", id, machineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": id, "machineId": machineID})
}

// removeToken returns a new slice with all occurrences of tok removed.
func removeToken(tokens []string, tok string) []string {
	out := tokens[:0]
	for _, t := range tokens {
		if t != tok {
			out = append(out, t)
		}
	}
	return out
}

// handleAdminLogs returns recent hub log lines.
//
//	GET /api/admin/logs              Last 100 lines (default)
//	GET /api/admin/logs?lines=500    Last 500 lines
//
// Response: {"lines": ["2026-04-06T12:00:00Z [hub] ...", ...]}
// handleAdminUpdate is the self-update resource for the hub.
//
//	GET   /api/admin/update             Read current channel + status
//	PATCH /api/admin/update              Change channel: {"channel":"beta"}
//	POST  /api/admin/update              Trigger update to channel HEAD
//	POST  /api/admin/update {"version":"v0.4.0"}  Trigger to specific version
//
// GET response:
//
//	{
//	  "channel":        "dev",
//	  "manifestUrl":    "https://.../channels/dev.json",
//	  "currentVersion": "v0.4.0-dev.42",
//	  "latestVersion":  "v0.4.0-dev.43",
//	  "updateAvailable": true
//	}
func handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		ch, base := hubChannel()
		out := map[string]interface{}{
			"channel":         ch,
			"manifestUrl":     channel.ManifestURL(base, ch),
			"currentVersion":  version,
			"latestVersion":   "",
			"updateAvailable": false,
		}
		if latest, err := hubLatestRelease(); err == nil {
			out["latestVersion"] = latest
			out["updateAvailable"] = channel.IsNewer(latest, version)
		} else {
			out["error"] = err.Error()
		}
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, out)
		return

	case http.MethodPatch:
		var req struct {
			Channel      string `json:"channel"`
			ManifestBase string `json:"manifestBase"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				adminCorsHeaders(w, r)
				writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
		}
		req.Channel = strings.TrimSpace(strings.ToLower(req.Channel))
		if req.Channel != "" && !channel.IsValid(req.Channel) {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid channel name: " + req.Channel})
			return
		}
		globalCfgMu.Lock()
		if req.Channel != "" {
			globalCfg.Update.Channel = req.Channel
		}
		// PATCH semantics: only set manifestBase if the field was sent.
		// We use a sentinel here: empty means "leave alone".
		if req.ManifestBase != "" {
			globalCfg.Update.ManifestBase = req.ManifestBase
		}
		globalCfgMu.Unlock()
		if err := persistConfig(); err != nil {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "persist config: " + err.Error()})
			return
		}
		ch, base := hubChannel()
		log.Printf("[hub] update channel set to %s (manifest %s)", ch, channel.ManifestURL(base, ch))
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{
			"ok":          true,
			"channel":     ch,
			"manifestUrl": channel.ManifestURL(base, ch),
		})
		return

	case http.MethodPost:
		// fall through to update trigger
	default:
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Version string `json:"version"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	ver := req.Version

	if ver == "" || ver == "latest" {
		resolved, err := hubLatestRelease()
		if err != nil {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadGateway, map[string]string{"error": "failed to query latest release: " + err.Error()})
			return
		}
		ver = resolved
	}

	if ver == version && version != "dev" {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "message": "already running " + ver})
		return
	}

	log.Printf("[hub] update requested to %s", ver)
	adminCorsHeaders(w, r)
	writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "message": "updating to " + ver})

	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := hubDownloadAndStage(ver); err != nil {
			log.Printf("[hub] update failed: %v", err)
			return
		}
		hubRestartSelf()
	}()
}

// hubLatestRelease returns the current version on the hub's configured
// release channel by fetching that channel's manifest.
func hubLatestRelease() (string, error) {
	ch, base := hubChannel()
	m, err := hubChannelFetcher.GetURL(channel.ManifestURL(base, ch))
	if err != nil {
		return "", fmt.Errorf("fetch %s manifest: %w", ch, err)
	}
	return m.Version, nil
}

// hubDownloadAndStage downloads the given version of telahubd from the
// channel manifest and replaces the current binary. The download is
// verified against the manifest's SHA-256 before being installed.
func hubDownloadAndStage(ver string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	binaryName := fmt.Sprintf("telahubd-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	ch, base := hubChannel()
	m, err := hubChannelFetcher.GetURL(channel.ManifestURL(base, ch))
	if err != nil {
		return fmt.Errorf("fetch %s manifest: %w", ch, err)
	}
	if ver != m.Version {
		return fmt.Errorf("requested version %s is not the current %s on channel %s (which is %s); switch channel or wait for promotion", ver, ch, ch, m.Version)
	}
	entry, ok := m.Binaries[binaryName]
	if !ok {
		return fmt.Errorf("manifest for %s has no binary named %s", m.Version, binaryName)
	}
	dlURL := m.BinaryURL(binaryName)

	log.Printf("[hub] downloading %s (sha256 %s, %d bytes)", dlURL, entry.SHA256, entry.Size)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(dlURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	dir := filepath.Dir(exe)
	tmpFile, err := os.CreateTemp(dir, "telahubd-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		os.Remove(tmpPath)
	}()

	if err := channel.VerifyReader(tmpFile, resp.Body, entry.SHA256, entry.Size); err != nil {
		tmpFile.Close()
		return fmt.Errorf("verify download: %w", err)
	}
	tmpFile.Close()

	if runtime.GOOS != "windows" {
		os.Chmod(tmpPath, 0755)
	}

	if runtime.GOOS == "windows" {
		oldPath := exe + ".old"
		os.Remove(oldPath)
		if err := os.Rename(exe, oldPath); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			os.Rename(oldPath, exe)
			return fmt.Errorf("install new binary: %w", err)
		}
		go func() {
			for range 10 {
				if os.Remove(oldPath) == nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	} else {
		if err := os.Rename(tmpPath, exe); err != nil {
			return fmt.Errorf("install new binary: %w", err)
		}
	}

	log.Printf("[hub] updated telahubd to %s", ver)
	return nil
}

// hubRestartSelf re-executes the current telahubd binary.
// isDocker returns true if the process is running inside a Docker container.
func isDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// hubRestartSelf re-executes the current telahubd binary.
// Inside Docker, we just exit and let the container restart policy
// handle the re-exec (starting a child process doesn't survive
// tini's exit).
func hubRestartSelf() {
	if isDocker() {
		log.Printf("[hub] exiting for container restart")
		os.Exit(0)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[hub] restart failed: cannot find executable: %v", err)
		return
	}
	log.Printf("[hub] restarting telahubd via %s", exe)
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Start()
	os.Exit(0)
}

// handleAdminRestart triggers a graceful restart of the hub.
//
//	POST /api/admin/restart
//
// Response: {"ok":true, "message":"restarting"}
func handleAdminRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	log.Printf("[hub] restart requested via admin API")
	adminCorsHeaders(w, r)
	writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "message": "restarting"})

	go func() {
		time.Sleep(500 * time.Millisecond)
		hubRestartSelf()
	}()
}

func handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	n := 100
	if v := r.URL.Query().Get("lines"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}

	lines := snapshotLogRing(n)
	adminCorsHeaders(w, r)
	writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"lines": lines})
}
