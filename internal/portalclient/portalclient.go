// Package portalclient is a Go client for the portal protocol
// described in DESIGN-portal.md. It speaks to any portal that
// implements protocol version 1.1: the in-process embedded portal
// shipped inside TelaVisor, the standalone telaportal binary, and
// remote multi-tenant portals like Awan Saya.
//
// The package is small on purpose. It exposes one Client type that
// holds a base URL, a bearer token, and an *http.Client, plus typed
// wrapper methods for each protocol endpoint. Callers that need to
// reach beyond the documented endpoints can use HubAdmin (the generic
// admin proxy passthrough) and the lower-level Do method.
//
// The two callers planned today are TelaVisor (which embeds the file
// store and talks to it over loopback) and the future tela CLI hubs
// subcommand. Awan Saya does not consume this package; it is the
// portal, not a portal client.
//
// Spec: tela/DESIGN-portal.md.
package portalclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/paulmooreparks/tela/internal/portal"
)

// Client is a portal protocol client. Construct with New; the zero
// value is not usable. A Client is safe for concurrent use by
// multiple goroutines.
type Client struct {
	// BaseURL is the portal's HTTP base, e.g. http://127.0.0.1:8780
	// or https://awansaya.net. Trailing slashes are tolerated.
	BaseURL string

	// BearerToken is presented on every authenticated request via
	// the Authorization header. Empty for the discovery call only.
	BearerToken string

	// HTTPClient is the underlying transport. If nil, the package
	// uses a client with a 60s timeout, matching the admin proxy's
	// upstream budget on the server side.
	HTTPClient *http.Client
}

// New returns a Client configured for baseURL with the given bearer
// token. The HTTP client is a fresh *http.Client with a 60s timeout.
// Callers that need a custom transport (proxies, custom TLS, etc.)
// can replace HTTPClient on the returned value or build the Client
// struct directly.
func New(baseURL, bearerToken string) *Client {
	return &Client{
		BaseURL:     baseURL,
		BearerToken: bearerToken,
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Discovery is the decoded body of /.well-known/tela. Mirrors the
// shape produced by internal/portal.wellKnownResponse.
type Discovery struct {
	HubDirectory      string   `json:"hub_directory"`
	ProtocolVersion   string   `json:"protocolVersion"`
	SupportedVersions []string `json:"supportedVersions"`
	PortalID          string   `json:"portalId,omitempty"`
}

// Discover fetches /.well-known/tela. No authentication is sent.
// Used by callers that want to verify the portal speaks a compatible
// version before issuing user-scoped calls.
func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	var out Discovery
	req, err := c.newRequest(ctx, http.MethodGet, "/.well-known/tela", nil)
	if err != nil {
		return out, err
	}
	// Discovery is unauthenticated. Strip the header the helper added.
	req.Header.Del("Authorization")
	if err := c.doJSON(req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// ── Hub directory ───────────────────────────────────────────────────

// ListHubs returns the hubs visible to the authenticated user.
// Wraps GET /api/hubs.
func (c *Client) ListHubs(ctx context.Context) ([]portal.HubVisibility, error) {
	var body struct {
		Hubs []portal.HubVisibility `json:"hubs"`
	}
	req, err := c.newRequest(ctx, http.MethodGet, portal.HubDirectoryPath, nil)
	if err != nil {
		return nil, err
	}
	if err := c.doJSON(req, &body); err != nil {
		return nil, err
	}
	return body.Hubs, nil
}

// AddHubRequest is the body shape for AddHub. Mirrors the server-side
// shape in internal/portal.addHubRequest.
type AddHubRequest struct {
	Name        string `json:"name"`
	HubID       string `json:"hubId,omitempty"`
	URL         string `json:"url"`
	ViewerToken string `json:"viewerToken,omitempty"`
	AdminToken  string `json:"adminToken,omitempty"`
	OrgName     string `json:"orgName,omitempty"`
}

// AddHubResponse is the response shape from AddHub. SyncToken is
// non-empty only when the request was authenticated as a hub
// bootstrap (the portal issues a fresh sync token exactly once).
// Updated is true on upsert.
type AddHubResponse struct {
	Hubs      []portal.HubVisibility `json:"hubs"`
	SyncToken string                 `json:"syncToken,omitempty"`
	Updated   bool                   `json:"updated,omitempty"`
}

// AddHub adds a hub to the portal directory. Wraps POST /api/hubs.
func (c *Client) AddHub(ctx context.Context, body AddHubRequest) (AddHubResponse, error) {
	var out AddHubResponse
	req, err := c.newRequest(ctx, http.MethodPost, portal.HubDirectoryPath, body)
	if err != nil {
		return out, err
	}
	if err := c.doJSON(req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// UpdateHubRequest is the body shape for UpdateHub. Pointer fields
// encode "field absent" vs "field present" so callers can change one
// field without resetting the others. Mirrors
// internal/portal.updateHubRequest.
type UpdateHubRequest struct {
	CurrentName string  `json:"currentName"`
	Name        *string `json:"name,omitempty"`
	URL         *string `json:"url,omitempty"`
	ViewerToken *string `json:"viewerToken,omitempty"`
	AdminToken  *string `json:"adminToken,omitempty"`
}

// UpdateHub partially updates a hub. Returns the post-mutation hub
// list. Wraps PATCH /api/hubs.
func (c *Client) UpdateHub(ctx context.Context, body UpdateHubRequest) ([]portal.HubVisibility, error) {
	var resp struct {
		Hubs []portal.HubVisibility `json:"hubs"`
	}
	req, err := c.newRequest(ctx, http.MethodPatch, portal.HubDirectoryPath, body)
	if err != nil {
		return nil, err
	}
	if err := c.doJSON(req, &resp); err != nil {
		return nil, err
	}
	return resp.Hubs, nil
}

// DeleteHub removes a hub by name. Returns the post-mutation hub
// list. Wraps DELETE /api/hubs?name=NAME.
func (c *Client) DeleteHub(ctx context.Context, name string) ([]portal.HubVisibility, error) {
	var resp struct {
		Hubs []portal.HubVisibility `json:"hubs"`
	}
	path := portal.HubDirectoryPath + "?name=" + url.QueryEscape(name)
	req, err := c.newRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}
	if err := c.doJSON(req, &resp); err != nil {
		return nil, err
	}
	return resp.Hubs, nil
}

// ── Fleet aggregation ───────────────────────────────────────────────

// FleetAgent is one entry from GET /api/fleet/agents (DESIGN-portal.md
// section 5). Each entry mirrors a hub's /api/status machine record,
// tagged with the hub identity fields the portal adds.
//
// Fields marked "(1.1)" are present only when both the portal and the
// upstream hub speak protocol version 1.1. Callers MUST tolerate empty
// strings for those fields when talking to a mix of 1.0 and 1.1 hubs.
type FleetAgent struct {
	// Machine identity (from the hub's /api/status machine record).
	ID                    string `json:"id"`                              // machineName; always present
	AgentID               string `json:"agentId,omitempty"`               // (1.1) stable telad UUID
	MachineRegistrationID string `json:"machineRegistrationId,omitempty"` // (1.1) hub-local UUID

	// Machine status (from the hub's /api/status machine record).
	AgentConnected bool             `json:"agentConnected"`
	SessionCount   int              `json:"sessionCount"`
	DisplayName    *string          `json:"displayName,omitempty"`
	Hostname       *string          `json:"hostname,omitempty"`
	OS             *string          `json:"os,omitempty"`
	AgentVersion   *string          `json:"agentVersion,omitempty"`
	Tags           []string         `json:"tags,omitempty"`
	Location       *string          `json:"location,omitempty"`
	Owner          *string          `json:"owner,omitempty"`
	RegisteredAt   *string          `json:"registeredAt,omitempty"`
	LastSeen       *string          `json:"lastSeen,omitempty"`
	Services       []map[string]any `json:"services,omitempty"`
	Capabilities   map[string]any   `json:"capabilities,omitempty"`

	// Hub identity (added by the portal aggregation layer).
	Hub    string `json:"hub"`             // portal-assigned hub name
	HubURL string `json:"hubUrl"`          // hub's public URL
	HubID  string `json:"hubId,omitempty"` // (1.1) hub's stable UUID
}

// FleetAgents returns the merged agent list across every hub the
// authenticated user can manage. Each entry is the upstream hub's
// /api/status machine record tagged with hub identity fields.
// Wraps GET /api/fleet/agents.
func (c *Client) FleetAgents(ctx context.Context) ([]FleetAgent, error) {
	var resp struct {
		Agents []FleetAgent `json:"agents"`
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/fleet/agents", nil)
	if err != nil {
		return nil, err
	}
	if err := c.doJSON(req, &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// ── Admin proxy ─────────────────────────────────────────────────────

// HubAdmin issues an admin request against the named hub via the
// portal's /api/hub-admin/{hubName}/{operation} proxy. Method is the
// HTTP verb (GET, POST, PATCH, etc.); operation is the hub admin
// path WITHOUT the leading /api/admin/ (e.g. "access" or
// "agents/abc123/status"). The body, if any, is sent as
// application/json. Additional per-request headers (If-Match,
// If-None-Match, Accept-Language, ...) may be passed via the optional
// extraHeaders variadic; maps are applied in order, so a later entry
// overrides an earlier one for the same key.
//
// The returned *http.Response is the raw upstream response. The
// caller is responsible for closing Body. Non-2xx statuses are NOT
// translated to errors here, because admin endpoints have endpoint-
// specific status semantics; the caller decides what counts as a
// failure. Network errors and request-construction errors are
// returned in err with a nil response.
func (c *Client) HubAdmin(ctx context.Context, hubName, operation, method string, body any, extraHeaders ...map[string]string) (*http.Response, error) {
	if hubName == "" || operation == "" {
		return nil, errors.New("portalclient: HubAdmin requires hubName and operation")
	}
	path := "/api/hub-admin/" + url.PathEscape(hubName) + "/" + operation
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	for _, h := range extraHeaders {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	return c.HTTPClient.Do(req)
}

// HubAdminJSON is a convenience wrapper around HubAdmin that decodes
// the response body into out (if non-nil) and returns an error for
// non-2xx responses. Callers that need raw access to the response
// (streaming, custom status handling, non-JSON bodies) should use
// HubAdmin directly.
func (c *Client) HubAdminJSON(ctx context.Context, hubName, operation, method string, body, out any) error {
	resp, err := c.HubAdmin(ctx, hubName, operation, method, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeOrError(resp, out)
}

// ── Public hub endpoint proxies ────────────────────────────────────

// HubStatus fetches the named hub's /api/status through the portal's
// /api/hub-status/{hubName} proxy. The portal authenticates against
// the hub with its stored token; the caller does not need direct
// access to the hub or its token.
func (c *Client) HubStatus(ctx context.Context, hubName string) ([]byte, error) {
	return c.hubPublicProxy(ctx, hubName, "/api/hub-status/")
}

// HubHistory fetches the named hub's /api/history through the portal's
// /api/hub-history/{hubName} proxy.
func (c *Client) HubHistory(ctx context.Context, hubName string) ([]byte, error) {
	return c.hubPublicProxy(ctx, hubName, "/api/hub-history/")
}

// hubPublicProxy issues a GET against a portal public-hub proxy
// endpoint and returns the raw response body.
func (c *Client) hubPublicProxy(ctx context.Context, hubName, prefix string) ([]byte, error) {
	if hubName == "" {
		return nil, errors.New("portalclient: hubName is required")
	}
	path := prefix + url.PathEscape(hubName)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// ── Hub token retrieval ────────────────────────────────────────────

// HubToken retrieves the stored admin token for a hub through the
// portal's /api/hub-token/{hubName} endpoint. The caller must have
// manage permission on the hub. Returns the raw token string.
func (c *Client) HubToken(ctx context.Context, hubName string) (string, error) {
	if hubName == "" {
		return "", errors.New("portalclient: hubName is required")
	}
	path := "/api/hub-token/" + url.PathEscape(hubName)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.doJSON(req, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// ── OAuth 2.0 device authorization grant (RFC 8628) ────────────────

// DeviceAuthorization is the response from POST /api/oauth/device.
// The user visits VerificationURI (or VerificationURIComplete, which
// pre-fills UserCode), authorizes the request in their browser, and
// the client polls /api/oauth/token with DeviceCode until it gets a
// token back. Spec: DESIGN-portal.md section 6.3.
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenResponse is the success response from polling
// POST /api/oauth/token. AccessToken is the bearer token to install
// on a Client; TokenType is always "Bearer" per the spec.
type DeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// StartDeviceAuth begins the device authorization grant. No bearer
// token is sent (this is the call that bootstraps one). The caller
// then displays UserCode + VerificationURI to the user (typically
// also opening the URI in the system browser) and starts polling
// PollDeviceToken on the documented Interval until it returns a
// token, returns ErrAuthorizationPending (still waiting), or returns
// any other error.
func (c *Client) StartDeviceAuth(ctx context.Context) (DeviceAuthorization, error) {
	var out DeviceAuthorization
	req, err := c.newRequest(ctx, http.MethodPost, "/api/oauth/device", struct{}{})
	if err != nil {
		return out, err
	}
	req.Header.Del("Authorization")
	if err := c.doJSON(req, &out); err != nil {
		return out, err
	}
	return out, nil
}

// PollDeviceToken issues one poll against /api/oauth/token. The
// returned error is one of:
//
//   - nil with a populated DeviceTokenResponse: the user has
//     authorized; install AccessToken on a Client and proceed.
//   - ErrAuthorizationPending: keep polling at the documented
//     interval.
//   - ErrSlowDown: increase the polling interval by 5 seconds and
//     keep polling.
//   - ErrExpiredToken: the device code expired; restart the flow
//     with StartDeviceAuth.
//   - ErrAccessDenied: the user denied the request.
//   - any other error: transport or unexpected server response.
func (c *Client) PollDeviceToken(ctx context.Context, deviceCode string) (DeviceTokenResponse, error) {
	var out DeviceTokenResponse
	body := struct {
		GrantType  string `json:"grant_type"`
		DeviceCode string `json:"device_code"`
	}{
		GrantType:  "urn:ietf:params:oauth:grant-type:device_code",
		DeviceCode: deviceCode,
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/oauth/token", body)
	if err != nil {
		return out, err
	}
	req.Header.Del("Authorization")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("portalclient: poll device token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, fmt.Errorf("portalclient: decode token response: %w", err)
		}
		return out, nil
	}

	// RFC 8628 polling errors come back as 400 with an OAuth error
	// body: {"error": "authorization_pending"} etc. Map the documented
	// codes onto sentinel errors so callers can use errors.Is.
	var oauthErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &oauthErr)
	switch oauthErr.Error {
	case "authorization_pending":
		return out, ErrAuthorizationPending
	case "slow_down":
		return out, ErrSlowDown
	case "expired_token":
		return out, ErrExpiredToken
	case "access_denied":
		return out, ErrAccessDenied
	}
	msg := strings.TrimSpace(string(raw))
	if oauthErr.ErrorDescription != "" {
		msg = oauthErr.ErrorDescription
	} else if oauthErr.Error != "" {
		msg = oauthErr.Error
	}
	return out, &HTTPError{StatusCode: resp.StatusCode, Message: msg}
}

// Sentinel errors returned by PollDeviceToken to encode the four
// documented OAuth device-grant polling states from RFC 8628.
var (
	ErrAuthorizationPending = errors.New("portalclient: authorization_pending")
	ErrSlowDown             = errors.New("portalclient: slow_down")
	ErrExpiredToken         = errors.New("portalclient: expired_token")
	ErrAccessDenied         = errors.New("portalclient: access_denied")
)

// ── Lower-level helpers ─────────────────────────────────────────────

// newRequest builds a request against c.BaseURL with the bearer
// token attached. Body, if non-nil, is JSON-encoded.
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	target := strings.TrimRight(c.BaseURL, "/") + path
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("portalclient: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("portalclient: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// doJSON executes req via the client's HTTP transport and decodes
// the JSON response body into out. Non-2xx responses return an
// *HTTPError; transport failures return a wrapped error.
func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("portalclient: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	return decodeOrError(resp, out)
}

// decodeOrError reads resp.Body and either decodes it into out (on
// 2xx) or returns an *HTTPError carrying the status code and
// server-provided error message. out may be nil to discard the body.
func decodeOrError(resp *http.Response, out any) error {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		// Best-effort: if the body is the documented {"error": "..."} shape, extract it.
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			msg = errBody.Error
		}
		return &HTTPError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("portalclient: decode response: %w", err)
	}
	return nil
}

// HTTPError is returned for non-2xx responses from any client method
// that decodes JSON. Callers can errors.As to inspect StatusCode
// (e.g. to distinguish 401 from 404).
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("portalclient: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("portalclient: HTTP %d: %s", e.StatusCode, e.Message)
}
