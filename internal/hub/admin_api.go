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

// adminAddToken returns an inline JSON map {id, role, token, version}
// directly rather than a named struct; the token field is the full
// secret value and is shown exactly once so the client can capture it.

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

	version := ensureAccessVersion(req.ID)

	log.Printf("[hub] admin: added identity %q (role: %s)", req.ID, req.Role)

	adminCorsHeaders(w, r)
	setETag(w, version)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":      req.ID,
		"role":    req.Role,
		"token":   token,
		"version": version,
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

	if ifm, ok := parseIfMatch(r); ok {
		current := ensureAccessVersion(id)
		if ifm != current {
			writeAccessConflict(w, r, id, current, buildAccessEntry(*entry))
			return
		}
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
		if replaceConnectGrantToken(acl.ConnectTokens, oldToken, newToken) {
			changed = true
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

	newVersion := bumpAccessVersion(id)

	log.Printf("[hub] admin: rotated token for %q", id)

	adminCorsHeaders(w, r)
	setETag(w, newVersion)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "rotated",
		"id":      id,
		"token":   newToken,
		"version": newVersion,
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
	// Version is the identity's current monotonic version counter. Clients
	// echo it back as If-Match on mutating requests to detect drift. See
	// internal/hub/access_version.go for lifecycle details.
	Version uint64 `json:"version"`
	// WildcardInherited enumerates the permissions the wildcard "*" ACL
	// entry cascades to every machine that does not appear explicitly in
	// Machines. The hub's canConnect / canManage routines honor this
	// cascade; the field is derived from the same source so clients can
	// render the effective per-machine state without re-implementing
	// the cascade rules. Empty when the identity has no wildcard entry
	// or its wildcard entry grants no cascading permissions. Owner,
	// admin, and viewer identities have role-based implicit access
	// rather than ACL-based cascade; for them the list is always empty
	// and the implicit grant is represented by a synthetic "*" entry
	// in Machines.
	WildcardInherited []string `json:"wildcardInherited,omitempty"`
	// WildcardInheritedServices is the services filter attached to the
	// wildcard connect grant. Empty means "all services". Ignored when
	// WildcardInherited does not contain "connect".
	WildcardInheritedServices []string `json:"wildcardInheritedServices,omitempty"`
}

type machineAccess struct {
	MachineID   string   `json:"machineId"`
	Permissions []string `json:"permissions"`
	// Services, when non-empty, restricts the connect grant on this
	// machine to the named services. Omitted (nil/absent in JSON) or
	// empty means "all services" (the 0.14 behavior). Applies only to
	// the connect permission; register and manage are unaffected.
	Services []string `json:"services,omitempty"`
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
		Version:      ensureAccessVersion(t.ID),
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
		var services []string
		if acl.RegisterToken == t.Token {
			perms = append(perms, "register")
		}
		if grant, ok := findConnectGrant(acl.ConnectTokens, t.Token); ok {
			perms = append(perms, "connect")
			if len(grant.Services) > 0 {
				services = append([]string(nil), grant.Services...)
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
				Services:    services,
			})
		}
	}

	// Compute wildcard-cascade metadata. The hub's canConnect and
	// canManage check the wildcard entry as a fallback when a machine-
	// specific grant is absent; we surface which permissions that
	// fallback covers so clients render specific-machine rows
	// consistently with the hub's own authorization decisions rather
	// than re-implementing the cascade rules.
	if wildACL, ok := globalCfg.Auth.Machines["*"]; ok {
		if grant, gOk := findConnectGrant(wildACL.ConnectTokens, t.Token); gOk {
			entry.WildcardInherited = append(entry.WildcardInherited, "connect")
			if len(grant.Services) > 0 {
				entry.WildcardInheritedServices = append([]string(nil), grant.Services...)
			}
		}
		for _, mt := range wildACL.ManageTokens {
			if mt == t.Token {
				entry.WildcardInherited = append(entry.WildcardInherited, "manage")
				break
			}
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
			entry := buildAccessEntry(t)
			adminCorsHeaders(w, r)
			setETag(w, entry.Version)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(entry)
			return
		}
	}

	writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
}

// adminPatchAccess applies partial updates to an identity. The patch
// body may carry an `id` field (rename), a `role` field (role change),
// or both. Empty body or neither field is a 400. Role must be one of
// "owner", "admin", "viewer", or "" (user, the default). Demoting the
// sole owner is rejected with 409 so the hub never ends up without an
// owner identity.
func adminPatchAccess(w http.ResponseWriter, r *http.Request, id string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var patch struct {
		ID   *string `json:"id,omitempty"`
		Role *string `json:"role,omitempty"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if patch.ID == nil && patch.Role == nil {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "specify at least one of id or role"})
		return
	}
	if patch.Role != nil {
		switch *patch.Role {
		case "", "owner", "admin", "viewer":
			// ok
		default:
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "role must be one of owner, admin, viewer, or empty (user)"})
			return
		}
	}
	if patch.ID != nil && *patch.ID == "" {
		writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "id cannot be empty"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	if ifm, ok := parseIfMatch(r); ok {
		current := ensureAccessVersion(id)
		if ifm != current {
			writeAccessConflict(w, r, id, current, buildAccessEntry(*entry))
			return
		}
	}

	resultID := id
	renamed := false
	if patch.ID != nil && *patch.ID != id {
		if findTokenEntryInCfg(*patch.ID) != nil {
			writeAdminJSON(w, r, http.StatusConflict, map[string]string{"error": "identity already exists: " + *patch.ID})
			return
		}
		entry.ID = *patch.ID
		resultID = *patch.ID
		renamed = true
	}

	if patch.Role != nil && *patch.Role != entry.HubRole {
		newRole := *patch.Role
		// Refuse to demote the last owner. Iterating once is cheap
		// given the number of identities a hub holds, and guarding
		// here keeps the check colocated with the mutation.
		if entry.HubRole == "owner" && newRole != "owner" {
			owners := 0
			for _, t := range globalCfg.Auth.Tokens {
				if t.HubRole == "owner" {
					owners++
				}
			}
			if owners <= 1 {
				writeAdminJSON(w, r, http.StatusConflict, map[string]string{"error": "cannot demote the sole owner; promote another identity first"})
				return
			}
		}
		entry.HubRole = newRole
	}

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	var resultVersion uint64
	if renamed {
		renameAccessVersion(id, resultID)
		resultVersion = currentAccessVersion(resultID)
		log.Printf("[hub] admin: renamed %q to %q", id, resultID)
	} else {
		resultVersion = bumpAccessVersion(id)
	}
	if patch.Role != nil {
		log.Printf("[hub] admin: set %q role to %q", resultID, *patch.Role)
	}

	adminCorsHeaders(w, r)
	setETag(w, resultVersion)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "updated",
		"id":      resultID,
		"version": resultVersion,
	})
}

func adminDeleteAccess(w http.ResponseWriter, r *http.Request, id string) {
	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	if ifm, ok := parseIfMatch(r); ok {
		current := ensureAccessVersion(id)
		if ifm != current {
			writeAccessConflict(w, r, id, current, buildAccessEntry(*entry))
			return
		}
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
		acl.ConnectTokens = removeConnectGrant(acl.ConnectTokens, token)
		acl.ManageTokens = removeToken(acl.ManageTokens, token)
		globalCfg.Auth.Machines[machineID] = acl
	}

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	deleteAccessVersion(id)

	log.Printf("[hub] admin: removed access entry %q", id)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed", "id": id})
}

func adminSetMachineAccess(w http.ResponseWriter, r *http.Request, id, machineID string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req struct {
		Permissions []string `json:"permissions"`
		// Services, when present and non-empty, attaches a per-service
		// filter to the connect permission. nil / absent / empty means
		// "all services" (the pre-0.15 behavior). Ignored when connect
		// is not in Permissions.
		Services []string `json:"services,omitempty"`
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

	if ifm, ok := parseIfMatch(r); ok {
		current := ensureAccessVersion(id)
		if ifm != current {
			writeAccessConflict(w, r, id, current, buildAccessEntry(*entry))
			return
		}
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
	acl.ConnectTokens = removeConnectGrant(acl.ConnectTokens, token)
	acl.ManageTokens = removeToken(acl.ManageTokens, token)

	// Apply requested permissions. The connect grant carries the
	// services filter (if any) from the request body; register and
	// manage are unaffected by Services.
	for _, perm := range req.Permissions {
		switch perm {
		case "register":
			acl.RegisterToken = token
		case "connect":
			grant := connectGrant{Token: token}
			if len(req.Services) > 0 {
				grant.Services = append([]string(nil), req.Services...)
			}
			acl.ConnectTokens = append(acl.ConnectTokens, grant)
		case "manage":
			acl.ManageTokens = append(acl.ManageTokens, token)
		}
	}

	globalCfg.Auth.Machines[machineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	newVersion := bumpAccessVersion(id)

	log.Printf("[hub] admin: set %q permissions on %q to %v (services=%v)", id, machineID, req.Permissions, req.Services)

	adminCorsHeaders(w, r)
	setETag(w, newVersion)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"status":      "updated",
		"id":          id,
		"machineId":   machineID,
		"permissions": req.Permissions,
		"version":     newVersion,
	}
	if len(req.Services) > 0 {
		resp["services"] = req.Services
	}
	json.NewEncoder(w).Encode(resp)
}

func adminRevokeMachineAccess(w http.ResponseWriter, r *http.Request, id, machineID string) {
	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "identity not found"})
		return
	}

	if ifm, ok := parseIfMatch(r); ok {
		current := ensureAccessVersion(id)
		if ifm != current {
			writeAccessConflict(w, r, id, current, buildAccessEntry(*entry))
			return
		}
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
	acl.ConnectTokens = removeConnectGrant(acl.ConnectTokens, token)
	acl.ManageTokens = removeToken(acl.ManageTokens, token)
	globalCfg.Auth.Machines[machineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	newVersion := bumpAccessVersion(id)

	log.Printf("[hub] admin: revoked all %q permissions on %q", id, machineID)

	adminCorsHeaders(w, r)
	setETag(w, newVersion)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "revoked",
		"id":        id,
		"machineId": machineID,
		"version":   newVersion,
	})
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
		globalCfgMu.RLock()
		sources := map[string]string{}
		for k, v := range globalCfg.Update.Sources {
			sources[k] = v
		}
		globalCfgMu.RUnlock()
		out := map[string]interface{}{
			"channel":         ch,
			"manifestUrl":     channel.ManifestURL(base, ch),
			"currentVersion":  version,
			"latestVersion":   "",
			"updateAvailable": false,
			"sources":         sources,
		}
		if latest, err := hubLatestRelease(); err == nil {
			out["latestVersion"] = latest
			out["updateAvailable"] = channel.ShouldOfferUpdate(version, ch, latest)
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
		// PATCH semantics: a non-empty manifestBase in the request is
		// redirected into Sources[channel] so the legacy '-manifest-base'
		// CLI flag still works after the deprecated config field was
		// removed in #59. Operators authoring fresh configs should use
		// 'channel sources set' instead.
		if req.ManifestBase != "" {
			ch := req.Channel
			if ch == "" {
				ch = globalCfg.Update.Channel
			}
			if ch != "" {
				if globalCfg.Update.Sources == nil {
					globalCfg.Update.Sources = map[string]string{}
				}
				globalCfg.Update.Sources[ch] = req.ManifestBase
			}
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

	explicitVersion := req.Version != "" && req.Version != "latest"

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

	// Downgrade refusal on the channel-HEAD path: if the caller didn't pin a
	// specific version and the channel's HEAD is older than what's running,
	// refuse rather than silently downgrade. Only applies when staying on
	// the same channel; a channel switch is an explicit declaration of
	// intent to follow the new HEAD regardless of how the semver sort
	// orders the two lineages. An explicit version also bypasses the gate.
	targetCh, _ := hubChannel()
	if !explicitVersion && version != "dev" && !channel.IsCrossChannel(version, targetCh) && !channel.IsNewer(ver, version) {
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("latest version on channel is %s, older than currently running %s; specify an explicit version to downgrade", ver, version),
		})
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

// handleAdminUpdateSources handles CRUD on the hub's update.sources map.
//
//	GET    /api/admin/update/sources              List all sources
//	PUT    /api/admin/update/sources/{name}       Set: {"base":"..."}
//	DELETE /api/admin/update/sources/{name}       Remove a source
//
// All operations require owner or admin auth and are persisted to YAML
// (hot-reload via persistConfig). Built-in channels (dev/beta/stable)
// remain available even after their entry is removed; they fall back to
// the baked-in default. Custom channel names become unresolvable when
// their entry is removed.
func handleAdminUpdateSources(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	// Path is one of:
	//   /api/admin/update/sources         (list, GET only)
	//   /api/admin/update/sources/{name}  (set/remove)
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/update/sources")
	rest = strings.TrimPrefix(rest, "/")
	name := strings.TrimSpace(strings.ToLower(rest))

	switch r.Method {
	case http.MethodGet:
		if name != "" {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "GET /api/admin/update/sources/{name} not supported; GET the collection instead"})
			return
		}
		globalCfgMu.RLock()
		out := map[string]string{}
		for k, v := range globalCfg.Update.Sources {
			out[k] = v
		}
		globalCfgMu.RUnlock()
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"sources": out})
		return

	case http.MethodPut:
		if name == "" {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "PUT requires /api/admin/update/sources/{name}"})
			return
		}
		if !channel.IsValid(name) {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid channel name: " + name})
			return
		}
		var req struct {
			Base string `json:"base"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				adminCorsHeaders(w, r)
				writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
		}
		base := strings.TrimRight(strings.TrimSpace(req.Base), "/")
		if strings.HasSuffix(base, ".json") {
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[:i]
			}
		}
		globalCfgMu.Lock()
		if globalCfg.Update.Sources == nil {
			globalCfg.Update.Sources = map[string]string{}
		}
		globalCfg.Update.Sources[name] = base
		globalCfgMu.Unlock()
		if err := persistConfig(); err != nil {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "persist config: " + err.Error()})
			return
		}
		log.Printf("[hub] sources[%s] = %s", name, base)
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "name": name, "base": base})
		return

	case http.MethodDelete:
		if name == "" {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusBadRequest, map[string]string{"error": "DELETE requires /api/admin/update/sources/{name}"})
			return
		}
		globalCfgMu.Lock()
		_, exists := globalCfg.Update.Sources[name]
		delete(globalCfg.Update.Sources, name)
		globalCfgMu.Unlock()
		if !exists {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusNotFound, map[string]string{"error": "no source entry for channel " + name})
			return
		}
		if err := persistConfig(); err != nil {
			adminCorsHeaders(w, r)
			writeAdminJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "persist config: " + err.Error()})
			return
		}
		log.Printf("[hub] sources[%s] removed", name)
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "name": name})
		return

	default:
		adminCorsHeaders(w, r)
		writeAdminJSON(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
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
	// Install paths bypass the manifest cache. See the agent-side note
	// in downloadAndStageUpdate.
	m, err := hubChannelFetcher.Fetch(channel.ManifestURL(base, ch))
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
