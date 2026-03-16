// pair.go -- One-time pairing codes for secure agent onboarding
//
// Admins generate short-lived pairing codes (e.g., ABCD-1234) that agents
// can exchange for permanent tokens without manual token copying.
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
	"strings"
	"sync"
	"time"
)

// pairCode represents a one-time pairing code.
type pairCode struct {
	Code      string
	MachineID string
	Role      string    // user, admin, viewer, owner
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
func (ps *pairStore) Generate(machineID string, role string, expiresIn time.Duration) (string, time.Time, error) {
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
	ps.codes[code] = &pairCode{
		Code:      code,
		MachineID: machineID,
		Role:      role,
		ExpiresAt: expiresAt,
	}

	return code, expiresAt, nil
}

// Validate checks if a code is valid and returns it.
// Expired codes are deleted during the check.
func (ps *pairStore) Validate(code string, expectedMachineID string) (*pairCode, error) {
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

	// Check machine ID match
	if pc.MachineID != expectedMachineID {
		return nil, fmt.Errorf("pairing code does not match machine ID")
	}

	return pc, nil
}

// Redeem consumes a code (deletes it after validation).
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

	// Check machine ID match
	if pc.MachineID != expectedMachineID {
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

// ── POST /api/admin/pair-code ──────────────────────────────────────────

type adminPairCodeRequest struct {
	MachineID string `json:"machineId"`
	Role      string `json:"role,omitempty"` // user, admin, viewer, owner (default: user)
	ExpiresIn int    `json:"expiresIn"`      // seconds (default: 600 / 10 minutes)
}

type adminPairCodeResponse struct {
	Code      string `json:"code"`
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

	var req adminPairCodeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if req.MachineID == "" {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "machineId required"})
		return
	}

	// Default role to user
	role := req.Role
	if role == "" {
		role = "user"
	}

	// Default expiry to 10 minutes
	expiresIn := time.Duration(req.ExpiresIn) * time.Second
	if expiresIn == 0 {
		expiresIn = 10 * time.Minute
	}

	code, expiresAt, err := globalPairStore.Generate(req.MachineID, role, expiresIn)
	if err != nil {
		adminCorsHeaders(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[telahubd:pair] generated code %s for machine %s by %s", code, req.MachineID, callerToken)

	adminCorsHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(adminPairCodeResponse{
		Code:      code,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}

// ── POST /api/pair ────────────────────────────────────────────────────

type pairRequest struct {
	Code      string `json:"code"`
	MachineID string `json:"machineId"`
}

type pairResponse struct {
	Token    string `json:"token"`
	Identity string `json:"identity"`
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

	if req.Code == "" || req.MachineID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "code and machineId required"})
		return
	}

	// Redeem the pairing code (single-use)
	pc, err := globalPairStore.Redeem(req.Code, req.MachineID)
	if err != nil {
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

	// Create token identity (e.g., "barn-agent")
	identity := fmt.Sprintf("%s-agent", pc.MachineID)

	// Add to config
	globalAuth.mu.Lock()
	globalCfg.Auth.Tokens = append(globalCfg.Auth.Tokens, tokenEntry{
		ID:      identity,
		Token:   token,
		HubRole: pc.Role,
	})

	// Set machine register ACL for this token
	if globalCfg.Auth.Machines == nil {
		globalCfg.Auth.Machines = make(map[string]machineACL)
	}
	globalCfg.Auth.Machines[pc.MachineID] = machineACL{
		RegisterToken: token,
	}
	globalAuth.mu.Unlock()

	// Persist config
	if err := persistConfig(); err != nil {
		log.Printf("[telahubd:pair] failed to persist config: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "persist config"})
		return
	}

	log.Printf("[telahubd:pair] redeemed code for machine %s, created identity %s", pc.MachineID, identity)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pairResponse{
		Token:    token,
		Identity: identity,
	})
}
