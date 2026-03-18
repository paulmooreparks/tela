// pair.go -- One-time pairing codes for secure agent and client onboarding
//
// Admins generate short-lived pairing codes (e.g., ABCD-1234) that agents
// or clients can exchange for permanent tokens without manual token copying.
//
// Two code types:
//   "register" -- agent onboarding (requires machineId, grants registerToken ACL)
//   "connect"  -- client onboarding (grants connectTokens ACL for specified machines)
//
// Endpoints:
//   POST /api/admin/pair-code     Generate pairing code (admin auth required)
//   POST /api/pair                Exchange code for token (code IS the auth)

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pairCode represents a one-time pairing code.
type pairCode struct {
	Code      string
	MachineID string   // required for "register", empty for "connect"
	Role      string   // user, admin, viewer, owner
	Type      string   // "register" or "connect"
	Machines  []string // machines the connect token can access (default ["*"])
	ExpiresAt time.Time
}

// pairStore manages in-memory pairing codes.
type pairStore struct {
	mu    sync.Mutex
	codes map[string]*pairCode
}

// globalPairStore is the global pair code store.
var globalPairStore = &pairStore{
	codes: make(map[string]*pairCode),
}

// Generate creates a new pairing code.
// Code format: XXXX-XXXX (8 alphanumeric characters with dash).
// Returns the code and its expiration time.
func (ps *pairStore) Generate(pc *pairCode, expiresIn time.Duration) (string, time.Time, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Generate 6 random bytes = 12 hex chars, formatted as XXXX-XXXX
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, fmt.Errorf("generate pairing code: %w", err)
	}

	// Format: XXXX-XXXX (alphanumeric subset)
	code := strings.ToUpper(hex.EncodeToString(buf)[:8])
	code = code[:4] + "-" + code[4:]

	expiresAt := time.Now().Add(expiresIn)
	pc.Code = code
	pc.ExpiresAt = expiresAt
	ps.codes[code] = pc

	return code, expiresAt, nil
}

// Redeem consumes a code (deletes it after validation).
// For "register" codes, expectedMachineID must match. For "connect" codes,
// expectedMachineID is ignored (pass empty string).
func (ps *pairStore) Redeem(code string, expectedMachineID string) (*pairCode, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	pc, ok := ps.codes[code]
	if !ok {
		return nil, fmt.Errorf("pairing code not found")
	}

	// Check expiration
	if time.Now().After(pc.ExpiresAt) {
		delete(ps.codes, code)
		return nil, fmt.Errorf("pairing code expired")
	}

	// Check machine ID match for register codes
	if pc.Type == "register" && pc.MachineID != expectedMachineID {
		return nil, fmt.Errorf("pairing code does not match machine ID")
	}

	// Consume the code (single-use)
	delete(ps.codes, code)
	return pc, nil
}

// CleanupExpiredCodes runs periodically to remove expired codes.
func (ps *pairStore) CleanupExpiredCodes() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now()
	for code, pc := range ps.codes {
		if now.After(pc.ExpiresAt) {
			delete(ps.codes, code)
		}
	}
}

// maxPairCodeExpiry is the maximum allowed expiry for pairing codes (7 days).
const maxPairCodeExpiry = 7 * 24 * time.Hour // 604800 seconds

// parseDuration parses a duration from either a number of seconds or a
// duration string. Supported string suffixes: "s" (seconds), "m" (minutes),
// "h" (hours), "d" (days). Plain numbers are treated as seconds.
func parseDuration(v any) (time.Duration, error) {
	switch val := v.(type) {
	case float64:
		return time.Duration(val) * time.Second, nil
	case string:
		val = strings.TrimSpace(val)
		if val == "" {
			return 0, fmt.Errorf("empty duration string")
		}
		// Check for "d" suffix (days), which time.ParseDuration does not support
		if strings.HasSuffix(val, "d") {
			numStr := strings.TrimSuffix(val, "d")
			days, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("parse duration %q: %w", val, err)
			}
			return time.Duration(days * 24 * float64(time.Hour)), nil
		}
		// Try standard Go duration parsing (supports s, m, h)
		d, err := time.ParseDuration(val)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", val, err)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("expiresIn must be a number (seconds) or duration string")
	}
}

// ── POST /api/admin/pair-code ──────────────────────────────────────────

type adminPairCodeRequest struct {
	MachineID string `json:"machineId,omitempty"` // required for "register", ignored for "connect"
	Role      string `json:"role,omitempty"`      // user, admin, viewer, owner (default: user)
	Type      string `json:"type,omitempty"`      // "register" or "connect" (default: "connect")
	// ExpiresIn is parsed separately to support both number and string formats.
	// Machines is parsed separately to allow flexible JSON input.
}

type adminPairCodeResponse struct {
	Code      string `json:"code"`
	Type      string `json:"type"`
	ExpiresAt string `json:"expiresAt"`
}

func handleAdminPairCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	callerToken, ok := requireOwnerOrAdmin(w, r)
	if !ok {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "read request"})
		return
	}

	// Parse into a generic map first to handle expiresIn flexibility
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	// Extract type (default: "connect")
	codeType := "connect"
	if t, ok := raw["type"].(string); ok && t != "" {
		codeType = t
	}
	if codeType != "register" && codeType != "connect" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "type must be \"register\" or \"connect\""})
		return
	}

	// Extract machineId
	machineID, _ := raw["machineId"].(string)
	if codeType == "register" && machineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "machineId required for register codes"})
		return
	}

	// Default role to user
	role, _ := raw["role"].(string)
	if role == "" {
		role = "user"
	}

	// Parse expiresIn (number of seconds or duration string)
	expiresIn := 10 * time.Minute // default
	if rawExp, exists := raw["expiresIn"]; exists {
		parsed, err := parseDuration(rawExp)
		if err != nil {
			adminCorsHeaders(w, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if parsed > 0 {
			expiresIn = parsed
		}
	}
	if expiresIn > maxPairCodeExpiry {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "expiresIn exceeds maximum of 7 days (604800 seconds)"})
		return
	}

	// Parse machines list (for connect codes)
	machines := []string{"*"}
	if rawMachines, exists := raw["machines"]; exists {
		if arr, ok := rawMachines.([]any); ok {
			parsed := make([]string, 0, len(arr))
			for _, m := range arr {
				if s, ok := m.(string); ok && s != "" {
					parsed = append(parsed, s)
				}
			}
			if len(parsed) > 0 {
				machines = parsed
			}
		}
	}

	pc := &pairCode{
		MachineID: machineID,
		Role:      role,
		Type:      codeType,
		Machines:  machines,
	}

	code, expiresAt, err := globalPairStore.Generate(pc, expiresIn)
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if codeType == "register" {
		log.Printf("[telahubd:pair] generated %s code %s for machine %s by %s", codeType, code, machineID, callerToken)
	} else {
		log.Printf("[telahubd:pair] generated %s code %s for machines %v by %s", codeType, code, machines, callerToken)
	}

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(adminPairCodeResponse{
		Code:      code,
		Type:      codeType,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

// ── POST /api/pair ────────────────────────────────────────────────────

type pairRequest struct {
	Code      string `json:"code"`
	MachineID string `json:"machineId,omitempty"` // required for register, optional for connect
}

type pairResponse struct {
	Token    string `json:"token"`
	Identity string `json:"identity"`
	Type     string `json:"type"`
}

func handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "read request"})
		return
	}

	var req pairRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if req.Code == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "code is required"})
		return
	}

	// Redeem the pairing code (single-use)
	// For register codes, machineId is validated inside Redeem.
	// For connect codes, machineId is ignored.
	pc, err := globalPairStore.Redeem(req.Code, req.MachineID)
	if err != nil {
		// If it was a register code and machineId was missing, the error will say
		// "does not match machine ID". Provide a clearer message if machineId is empty.
		if req.MachineID == "" && strings.Contains(err.Error(), "does not match machine ID") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "machineId is required for register codes"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Generate a permanent token
	token, err := generateToken()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "generate token"})
		return
	}

	var identity string

	globalCfgMu.Lock()
	switch pc.Type {
	case "register":
		// Agent onboarding: identity is "{machineId}-agent", registerToken ACL
		identity = fmt.Sprintf("%s-agent", pc.MachineID)
		globalCfg.Auth.Tokens = append(globalCfg.Auth.Tokens, tokenEntry{
			ID:      identity,
			Token:   token,
			HubRole: pc.Role,
		})

		if globalCfg.Auth.Machines == nil {
			globalCfg.Auth.Machines = make(map[string]machineACL)
		}
		globalCfg.Auth.Machines[pc.MachineID] = machineACL{
			RegisterToken: token,
		}

	case "connect":
		// Client onboarding: identity is "paired-user-{timestamp}", connectTokens ACL
		identity = fmt.Sprintf("paired-user-%d", time.Now().Unix())
		globalCfg.Auth.Tokens = append(globalCfg.Auth.Tokens, tokenEntry{
			ID:      identity,
			Token:   token,
			HubRole: pc.Role,
		})

		if globalCfg.Auth.Machines == nil {
			globalCfg.Auth.Machines = make(map[string]machineACL)
		}
		for _, machineID := range pc.Machines {
			acl := globalCfg.Auth.Machines[machineID]
			acl.ConnectTokens = append(acl.ConnectTokens, token)
			globalCfg.Auth.Machines[machineID] = acl
		}
	}
	globalCfgMu.Unlock()

	// Persist config
	if err := persistConfig(); err != nil {
		log.Printf("[telahubd:pair] failed to persist config: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "persist config"})
		return
	}

	log.Printf("[telahubd:pair] redeemed %s code, created identity %s", pc.Type, identity)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pairResponse{
		Token:    token,
		Identity: identity,
		Type:     pc.Type,
	})
}
