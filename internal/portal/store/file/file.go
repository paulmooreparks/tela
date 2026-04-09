package file

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/internal/portal"
)

// localUser is the single principal recognized by the file store.
// All authenticated requests resolve to this value; the file store
// does not model multiple users.
type localUser struct{}

func (localUser) ID() string { return "local" }

// LocalUser is the package-level instance returned by Authenticate
// for any request that successfully presents the configured admin
// token (or any request at all when no admin token is configured).
// Tests may need this to compare against the User returned by store
// methods that take a User parameter.
var LocalUser portal.User = localUser{}

// Store is a file-backed portal.Store implementation. It serializes
// its state to a YAML file on disk and loads it on Open.
//
// On-disk format:
//
//	# portal.yaml
//	portalId: 550e8400-e29b-41d4-a716-446655440000
//
//	# Optional: SHA-256 hex of the admin token. When present, the
//	# Authenticator requires Bearer auth on every request and
//	# matches the presented token's hash against this value.
//	adminTokenHash: 5b3d...e9
//
//	# Hubs registered with the portal.
//	hubs:
//	  - name: myhub
//	    hubId: 550e8400-e29b-41d4-a716-446655440001
//	    url: https://hub.example.com
//	    viewerToken: <hex>
//	    adminToken: <hex>        # the hub's admin token (secret)
//	    syncTokenHash: <sha256>  # SHA-256 of the issued sync token
//	    orgName: ""
//
// All file mutations are atomic: writes go to a temp file in the
// same directory and are renamed into place.
type Store struct {
	path string

	mu             sync.RWMutex
	portalID       string                // stable UUID, generated once on first Open
	adminTokenHash string                // empty when no auth required
	hubs           map[string]*storedHub // hubName -> record
}

// storedHub is the on-disk shape of one hub. Field names use
// camelCase YAML tags to match the portal wire convention
// (Principle 7 of DESIGN-identity.md).
type storedHub struct {
	Name          string `yaml:"name"`
	HubID         string `yaml:"hubId,omitempty"`
	URL           string `yaml:"url"`
	ViewerToken   string `yaml:"viewerToken,omitempty"`
	AdminToken    string `yaml:"adminToken,omitempty"`
	SyncTokenHash string `yaml:"syncTokenHash,omitempty"`
	OrgName       string `yaml:"orgName,omitempty"`
}

// fileShape is the top-level YAML document.
type fileShape struct {
	PortalID       string       `yaml:"portalId,omitempty"`
	AdminTokenHash string       `yaml:"adminTokenHash,omitempty"`
	Hubs           []*storedHub `yaml:"hubs,omitempty"`
}

// Open reads the portal store from path. If the file does not exist,
// Open creates an empty in-memory store, generates a portalId, and
// returns without touching the disk; the file is created on the first
// successful mutation. If the file exists but is malformed, Open
// returns an error and does not return a Store.
func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		hubs: make(map[string]*storedHub),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			id, err := newUUID()
			if err != nil {
				return nil, fmt.Errorf("portal/file: generate portalId: %w", err)
			}
			s.portalID = id
			return s, nil
		}
		return nil, fmt.Errorf("portal/file: read %s: %w", path, err)
	}

	var shape fileShape
	if err := yaml.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("portal/file: parse %s: %w", path, err)
	}

	s.adminTokenHash = shape.AdminTokenHash

	// Generate a portalId if the file predates identity support.
	if shape.PortalID == "" {
		id, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("portal/file: generate portalId: %w", err)
		}
		s.portalID = id
	} else {
		s.portalID = shape.PortalID
	}

	for _, h := range shape.Hubs {
		if h == nil || h.Name == "" {
			continue
		}
		// Defensive copy so the in-memory store does not retain
		// references to the parsed YAML node.
		copy := *h
		s.hubs[h.Name] = &copy
	}
	return s, nil
}

// PortalID returns the stable UUID that identifies this portal
// instance. Generated once on first Open and persisted to the YAML
// file on the next mutation.
func (s *Store) PortalID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.portalID
}

// SetAdminToken installs a fresh admin token. The store hashes the
// token and persists the hash; the cleartext is not retained. Pass
// an empty string to disable bearer auth (revert to "trust every
// request" mode, only safe on localhost).
//
// This is the only method that mutates the admin token; the portal
// HTTP handlers do not expose admin token rotation as part of the
// protocol. Operators set or rotate the token out-of-band.
func (s *Store) SetAdminToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		s.adminTokenHash = ""
	} else {
		s.adminTokenHash = hashHex(token)
	}
	return s.saveLocked()
}

// HasAuth reports whether the store requires bearer-token
// authentication. False means every request is treated as the local
// operator (use only on localhost).
func (s *Store) HasAuth() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.adminTokenHash != ""
}

// Path returns the filesystem path the store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// ── portal.Store implementation ────────────────────────────────────

// Authenticate checks the request's Authorization header against the
// configured admin token. Returns LocalUser on success, ErrUnauthorized
// on failure.
//
// When no admin token is configured, Authenticate returns LocalUser
// for every request. This is the localhost-only mode and the caller
// is responsible for binding the listener to 127.0.0.1.
func (s *Store) Authenticate(r *http.Request) (portal.User, error) {
	s.mu.RLock()
	hash := s.adminTokenHash
	s.mu.RUnlock()

	if hash == "" {
		return LocalUser, nil
	}

	auth := r.Header.Get("Authorization")
	const bearer = "Bearer "
	if !strings.HasPrefix(auth, bearer) {
		return nil, portal.ErrUnauthorized
	}
	presented := auth[len(bearer):]
	if presented == "" {
		return nil, portal.ErrUnauthorized
	}
	if hashHex(presented) != hash {
		return nil, portal.ErrUnauthorized
	}
	return LocalUser, nil
}

// ListHubsForUser returns every hub the store holds. Single-user
// portals do not filter by visibility; the operator sees all hubs
// they have registered with `canManage: true` on every one.
func (s *Store) ListHubsForUser(_ context.Context, _ portal.User) ([]portal.HubVisibility, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]portal.HubVisibility, 0, len(s.hubs))
	for _, h := range s.hubs {
		out = append(out, portal.HubVisibility{
			Name:      h.Name,
			URL:       h.URL,
			CanManage: true,
			OrgName:   h.OrgName,
		})
	}
	// Sort by name for deterministic output (helps tests and helps
	// any UI that wants stable ordering between refreshes).
	sortHubVisibility(out)
	return out, nil
}

// LookupHubForUser returns the named hub if it exists. The single
// operator can manage every hub, so the second return is always true
// when the first return is non-nil.
func (s *Store) LookupHubForUser(_ context.Context, _ portal.User, hubName string) (*portal.Hub, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, ok := s.hubs[hubName]
	if !ok {
		return nil, false, portal.ErrHubNotFound
	}
	return toPublicHub(h), true, nil
}

// AddHub creates or upserts a hub. The single-user file store always
// issues a sync token (the distinction between hub-bootstrap and
// user-add modes collapses when there is only one principal). The
// returned cleartext sync token is the only time it appears outside
// the store; subsequent calls only see the hash.
func (s *Store) AddHub(_ context.Context, _ portal.User, hub portal.Hub) (bool, string, error) {
	if hub.Name == "" || hub.URL == "" {
		return false, "", portal.ErrInvalidInput
	}

	syncToken, err := portal.GenerateSyncToken()
	if err != nil {
		return false, "", fmt.Errorf("portal/file: generate sync token: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, existed := s.hubs[hub.Name]
	s.hubs[hub.Name] = &storedHub{
		Name:          hub.Name,
		URL:           hub.URL,
		ViewerToken:   hub.ViewerToken,
		AdminToken:    hub.AdminToken,
		SyncTokenHash: portal.HashSyncToken(syncToken),
		OrgName:       hub.OrgName,
	}

	if err := s.saveLocked(); err != nil {
		// Roll back the in-memory mutation so the store stays
		// consistent with disk.
		if existed {
			// We overwrote an existing record without saving the
			// previous shape; we cannot perfectly restore. Reload
			// from disk to recover the pre-call state.
			_ = s.reloadLocked()
		} else {
			delete(s.hubs, hub.Name)
		}
		return false, "", err
	}

	return !existed, syncToken, nil
}

// UpdateHub partially updates an existing hub. Nil fields in the
// update are left unchanged. Returns ErrHubNotFound if no record
// matches currentName.
func (s *Store) UpdateHub(_ context.Context, _ portal.User, currentName string, update portal.HubUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.hubs[currentName]
	if !ok {
		return portal.ErrHubNotFound
	}

	// Apply the update to a copy first so a save failure can be
	// rolled back atomically.
	next := *h
	if update.Name != nil {
		next.Name = *update.Name
	}
	if update.URL != nil {
		next.URL = *update.URL
	}
	if update.ViewerToken != nil {
		next.ViewerToken = *update.ViewerToken
	}
	if update.AdminToken != nil {
		next.AdminToken = *update.AdminToken
	}
	// Validate the post-update record.
	if next.Name == "" || next.URL == "" {
		return portal.ErrInvalidInput
	}
	// If the rename collides with an existing hub, refuse.
	if next.Name != currentName {
		if _, exists := s.hubs[next.Name]; exists {
			return portal.ErrHubExists
		}
		delete(s.hubs, currentName)
		s.hubs[next.Name] = &next
	} else {
		s.hubs[currentName] = &next
	}

	if err := s.saveLocked(); err != nil {
		_ = s.reloadLocked()
		return err
	}
	return nil
}

// DeleteHub removes the named hub.
func (s *Store) DeleteHub(_ context.Context, _ portal.User, hubName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.hubs[hubName]; !ok {
		return portal.ErrHubNotFound
	}
	delete(s.hubs, hubName)

	if err := s.saveLocked(); err != nil {
		_ = s.reloadLocked()
		return err
	}
	return nil
}

// VerifyHubSyncToken checks that the presented sync token matches
// the stored hash for the named hub. Returns the matching hub on
// success, ErrUnauthorized on token mismatch, ErrHubNotFound on a
// missing or unknown hub. Comparison is timing-safe via
// portal.CompareSyncTokenHash.
func (s *Store) VerifyHubSyncToken(_ context.Context, hubName, syncToken string) (*portal.Hub, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	h, ok := s.hubs[hubName]
	if !ok {
		return nil, portal.ErrHubNotFound
	}
	if h.SyncTokenHash == "" {
		return nil, portal.ErrUnauthorized
	}
	if !portal.CompareSyncTokenHash(syncToken, h.SyncTokenHash) {
		return nil, portal.ErrUnauthorized
	}
	return toPublicHub(h), nil
}

// SetHubViewerToken updates the named hub's viewer token. Caller is
// responsible for verifying the sync token first.
func (s *Store) SetHubViewerToken(_ context.Context, hubName, viewerToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.hubs[hubName]
	if !ok {
		return portal.ErrHubNotFound
	}
	prev := h.ViewerToken
	h.ViewerToken = viewerToken

	if err := s.saveLocked(); err != nil {
		h.ViewerToken = prev
		return err
	}
	return nil
}

// ── Internal helpers ───────────────────────────────────────────────

// saveLocked writes the current in-memory state to disk atomically.
// Caller MUST hold s.mu (either read or write; saveLocked only reads
// in-memory state, but UpdateHub/AddHub/DeleteHub all hold the write
// lock when they call this).
func (s *Store) saveLocked() error {
	if s.path == "" {
		return errors.New("portal/file: no path configured")
	}

	shape := fileShape{
		PortalID:       s.portalID,
		AdminTokenHash: s.adminTokenHash,
		Hubs:           make([]*storedHub, 0, len(s.hubs)),
	}
	for _, h := range s.hubs {
		copy := *h
		shape.Hubs = append(shape.Hubs, &copy)
	}
	sortStoredHubs(shape.Hubs)

	data, err := yaml.Marshal(&shape)
	if err != nil {
		return fmt.Errorf("portal/file: marshal: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("portal/file: create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".portal-*.tmp")
	if err != nil {
		return fmt.Errorf("portal/file: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("portal/file: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal/file: close temp: %w", err)
	}
	// 0600 so the file is not world-readable; it contains hub admin
	// tokens which are secrets.
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal/file: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("portal/file: rename: %w", err)
	}
	return nil
}

// reloadLocked re-reads the file from disk into the in-memory state.
// Used to recover from a save failure that left the in-memory state
// inconsistent with disk. Caller must hold s.mu (write lock).
func (s *Store) reloadLocked() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.adminTokenHash = ""
			s.hubs = make(map[string]*storedHub)
			return nil
		}
		return err
	}
	var shape fileShape
	if err := yaml.Unmarshal(data, &shape); err != nil {
		return err
	}
	s.portalID = shape.PortalID
	s.adminTokenHash = shape.AdminTokenHash
	s.hubs = make(map[string]*storedHub, len(shape.Hubs))
	for _, h := range shape.Hubs {
		if h == nil || h.Name == "" {
			continue
		}
		copy := *h
		s.hubs[h.Name] = &copy
	}
	return nil
}

// newUUID returns a random UUID v4 formatted as
// "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx". Uses crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

// hashHex returns the lowercase hex SHA-256 of s. Used for the admin
// token hash; sync token hashing goes through portal.HashSyncToken.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// toPublicHub copies a storedHub into the public portal.Hub shape.
func toPublicHub(h *storedHub) *portal.Hub {
	return &portal.Hub{
		Name:        h.Name,
		URL:         h.URL,
		ViewerToken: h.ViewerToken,
		AdminToken:  h.AdminToken,
		OrgName:     h.OrgName,
	}
}

// sortHubVisibility sorts a HubVisibility slice by Name in place.
// Insertion sort; the slice is small (one entry per hub on the
// portal, max a few hundred in any realistic deployment).
func sortHubVisibility(s []portal.HubVisibility) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Name > s[j].Name; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// sortStoredHubs sorts a *storedHub slice by Name in place.
func sortStoredHubs(s []*storedHub) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Name > s[j].Name; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
