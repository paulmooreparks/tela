// admin_api.go — REST API for remote hub auth management
//
// All endpoints require Authorization: Bearer <owner-or-admin-token>.
// Changes take effect immediately (hot-reload) and are persisted to the
// YAML config file so they survive restarts.
//
// Endpoints:
//   GET    /api/admin/tokens            List all token identities
//   POST   /api/admin/tokens            Add a new token identity
//   DELETE /api/admin/tokens?id=<id>    Remove a token identity
//   POST   /api/admin/grant             Grant connect access  {id, machineId}
//   POST   /api/admin/revoke            Revoke connect access {id, machineId}
//   POST   /api/admin/rotate/<id>       Regenerate token for identity

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

// requireOwnerOrAdmin checks the Authorization header and returns the
// caller token. If auth is disabled, returns "" with ok=true (open hub).
// If auth is enabled and caller is not owner/admin, writes 403 and returns ok=false.
func requireOwnerOrAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	callerToken := tokenFromRequest(r)
	if !globalAuth.isEnabled() {
		// Open hub — admin API still works (no protection needed)
		return callerToken, true
	}
	if !globalAuth.isOwnerOrAdmin(callerToken) {
		corsHeaders(w)
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
	Token string `json:"token"` // full token — shown once
}

// ── handleAdminTokens dispatches GET / POST / DELETE ───────────────

func handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
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
		corsHeaders(w)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func adminListTokens(w http.ResponseWriter, r *http.Request) {
	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

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

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
}

func adminAddToken(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminAddRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id is required"})
		return
	}
	if req.Role != "" && req.Role != "owner" && req.Role != "admin" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "role must be 'owner', 'admin', or omitted"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	if findTokenEntryInCfg(req.ID) != nil {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity already exists"})
		return
	}

	token, err := generateToken()
	if err != nil {
		corsHeaders(w)
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

	corsHeaders(w)
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
		corsHeaders(w)
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
		corsHeaders(w)
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

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed", "id": id})
}

// ── POST /api/admin/grant ──────────────────────────────────────────

type adminGrantRequest struct {
	ID        string `json:"id"`
	MachineID string `json:"machineId"`
}

func handleAdminGrant(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		corsHeaders(w)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		corsHeaders(w)
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
			corsHeaders(w)
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

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "granted", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/revoke ─────────────────────────────────────────

func handleAdminRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		corsHeaders(w)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	var req adminGrantRequest // same shape
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" || req.MachineID == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id and machineId are required"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(req.ID)
	if entry == nil {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	acl, exists := globalCfg.Auth.Machines[req.MachineID]
	if !exists {
		corsHeaders(w)
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
		corsHeaders(w)
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

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "id": req.ID, "machineId": req.MachineID})
}

// ── POST /api/admin/rotate/<id> ────────────────────────────────────

func handleAdminRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		corsHeaders(w)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := requireOwnerOrAdmin(w, r); !ok {
		return
	}

	// Extract id from path: /api/admin/rotate/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/rotate/")
	if id == "" {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "id is required in URL path"})
		return
	}

	globalCfgMu.Lock()
	defer globalCfgMu.Unlock()

	entry := findTokenEntryInCfg(id)
	if entry == nil {
		corsHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "identity not found"})
		return
	}

	oldToken := entry.Token
	newToken, err := generateToken()
	if err != nil {
		corsHeaders(w)
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

	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "rotated",
		"id":     id,
		"token":  newToken,
	})
}
