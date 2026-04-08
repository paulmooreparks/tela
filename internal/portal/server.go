package portal

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

// Server wraps a Store and exposes an http.Handler that implements
// the portal protocol described in DESIGN-portal.md.
//
// Construct with NewServer; mount the result with Server.Handler().
// The same Server can serve multiple concurrent requests; the
// underlying Store is responsible for its own concurrency.
type Server struct {
	Store Store

	// Logger receives notice-level events: failed requests, upstream
	// errors, etc. If nil, the package writes through the standard
	// log package.
	Logger *log.Logger
}

// NewServer constructs a Server backed by store. The returned Server
// is ready to serve requests after the caller mounts Handler() on a
// listener.
func NewServer(store Store) *Server {
	return &Server{Store: store}
}

// Handler returns the http.Handler that implements the portal
// protocol. The returned handler is safe to use from multiple
// goroutines and is the value the caller mounts on a listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Discovery (no auth, GET/HEAD only).
	mux.HandleFunc("GET /.well-known/tela", s.handleWellKnown)
	mux.HandleFunc("HEAD /.well-known/tela", s.handleWellKnown)

	// Directory (user auth on every endpoint).
	mux.HandleFunc("GET "+HubDirectoryPath, s.handleHubsList)
	mux.HandleFunc("POST "+HubDirectoryPath, s.handleHubsAdd)
	mux.HandleFunc("PATCH "+HubDirectoryPath, s.handleHubsUpdate)
	mux.HandleFunc("DELETE "+HubDirectoryPath, s.handleHubsDelete)

	// Hub-driven sync (sync token auth, NOT user auth).
	mux.HandleFunc("PATCH "+HubDirectoryPath+"/sync", s.handleHubSync)

	// Admin proxy (user auth, gated on canManage).
	mux.HandleFunc("/api/hub-admin/", s.handleAdminProxy)

	// Fleet aggregation (user auth).
	mux.HandleFunc("GET /api/fleet/agents", s.handleFleetAgents)

	return mux
}

// ── Discovery ──────────────────────────────────────────────────────

// wellKnownResponse is the JSON shape returned by /.well-known/tela.
// Defined as a struct (not a map literal) so the field order is
// stable in the JSON output and the type is greppable.
type wellKnownResponse struct {
	HubDirectory      string   `json:"hub_directory"`
	ProtocolVersion   string   `json:"protocolVersion"`
	SupportedVersions []string `json:"supportedVersions"`
}

func (s *Server) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	body := wellKnownResponse{
		HubDirectory:      HubDirectoryPath,
		ProtocolVersion:   ProtocolVersion,
		SupportedVersions: SupportedVersions,
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// ── Directory: GET /api/hubs ────────────────────────────────────────

// hubsListResponse is the JSON shape returned by GET /api/hubs and
// also by POST/PATCH/DELETE on /api/hubs (which all return the
// post-mutation list as their primary payload).
type hubsListResponse struct {
	Hubs []HubVisibility `json:"hubs"`
}

func (s *Server) handleHubsList(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	hubs, err := s.Store.ListHubsForUser(r.Context(), user)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, hubsListResponse{Hubs: hubs})
}

// ── Directory: POST /api/hubs ───────────────────────────────────────

// addHubRequest is the JSON shape POST /api/hubs accepts.
type addHubRequest struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	ViewerToken string `json:"viewerToken"`
	AdminToken  string `json:"adminToken"`
	OrgName     string `json:"orgName"`
}

// addHubResponse is the JSON shape POST /api/hubs returns. The Hubs
// field is the post-mutation list. SyncToken is the cleartext sync
// token returned exactly once when a hub is registered (omitted in
// user-add mode by stores that distinguish the two contexts).
// Updated is true on upsert.
type addHubResponse struct {
	Hubs      []HubVisibility `json:"hubs"`
	SyncToken string          `json:"syncToken,omitempty"`
	Updated   bool            `json:"updated,omitempty"`
}

func (s *Server) handleHubsAdd(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	var body addHubRequest
	if err := decodeJSONBody(r, &body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" || body.URL == "" {
		s.writeError(w, http.StatusBadRequest, "name and url are required")
		return
	}

	created, syncToken, err := s.Store.AddHub(r.Context(), user, Hub{
		Name:        body.Name,
		URL:         body.URL,
		ViewerToken: body.ViewerToken,
		AdminToken:  body.AdminToken,
		OrgName:     body.OrgName,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	hubs, err := s.Store.ListHubsForUser(r.Context(), user)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	resp := addHubResponse{
		Hubs:      hubs,
		SyncToken: syncToken,
		Updated:   !created,
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ── Directory: PATCH /api/hubs ─────────────────────────────────────

// updateHubRequest is the JSON shape PATCH /api/hubs accepts. The
// pointer-typed fields encode "field absent" vs "field present" so a
// caller can change one field without resetting the others.
type updateHubRequest struct {
	CurrentName string  `json:"currentName"`
	Name        *string `json:"name,omitempty"`
	URL         *string `json:"url,omitempty"`
	ViewerToken *string `json:"viewerToken,omitempty"`
	AdminToken  *string `json:"adminToken,omitempty"`
}

func (s *Server) handleHubsUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	var body updateHubRequest
	if err := decodeJSONBody(r, &body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.CurrentName == "" {
		s.writeError(w, http.StatusBadRequest, "currentName is required")
		return
	}

	update := HubUpdate{
		Name:        body.Name,
		URL:         body.URL,
		ViewerToken: body.ViewerToken,
		AdminToken:  body.AdminToken,
	}
	if err := s.Store.UpdateHub(r.Context(), user, body.CurrentName, update); err != nil {
		s.writeStoreError(w, err)
		return
	}

	hubs, err := s.Store.ListHubsForUser(r.Context(), user)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, hubsListResponse{Hubs: hubs})
}

// ── Directory: DELETE /api/hubs?name= ──────────────────────────────

func (s *Server) handleHubsDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "name query parameter is required")
		return
	}

	if err := s.Store.DeleteHub(r.Context(), user, name); err != nil {
		s.writeStoreError(w, err)
		return
	}

	hubs, err := s.Store.ListHubsForUser(r.Context(), user)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, hubsListResponse{Hubs: hubs})
}

// ── Hub-driven sync: PATCH /api/hubs/sync ──────────────────────────

// syncHubRequest is the JSON shape PATCH /api/hubs/sync accepts.
// Authentication is via a sync token in the Authorization header,
// not via user session.
type syncHubRequest struct {
	Name        string `json:"name"`
	ViewerToken string `json:"viewerToken"`
}

// syncHubResponse is the JSON shape PATCH /api/hubs/sync returns.
type syncHubResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) handleHubSync(w http.ResponseWriter, r *http.Request) {
	var body syncHubRequest
	if err := decodeJSONBody(r, &body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" || body.ViewerToken == "" {
		s.writeError(w, http.StatusBadRequest, "name and viewerToken are required")
		return
	}

	syncToken, ok := bearerToken(r)
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if _, err := s.Store.VerifyHubSyncToken(r.Context(), body.Name, syncToken); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := s.Store.SetHubViewerToken(r.Context(), body.Name, body.ViewerToken); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, syncHubResponse{OK: true})
}

// ── Helpers ────────────────────────────────────────────────────────

// authenticate runs the store's Authenticator on r and writes a 401
// response if authentication fails or returns no user. Handlers call
// this as the first step and bail out via the bool return on failure.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (User, bool) {
	user, err := s.Store.Authenticate(r)
	if err != nil || user == nil {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return user, true
}

// writeJSON serializes body to the response with the given status.
func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError writes a JSON error body in the documented shape.
func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, map[string]string{"error": msg})
}

// writeStoreError translates a Store error into the appropriate HTTP
// status code and JSON error body. Stores return well-known sentinel
// errors; this function maps each one. Unknown errors get a generic
// 500.
func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, ErrForbidden):
		s.writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrHubNotFound):
		s.writeError(w, http.StatusNotFound, "hub not found")
	case errors.Is(err, ErrHubExists):
		s.writeError(w, http.StatusConflict, "hub already exists")
	case errors.Is(err, ErrInvalidInput):
		s.writeError(w, http.StatusBadRequest, "invalid input")
	default:
		s.logf("portal: store error: %v", err)
		s.writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// decodeJSONBody parses the request body as JSON into v with a 1 MiB
// limit. Returns an error if the body is malformed, oversized, or
// missing.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	limited := http.MaxBytesReader(nil, r.Body, 1<<20)
	defer limited.Close()
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// bearerToken extracts the bearer token from the Authorization
// header. Returns the token and true on success, "" and false if the
// header is missing or not a Bearer token.
func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", false
	}
	tok := auth[len(prefix):]
	if tok == "" {
		return "", false
	}
	return tok, true
}

// drainAndClose reads anything left in r and closes it. Used by the
// admin proxy after copying the response body, so the underlying
// connection can be reused. Errors are ignored on purpose.
func drainAndClose(r io.ReadCloser) {
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}
