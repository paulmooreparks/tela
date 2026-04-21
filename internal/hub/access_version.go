package hub

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Per-identity monotonic version counter used for optimistic concurrency
// control on the /api/admin/access endpoints. Every mutation that touches
// an identity or one of its per-machine ACLs bumps that identity's
// counter. Clients pass the last-observed counter back as If-Match on
// mutating requests; the handler returns 412 Precondition Failed when
// the value is stale.
//
// The counter is in-memory only. A hub restart resets every version to
// the value returned by the first read after boot (initialized to 1 on
// demand). That is acceptable because ETags are scoped to a single
// operator session: a restart invalidates every stale value operators
// might still be holding, which is the conservative outcome for an
// access-control surface. Persisting the counter to disk would add a
// write on every mutation without buying anything a restart-scoped
// counter does not already give us.

var (
	accessVersionMu sync.RWMutex
	accessVersions  = map[string]uint64{}
)

// currentAccessVersion returns the identity's current version counter.
// Returns 0 when no counter has been initialized for the identity yet,
// which callers must treat as "never written" (initialize before use).
func currentAccessVersion(id string) uint64 {
	accessVersionMu.RLock()
	defer accessVersionMu.RUnlock()
	return accessVersions[id]
}

// ensureAccessVersion returns the identity's current version, creating
// it (value 1) if it does not exist. Called by read paths so that every
// GET of an identity returns a non-zero version clients can pin to.
func ensureAccessVersion(id string) uint64 {
	accessVersionMu.Lock()
	defer accessVersionMu.Unlock()
	v := accessVersions[id]
	if v == 0 {
		v = 1
		accessVersions[id] = v
	}
	return v
}

// bumpAccessVersion increments the identity's counter and returns the
// new value. Callers invoke this after every successful mutation so the
// next GET reflects the change and concurrent writers see a stale value.
func bumpAccessVersion(id string) uint64 {
	accessVersionMu.Lock()
	defer accessVersionMu.Unlock()
	v := accessVersions[id]
	if v == 0 {
		v = 1
	}
	v++
	accessVersions[id] = v
	return v
}

// renameAccessVersion carries the counter from oldID to newID. Called on
// identity rename so the version stays continuous across the rename and
// any pending If-Match the client submits for the old ID is
// reinterpreted against the new ID.
func renameAccessVersion(oldID, newID string) {
	if oldID == newID {
		return
	}
	accessVersionMu.Lock()
	defer accessVersionMu.Unlock()
	v := accessVersions[oldID]
	delete(accessVersions, oldID)
	if v == 0 {
		v = 1
	}
	// Rename is itself a mutation; bump on transfer.
	v++
	accessVersions[newID] = v
}

// deleteAccessVersion removes the counter for id. Called when the
// identity itself is deleted.
func deleteAccessVersion(id string) {
	accessVersionMu.Lock()
	defer accessVersionMu.Unlock()
	delete(accessVersions, id)
}

// parseIfMatch extracts the numeric version from an If-Match header.
// Accepts the strong ETag form `"N"`, the weak form `W/"N"`, and the
// bare form `N`. Returns 0 and ok=false when the header is absent or
// unparseable; callers treat that as "no precondition requested".
func parseIfMatch(r *http.Request) (uint64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" || raw == "*" {
		return 0, false
	}
	// Strip optional W/ prefix and surrounding quotes.
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// setETag writes the identity's current version to the ETag header in
// the strong form RFC 7232 requires (`"N"`, double-quoted).
func setETag(w http.ResponseWriter, version uint64) {
	if version == 0 {
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatUint(version, 10)+`"`)
}

// writeAccessConflict emits a 412 Precondition Failed response for a
// stale If-Match on an access mutation. The body carries the current
// server-side version and the full current accessEntry so the client
// can diff locally and present the user an accurate conflict summary
// without a follow-up round trip.
func writeAccessConflict(w http.ResponseWriter, r *http.Request, id string, currentVersion uint64, current accessEntry) {
	adminCorsHeaders(w, r)
	setETag(w, currentVersion)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPreconditionFailed)
	json.NewEncoder(w).Encode(map[string]any{
		"error":   "access entry has been modified since last read",
		"id":      id,
		"version": currentVersion,
		"current": current,
	})
}
