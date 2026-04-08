package portal

import (
	"net/http"
	"strings"
	"time"
)

// adminProxyClient is the HTTP client used to forward requests to
// upstream hubs. Defined at the package level so tests can swap it
// out and so the timeout is configurable in one place.
//
// 60s is generous: most admin endpoints respond in milliseconds, but
// hub log retrieval and update triggers can take a few seconds, and
// the proxy should not preempt them. A faster client at the request
// edge would be a separate concern (rate limiting, request budgets).
var adminProxyClient = &http.Client{
	Timeout: 60 * time.Second,
}

// handleAdminProxy implements section 4 of DESIGN-portal.md: the
// generic admin proxy that forwards an authenticated user's request
// to a hub's admin API.
//
// URL shape: /api/hub-admin/{hubName}/{operation} where {operation}
// is the hub admin path WITHOUT the leading /api/admin/ prefix. The
// proxy internally prepends /api/admin/ before forwarding. The
// legacy double-prefix form (/api/hub-admin/{hubName}/api/admin/...)
// is rejected with 400.
//
// Method, body, and query string are forwarded byte-for-byte. The
// proxy MUST NOT downgrade verbs (the recent Awan Saya bug where
// PATCH was being collapsed to POST is now a spec-mandated rule);
// upstream Tela hubs use real REST verbs and the proxy preserves
// them.
//
// Authorization is two-stage: user must be authenticated AND have
// canManage on the named hub. A user who can see a hub but cannot
// manage it gets 403; a user who cannot see the hub at all gets 404
// (the spec forbids leaking hub existence to unauthorized users).
func (s *Server) handleAdminProxy(w http.ResponseWriter, r *http.Request) {
	// URL is /api/hub-admin/{hubName}/{operation}
	rest := strings.TrimPrefix(r.URL.Path, "/api/hub-admin/")
	if rest == r.URL.Path {
		s.writeError(w, http.StatusBadRequest, "expected /api/hub-admin/{hubName}/{operation}")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		s.writeError(w, http.StatusBadRequest, "expected /api/hub-admin/{hubName}/{operation}")
		return
	}
	hubName, operation := parts[0], parts[1]

	// Refuse the legacy double-prefix form. A spec-conformant client
	// uses /api/hub-admin/myhub/access, not
	// /api/hub-admin/myhub/api/admin/access.
	if strings.HasPrefix(operation, "api/admin/") || operation == "api/admin" {
		s.writeError(w, http.StatusBadRequest,
			"legacy double-prefix admin proxy form is not supported in protocol version 1.0; use /api/hub-admin/{hubName}/{operation}")
		return
	}

	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	hub, canManage, err := s.Store.LookupHubForUser(r.Context(), user, hubName)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !canManage {
		s.writeError(w, http.StatusForbidden, "manage permission required")
		return
	}
	if hub.AdminToken == "" {
		s.writeError(w, http.StatusBadRequest, "no admin token stored for this hub")
		return
	}

	// Build the upstream URL.
	target := strings.TrimRight(hub.URL, "/") + "/api/admin/" + operation
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	// Forward the request. Body is passed through unchanged for
	// methods that have one.
	upstream, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		s.logf("portal: build upstream request: %v", err)
		s.writeError(w, http.StatusInternalServerError, "build upstream request")
		return
	}
	// Replace the inbound Authorization header with the stored
	// admin token. The inbound header carried the user's session
	// credential, which is meaningless to the upstream hub.
	upstream.Header.Set("Authorization", "Bearer "+hub.AdminToken)
	if ct := r.Header.Get("Content-Type"); ct != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		upstream.Header.Set("Content-Type", ct)
	}

	resp, err := adminProxyClient.Do(upstream)
	if err != nil {
		s.logf("portal: hub %q proxy %s %s: %v", hubName, r.Method, target, err)
		s.writeError(w, http.StatusBadGateway, "hub unreachable")
		return
	}
	defer drainAndClose(resp.Body)

	// Mirror the upstream response. Content-Type is preserved if
	// the hub set one; otherwise default to JSON. Cache-Control is
	// always no-cache because admin responses are not safe to
	// cache anywhere.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(resp.StatusCode)
	// Best-effort copy. If the client disconnects mid-stream, both
	// the source and destination errors are visible to ResponseWriter.
	_, _ = copyBody(w, resp.Body)
}

// copyBody is a small wrapper that handles the io.Copy and lets the
// proxy handler stay terse. Defined as a function (not inlined) so
// future iterations can add a max-size guard or per-byte accounting
// without disturbing the call site.
func copyBody(dst http.ResponseWriter, src interface{ Read(p []byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}
