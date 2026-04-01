// admin_api.go -- REST API for remote hub auth and portal management
//
// All endpoints require Authorization: Bearer <owner-or-admin-token>.
// Changes take effect immediately (hot-reload) and are persisted to the
// YAML config file so they survive restarts.
//
// Unified access API (RESTful):
//   GET    /api/admin/access                           List all access entries (tokens + permissions joined)
//   GET    /api/admin/access/{id}                      Get one access entry
//   PATCH  /api/admin/access/{id}                      Update entry (rename: {"id":"new-name"})
//   DELETE /api/admin/access/{id}                      Remove identity (token + all ACL refs)
//   PUT    /api/admin/access/{id}/machines/{machineId} Set permissions  {"permissions":["connect","manage"]}
//   DELETE /api/admin/access/{id}/machines/{machineId} Revoke all permissions on a machine
//
// Legacy endpoints (kept for backward compatibility):
//   GET    /api/admin/tokens            List all token identities
//   POST   /api/admin/tokens            Add a new token identity
//   DELETE /api/admin/tokens?id=<id>    Remove a token identity
//   GET    /api/admin/acls              List all machine ACL rules
//   POST   /api/admin/grant             Grant connect access  {id, machineId}
//   POST   /api/admin/revoke            Revoke connect access {id, machineId}
//   POST   /api/admin/grant-register    Grant register access {id, machineId}
//   POST   /api/admin/revoke-register   Revoke register access {id, machineId}
//   POST   /api/admin/rotate/<id>       Regenerate token for identity
//   GET    /api/admin/portals           List portal registrations
//   POST   /api/admin/portals           Add/update a portal registration
//   DELETE /api/admin/portals?name=<n>  Remove a portal registration

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

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

// ── handleAdminTokens dispatches GET / POST / DELETE ───────────────

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
	case http.MethodDelete:
		adminRemoveToken(w, r)
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

func adminRemoveToken(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id query param is required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	found := false
	var removedToken string
	filtered := make([]tokenEntry, 0, len(globalCfg.Auth.Tokens))
	for _, t := range globalCfg.Auth.Tokens {
		if t.ID == id {
			found = true
			removedToken = t.Token
			continue
		}
		filtered = append(filtered, t)
	}
	if !found {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}
	globalCfg.Auth.Tokens = filtered

	// Scrub the removed token from all machine ACLs
	for name, acl := range globalCfg.Auth.Machines {
		changed := false
		if acl.RegisterToken == removedToken {
			acl.RegisterToken = ""
			changed = true
		}
		newCT := make([]string, 0, len(acl.ConnectTokens))
		for _, ct := range acl.ConnectTokens {
			if ct == removedToken {
				changed = true
				continue
			}
			newCT = append(newCT, ct)
		}
		if changed {
			acl.ConnectTokens = newCT
			globalCfg.Auth.Machines[name] = acl
		}
	}

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: removed identity %q", id)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed", "id": id})
}

// ── GET /api/admin/acls ───────────────────────────────────────────

type adminACLView struct {
	MachineID     string   `json:"machineId"`
	RegisterID    string   `json:"registerId,omitempty"`    // identity that can register (empty = none)
	ConnectIDs    []string `json:"connectIds"`              // identities that can connect
	ManageIDs     []string `json:"manageIds"`               // identities that can manage
}

func handleAdminACLs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		adminCorsHeaders(w, r)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	globalCfgMu.RLock()
	defer globalCfgMu.RUnlock()

	acls := make([]adminACLView, 0, len(globalCfg.Auth.Machines))
	for machineID, acl := range globalCfg.Auth.Machines {
		view := adminACLView{
			MachineID:  machineID,
			ConnectIDs: make([]string, 0, len(acl.ConnectTokens)),
			ManageIDs:  make([]string, 0, len(acl.ManageTokens)),
		}

		// Reverse-lookup register token to identity
		if acl.RegisterToken != "" {
			if id := globalAuth.identityID(acl.RegisterToken); id != "" {
				view.RegisterID = id
			}
		}

		// Reverse-lookup connect tokens to identities
		for _, ct := range acl.ConnectTokens {
			if id := globalAuth.identityID(ct); id != "" {
				view.ConnectIDs = append(view.ConnectIDs, id)
			}
		}

		// Reverse-lookup manage tokens to identities
		for _, mt := range acl.ManageTokens {
			if id := globalAuth.identityID(mt); id != "" {
				view.ManageIDs = append(view.ManageIDs, id)
			}
		}

		acls = append(acls, view)
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"acls": acls})
}

// ── POST /api/admin/grant-register ───────────────────────────────

func handleAdminGrantRegister(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	if globalCfg.Auth.Machines == nil {
		globalCfg.Auth.Machines = make(map[string]machineACL)
	}
	acl := globalCfg.Auth.Machines[req.MachineID]

	if acl.RegisterToken == entry.Token {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_granted", "id": req.ID, "machineId": req.MachineID})
		return
	}

	acl.RegisterToken = entry.Token
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: granted %q register to %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "granted", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/revoke-register ──────────────────────────────

func handleAdminRevokeRegister(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	acl, exists := globalCfg.Auth.Machines[req.MachineID]
	if !exists || acl.RegisterToken != entry.Token {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity does not have register access to this machine"})
		return
	}

	acl.RegisterToken = ""
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: revoked %q register from %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/grant ──────────────────────────────────────────

type adminGrantRequest struct {
	ID        string `json:"id"`
	MachineID string `json:"machineId"`
}

func handleAdminGrant(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	if globalCfg.Auth.Machines == nil {
		globalCfg.Auth.Machines = make(map[string]machineACL)
	}
	acl := globalCfg.Auth.Machines[req.MachineID]

	// Check if already granted
	for _, ct := range acl.ConnectTokens {
		if ct == entry.Token {
			adminCorsHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "already_granted"})
			return
		}
	}

	acl.ConnectTokens = append(acl.ConnectTokens, entry.Token)
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: granted %q connect to %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "granted", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/revoke ─────────────────────────────────────────

func handleAdminRevoke(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest // same shape
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	acl, exists := globalCfg.Auth.Machines[req.MachineID]
	if !exists {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no ACL for machine"})
		return
	}

	found := false
	newCT := make([]string, 0, len(acl.ConnectTokens))
	for _, ct := range acl.ConnectTokens {
		if ct == entry.Token {
			found = true
			continue
		}
		newCT = append(newCT, ct)
	}
	if !found {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity does not have access to this machine"})
		return
	}

	acl.ConnectTokens = newCT
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: revoked %q from %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": req.ID, "machineId": req.MachineID})
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

// ── POST /api/admin/grant-manage ───────────────────────────────────

func handleAdminGrantManage(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	if globalCfg.Auth.Machines == nil {
		globalCfg.Auth.Machines = make(map[string]machineACL)
	}
	acl := globalCfg.Auth.Machines[req.MachineID]

	for _, mt := range acl.ManageTokens {
		if mt == entry.Token {
			adminCorsHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "already_granted"})
			return
		}
	}

	acl.ManageTokens = append(acl.ManageTokens, entry.Token)
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: granted %q manage on %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "granted", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/revoke-manage ──────────────────────────────────

func handleAdminRevokeManage(w http.ResponseWriter, r *http.Request) {
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

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	if globalCfg.Auth.Machines == nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "not_found"})
		return
	}
	acl := globalCfg.Auth.Machines[req.MachineID]

	filtered := make([]string, 0, len(acl.ManageTokens))
	for _, mt := range acl.ManageTokens {
		if mt != entry.Token {
			filtered = append(filtered, mt)
		}
	}
	acl.ManageTokens = filtered
	globalCfg.Auth.Machines[req.MachineID] = acl

	if err := persistConfig(); err != nil {
		log.Printf("[hub] admin: persist error: %v", err)
	}

	log.Printf("[hub] admin: revoked %q manage on %q", req.ID, req.MachineID)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": req.ID, "machineId": req.MachineID})
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
