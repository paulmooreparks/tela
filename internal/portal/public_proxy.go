package portal

import (
	"net/http"
	"strings"
	"time"
)

// publicProxyClient is the HTTP client used to forward requests to
// upstream hub public endpoints (/api/status, /api/history). Separate
// from adminProxyClient so the timeout can be tuned independently;
// public endpoints are lightweight reads.
var publicProxyClient = &http.Client{
	Timeout: 15 * time.Second,
}

// handleHubStatusProxy proxies GET /api/hub-status/{hubName} to the
// hub's /api/status endpoint, authenticating with the stored viewer
// token (or admin token if no viewer token is stored). This lets
// TelaVisor and other portal clients read hub status without needing
// direct access to the hub's token.
//
// User auth is required. The user must be able to see the hub
// (LookupHubForUser returns it); canManage is NOT required because
// /api/status is a read-only endpoint.
func (s *Server) handleHubStatusProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHubPublicEndpoint(w, r, "/api/hub-status/", "/api/status")
}

// handleHubHistoryProxy proxies GET /api/hub-history/{hubName} to the
// hub's /api/history endpoint. Same auth model as handleHubStatusProxy.
func (s *Server) handleHubHistoryProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyHubPublicEndpoint(w, r, "/api/hub-history/", "/api/history")
}

// proxyHubPublicEndpoint is the shared implementation for hub status
// and history proxies. It extracts the hub name from the URL, looks
// up the hub, picks the best token (viewer first, then admin), and
// forwards the request to the hub's public endpoint.
func (s *Server) proxyHubPublicEndpoint(w http.ResponseWriter, r *http.Request, prefix, hubPath string) {
	hubName := strings.TrimPrefix(r.URL.Path, prefix)
	if hubName == "" {
		s.writeError(w, http.StatusBadRequest, "hub name required")
		return
	}

	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	hub, _, err := s.Store.LookupHubForUser(r.Context(), user, hubName)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Pick the best token: viewer for read-only endpoints, fall back
	// to admin if no viewer token is stored.
	token := hub.ViewerToken
	if token == "" {
		token = hub.AdminToken
	}

	target := strings.TrimRight(hub.URL, "/") + hubPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		s.logf("portal: build upstream request: %v", err)
		s.writeError(w, http.StatusInternalServerError, "build upstream request")
		return
	}
	if token != "" {
		upstream.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := publicProxyClient.Do(upstream)
	if err != nil {
		s.logf("portal: hub %q proxy GET %s: %v", hubName, target, err)
		s.writeError(w, http.StatusBadGateway, "hub unreachable")
		return
	}
	defer drainAndClose(resp.Body)

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(resp.StatusCode)
	_, _ = copyBody(w, resp.Body)
}

// handleHubToken returns the stored admin token for a hub so that
// TelaVisor can write it to the local credential store before
// launching tela connect. The token is the portal's stored admin
// credential for the hub, which has connect permission on any machine.
//
// Gated on canManage: only users who can administer the hub may
// retrieve its token. The response is {"token":"..."} with
// Cache-Control: no-store so the token is never cached.
func (s *Server) handleHubToken(w http.ResponseWriter, r *http.Request) {
	hubName := strings.TrimPrefix(r.URL.Path, "/api/hub-token/")
	if hubName == "" {
		s.writeError(w, http.StatusBadRequest, "hub name required")
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
		s.writeError(w, http.StatusNotFound, "no admin token stored for this hub")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]string{"token": hub.AdminToken})
}
