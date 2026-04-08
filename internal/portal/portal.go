package portal

import (
	"context"
	"errors"
	"net/http"
)

// ── Protocol version constants ─────────────────────────────────────

// ProtocolVersion is the portal protocol version this package
// implements. It is the value the package returns in the
// `protocolVersion` field of /.well-known/tela responses, and the
// value clients should expect to negotiate against.
//
// See DESIGN-portal.md section 2 for the version negotiation rules.
const ProtocolVersion = "1.0"

// SupportedVersions is the full set of portal protocol versions this
// package can speak. It is the value the package returns in the
// `supportedVersions` field of /.well-known/tela responses. A client
// presenting a version not in this list MUST be refused with a clear
// error.
//
// New entries are added when the package learns to speak older or
// newer protocol versions in parallel. Today the package speaks one
// version.
var SupportedVersions = []string{ProtocolVersion}

// HubDirectoryPath is the conventional path under which a portal
// serves the hub directory endpoints (section 3 of DESIGN-portal.md).
// Implementations MAY serve the directory under a different path and
// advertise that path via the `hub_directory` field of
// /.well-known/tela; clients honor whatever the well-known endpoint
// returns and fall back to this constant on discovery failure.
const HubDirectoryPath = "/api/hubs"

// HubSyncTokenPrefix is the mandatory prefix for sync tokens issued
// by POST /api/hubs (DESIGN-portal.md section 3.2 and 8). Tokens
// without this prefix MUST NOT be accepted by the
// PATCH /api/hubs/sync endpoint.
const HubSyncTokenPrefix = "hubsync_"

// ── Domain types ───────────────────────────────────────────────────

// Hub describes a hub registered with the portal. The portal stores
// the full record (including the admin token, never echoed back to
// any client) and exposes a redacted view via HubVisibility for
// directory listings.
//
// Stores are free to add fields beyond what is documented here as
// long as they remain compatible with HubUpdate. The portal package
// itself only depends on the documented fields.
type Hub struct {
	// Name is the short hub name. Unique within the portal. Used as
	// the addressable identifier in the admin proxy URL space.
	Name string

	// URL is the public hub URL: either https:// (HTTP+WSS) or
	// http:// (HTTP+WS). The hub's own admin API and WebSocket
	// endpoint live under this URL.
	URL string

	// ViewerToken is the hub's console-viewer role token, if the
	// portal will host a web console for the hub. May be empty when
	// the portal does not need to proxy viewer-scope reads.
	ViewerToken string

	// AdminToken is the hub's owner or admin token. The portal stores
	// it so it can proxy admin requests later. The protocol forbids
	// echoing this field back in any response. Stores MUST treat it
	// as a secret.
	AdminToken string

	// OrgName is a free-form display label for the organizational
	// scope this hub belongs to, if the store models orgs. May be
	// empty. Single-user stores return empty for every hub.
	OrgName string
}

// HubVisibility is the directory response shape for one hub
// (DESIGN-portal.md section 3.1). Stores compute this view from
// their internal Hub records and from the requesting user's
// permissions on the hub.
//
// This is the shape clients see in `GET /api/hubs` responses. Stores
// MUST NOT include ViewerToken or AdminToken in this view.
type HubVisibility struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	CanManage bool   `json:"canManage"`

	// OrgName is the optional display label from the underlying Hub
	// record. Stores that do not model orgs return an empty string,
	// which the JSON encoder serializes as "" (not omitted). Clients
	// MUST tolerate empty string and the field being absent
	// interchangeably.
	OrgName string `json:"orgName,omitempty"`
}

// HubUpdate is a partial update to a Hub record. Nil fields mean
// "do not change." Used by the user-initiated PATCH /api/hubs
// endpoint (DESIGN-portal.md section 3.4).
type HubUpdate struct {
	Name        *string
	URL         *string
	ViewerToken *string
	AdminToken  *string
}

// User identifies an authenticated principal in the portal's
// identity store. Implementations choose what this means: a database
// account ID, an OS username, an opaque session UUID, etc. The
// portal package treats it as an opaque key for permission lookups
// and logging, never inspecting the concrete type.
type User interface {
	// ID returns a stable string identifier for this user. Used by
	// the portal package for logging and audit trails. The format
	// is store-defined.
	ID() string
}

// ── Authentication ─────────────────────────────────────────────────

// Authenticator inspects an HTTP request and returns the User it
// represents, or an authentication error. Authenticators are how
// stores plug their identity model into the protocol without the
// portal package needing to know about cookies, sessions, OAuth,
// or any other auth mechanism.
//
// A nil User return with a nil error means "no authentication
// presented" -- the request is anonymous, which is acceptable for
// endpoints that allow it (e.g. /.well-known/tela). Authenticators
// SHOULD return ErrUnauthorized for malformed credentials and a
// wrapped error for transient failures.
type Authenticator interface {
	Authenticate(r *http.Request) (User, error)
}

// ── Persistence and authorization ──────────────────────────────────

// Store is the pluggable persistence and authorization layer behind
// the portal HTTP handlers. Implementations decide everything about
// how hubs are stored, how users are modeled, and how visibility and
// management permissions are computed.
//
// The portal package treats the Store as the single source of truth
// for two questions:
//
//  1. Who is this request from? (Authenticator)
//  2. What can that user do with the hubs the store holds?
//
// Stores MUST implement the methods documented here in a way
// consistent with DESIGN-portal.md. The conformance test suite in
// internal/portal/portaltest exercises every method against the spec.
//
// Stores MUST be safe for concurrent use. The HTTP handlers call
// store methods from many goroutines.
type Store interface {
	Authenticator

	// ListHubsForUser returns the hubs the user can see. The
	// CanManage field on each entry is set per the store's policy.
	// An empty result is not an error.
	ListHubsForUser(ctx context.Context, user User) ([]HubVisibility, error)

	// LookupHubForUser returns the named hub if and only if the user
	// can see it. The boolean is the user's canManage permission on
	// that hub. Stores MUST NOT leak the existence of a hub to a
	// user who cannot see it: return ErrHubNotFound for both
	// "hub does not exist" and "user cannot see hub" cases.
	LookupHubForUser(ctx context.Context, user User, hubName string) (*Hub, bool, error)

	// AddHub creates or upserts a hub. Two contexts:
	//
	//   - Hub-bootstrap: a hub registers itself by presenting an
	//     admin token. The portal authenticates the admin token via
	//     the store's Authenticator (returning a synthetic
	//     hub-bootstrap user, or whatever model the store uses) and
	//     calls AddHub with the supplied URL and tokens. On success,
	//     the store generates a fresh sync token, stores its hash,
	//     and returns the cleartext exactly once via the syncToken
	//     return value.
	//
	//   - User-add: a logged-in user adds a hub through the portal
	//     UI by entering its URL and a viewer token. The store
	//     creates the record under the user's scope and returns an
	//     empty syncToken (no sync token issued; the hub did not
	//     register itself).
	//
	// The store decides which context applies based on the user
	// returned by Authenticator.
	//
	// Returns:
	//   - created: true if a new record was created, false if an
	//     existing record was updated (upsert).
	//   - syncToken: the cleartext sync token in hub-bootstrap mode,
	//     empty string in user-add mode.
	AddHub(ctx context.Context, user User, hub Hub) (created bool, syncToken string, err error)

	// UpdateHub partially updates a hub the user can manage. Nil
	// fields in the update mean "do not change." Returns
	// ErrHubNotFound if the user cannot see the hub or it does not
	// exist; ErrForbidden if the user can see it but cannot manage.
	UpdateHub(ctx context.Context, user User, currentName string, update HubUpdate) error

	// DeleteHub removes a hub the user owns. The authorization rule
	// is store-defined but MUST be tighter than read access: a user
	// who can see a hub but does not own it MUST get ErrForbidden.
	DeleteHub(ctx context.Context, user User, hubName string) error

	// VerifyHubSyncToken checks a sync token presented by a hub on
	// PATCH /api/hubs/sync. Returns the matching hub on success or
	// ErrUnauthorized on token mismatch (using a timing-safe
	// comparison). Returns ErrHubNotFound if the named hub does not
	// exist.
	//
	// The token MUST start with HubSyncTokenPrefix; tokens without
	// the prefix are rejected without comparison.
	VerifyHubSyncToken(ctx context.Context, hubName, syncToken string) (*Hub, error)

	// SetHubViewerToken updates a hub's viewer token. Called by the
	// PATCH /api/hubs/sync handler after VerifyHubSyncToken
	// succeeds. The store does not re-authorize; the caller is
	// responsible for verifying the sync token first.
	SetHubViewerToken(ctx context.Context, hubName, viewerToken string) error
}

// ── Standard errors ────────────────────────────────────────────────

// ErrUnauthorized indicates the request had no valid authentication
// or the presented credentials were rejected. Maps to HTTP 401.
var ErrUnauthorized = errors.New("portal: unauthorized")

// ErrForbidden indicates the request was authenticated but the user
// is not authorized to perform the operation. Maps to HTTP 403.
var ErrForbidden = errors.New("portal: forbidden")

// ErrHubNotFound indicates the named hub does not exist OR the user
// cannot see it. Stores MUST return this for both cases so the
// portal does not leak the existence of hubs to users who cannot
// see them. Maps to HTTP 404.
var ErrHubNotFound = errors.New("portal: hub not found")

// ErrHubExists indicates a hub with the requested name already
// exists. Used by stores that do not support upsert; modern stores
// upsert silently and never return this. Maps to HTTP 409.
var ErrHubExists = errors.New("portal: hub already exists")

// ErrInvalidInput indicates the request body or parameters were
// malformed: missing required fields, invalid URL, name too long,
// etc. Maps to HTTP 400.
var ErrInvalidInput = errors.New("portal: invalid input")
