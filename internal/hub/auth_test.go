package hub

import (
	"encoding/hex"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// Token strings used across tests. Real tokens are 64-char hex; the test
// values are short strings because the auth store does not validate
// token format -- it just compares bytes.
const (
	tokenOwner   = "tok-owner"
	tokenAdmin   = "tok-admin"
	tokenUser1   = "tok-user-1"
	tokenUser2   = "tok-user-2"
	tokenViewer  = "tok-viewer"
	tokenUnknown = "tok-unknown"
)

// makeStore builds an authStore with the given tokens and machine ACLs.
// Tests use this instead of newAuthStore directly so the per-test config
// stays terse.
func makeStore(tokens []tokenEntry, acls map[string]machineACL) *authStore {
	cfg := &authConfig{
		Tokens:   tokens,
		Machines: acls,
	}
	return newAuthStore(cfg)
}

// stdTokens returns the four-role token list every test starts from.
func stdTokens() []tokenEntry {
	return []tokenEntry{
		{ID: "owner", Token: tokenOwner, HubRole: "owner"},
		{ID: "admin", Token: tokenAdmin, HubRole: "admin"},
		{ID: "alice", Token: tokenUser1, HubRole: ""},
		{ID: "bob", Token: tokenUser2, HubRole: ""},
		{ID: "console", Token: tokenViewer, HubRole: "viewer"},
	}
}

// ── Construction and basic state ───────────────────────────────────

func TestNewAuthStore_NilCfgIsOpen(t *testing.T) {
	s := newAuthStore(nil)
	if s == nil {
		t.Fatal("newAuthStore(nil) returned nil")
	}
	if s.isEnabled() {
		t.Error("nil cfg should produce a disabled (open) store")
	}
}

func TestNewAuthStore_EmptyTokensIsOpen(t *testing.T) {
	s := newAuthStore(&authConfig{})
	if s.isEnabled() {
		t.Error("empty tokens should produce a disabled store")
	}
}

func TestNewAuthStore_TokensEnabled(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if !s.isEnabled() {
		t.Fatal("non-empty tokens should produce an enabled store")
	}
	if got := s.identityID(tokenOwner); got != "owner" {
		t.Errorf("identityID(owner) = %q, want %q", got, "owner")
	}
	if got := s.identityID(tokenUser1); got != "alice" {
		t.Errorf("identityID(user1) = %q, want %q", got, "alice")
	}
	if got := s.identityID(tokenUnknown); got != "" {
		t.Errorf("identityID(unknown) = %q, want \"\"", got)
	}
	if got := s.identityID(""); got != "" {
		t.Errorf("identityID(empty) = %q, want \"\"", got)
	}
}

// ── Role helpers ───────────────────────────────────────────────────

func TestIsOwnerOrAdmin(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	cases := []struct {
		token string
		want  bool
	}{
		{tokenOwner, true},
		{tokenAdmin, true},
		{tokenUser1, false},
		{tokenViewer, false},
		{tokenUnknown, false},
		{"", false},
	}
	for _, c := range cases {
		if got := s.isOwnerOrAdmin(c.token); got != c.want {
			t.Errorf("isOwnerOrAdmin(%q) = %v, want %v", c.token, got, c.want)
		}
	}
}

func TestIsOwnerOrAdmin_DisabledStoreAlwaysFalse(t *testing.T) {
	// Open hub: nobody is owner/admin because auth is off. The function
	// returns false specifically so callers can distinguish "open hub"
	// from "owner-authenticated hub".
	s := newAuthStore(nil)
	if s.isOwnerOrAdmin(tokenOwner) {
		t.Error("disabled store should return false for any token")
	}
}

func TestIsViewer(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if !s.isViewer(tokenViewer) {
		t.Error("viewer token should report isViewer=true")
	}
	if s.isViewer(tokenOwner) {
		t.Error("owner token should not report isViewer=true")
	}
	if s.isViewer(tokenUser1) {
		t.Error("user token should not report isViewer=true")
	}
	if s.isViewer("") {
		t.Error("empty token should not report isViewer=true")
	}
	if s.isViewer(tokenUnknown) {
		t.Error("unknown token should not report isViewer=true")
	}
}

func TestConsoleViewerToken(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if got := s.consoleViewerToken(); got != tokenViewer {
		t.Errorf("consoleViewerToken = %q, want %q", got, tokenViewer)
	}

	// Store with no viewer should return empty.
	withoutViewer := []tokenEntry{
		{ID: "owner", Token: tokenOwner, HubRole: "owner"},
	}
	s2 := makeStore(withoutViewer, nil)
	if got := s2.consoleViewerToken(); got != "" {
		t.Errorf("consoleViewerToken (no viewer) = %q, want \"\"", got)
	}
}

// ── canRegister ────────────────────────────────────────────────────

func TestCanRegister_DisabledAlwaysAllows(t *testing.T) {
	s := newAuthStore(nil)
	if !s.canRegister(tokenUnknown, "anywhere") {
		t.Error("disabled store should allow any register")
	}
}

func TestCanRegister_OwnerAndAdminAlwaysAllowed(t *testing.T) {
	s := makeStore(stdTokens(), nil) // no machine ACLs at all
	if !s.canRegister(tokenOwner, "barn") {
		t.Error("owner should be allowed to register barn even with no ACL")
	}
	if !s.canRegister(tokenAdmin, "barn") {
		t.Error("admin should be allowed to register barn even with no ACL")
	}
}

func TestCanRegister_NoACLEntryDeniesNonAdmin(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if s.canRegister(tokenUser1, "barn") {
		t.Error("user with no ACL entry should be denied register")
	}
}

func TestCanRegister_ExplicitRegisterTokenMustMatch(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {RegisterToken: tokenUser1},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canRegister(tokenUser1, "barn") {
		t.Error("matching registerToken should be allowed")
	}
	if s.canRegister(tokenUser2, "barn") {
		t.Error("non-matching token should be denied even though it's a known user")
	}
	if s.canRegister(tokenUnknown, "barn") {
		t.Error("unknown token should be denied")
	}
}

func TestCanRegister_ACLEntryWithoutRegisterTokenAllowsKnownTokens(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canRegister(tokenUser1, "barn") {
		t.Error("known token should be allowed when ACL exists but registerToken is empty")
	}
	if !s.canRegister(tokenUser2, "barn") {
		t.Error("any known token should be allowed when registerToken is empty")
	}
	if s.canRegister(tokenUnknown, "barn") {
		t.Error("unknown token should still be denied")
	}
}

// ── canConnect ─────────────────────────────────────────────────────

func TestCanConnect_DisabledAlwaysAllows(t *testing.T) {
	s := newAuthStore(nil)
	if !s.canConnect(tokenUnknown, "barn") {
		t.Error("disabled store should allow any connect")
	}
}

func TestCanConnect_OwnerAndAdminBypass(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if !s.canConnect(tokenOwner, "barn") {
		t.Error("owner should bypass machine ACLs on connect")
	}
	if !s.canConnect(tokenAdmin, "barn") {
		t.Error("admin should bypass machine ACLs on connect")
	}
}

func TestCanConnect_PerMachineAllow(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canConnect(tokenUser1, "barn") {
		t.Error("user listed in machine ConnectTokens should be allowed")
	}
	if s.canConnect(tokenUser2, "barn") {
		t.Error("user not listed should be denied")
	}
}

func TestCanConnect_WildcardACL(t *testing.T) {
	acls := map[string]machineACL{
		"*": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canConnect(tokenUser1, "barn") {
		t.Error("wildcard ConnectTokens should grant access to any machine")
	}
	if !s.canConnect(tokenUser1, "web01") {
		t.Error("wildcard should also cover other machines")
	}
	if s.canConnect(tokenUser2, "barn") {
		t.Error("user not in wildcard list should be denied")
	}
}

func TestCanConnect_PerMachineAndWildcardCombine(t *testing.T) {
	// alice in wildcard, bob only in machine-specific. Both should
	// connect to barn; only alice should connect to web01.
	acls := map[string]machineACL{
		"*":    {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser2}}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canConnect(tokenUser1, "barn") {
		t.Error("alice (wildcard) should connect to barn")
	}
	if !s.canConnect(tokenUser2, "barn") {
		t.Error("bob (machine-specific) should connect to barn")
	}
	if !s.canConnect(tokenUser1, "web01") {
		t.Error("alice (wildcard) should connect to web01")
	}
	if s.canConnect(tokenUser2, "web01") {
		t.Error("bob has no entry for web01 and should be denied")
	}
}

// ── canManage ──────────────────────────────────────────────────────

func TestCanManage_DisabledAlwaysAllows(t *testing.T) {
	s := newAuthStore(nil)
	if !s.canManage(tokenUnknown, "barn") {
		t.Error("disabled store should allow any manage")
	}
}

func TestCanManage_OwnerAndAdminBypass(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if !s.canManage(tokenOwner, "barn") {
		t.Error("owner should bypass machine ACLs on manage")
	}
	if !s.canManage(tokenAdmin, "barn") {
		t.Error("admin should bypass machine ACLs on manage")
	}
}

func TestCanManage_RequiresExplicitGrant(t *testing.T) {
	// alice is in connectTokens but NOT in manageTokens. She should be
	// able to connect but not manage. This is the key separation
	// that lets you give read-only / connect-only access to non-admins.
	acls := map[string]machineACL{
		"barn": {
			ConnectTokens: []connectGrant{{Token: tokenUser1}},
			ManageTokens:  []string{tokenUser2},
		},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canConnect(tokenUser1, "barn") {
		t.Error("alice should still connect")
	}
	if s.canManage(tokenUser1, "barn") {
		t.Error("alice has connect but not manage; canManage should be false")
	}
	if !s.canManage(tokenUser2, "barn") {
		t.Error("bob is in manageTokens and should be allowed")
	}
}

func TestCanManage_WildcardACL(t *testing.T) {
	acls := map[string]machineACL{
		"*": {ManageTokens: []string{tokenUser1}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canManage(tokenUser1, "barn") {
		t.Error("wildcard ManageTokens should grant manage to any machine")
	}
	if !s.canManage(tokenUser1, "web01") {
		t.Error("wildcard manage should also cover other machines")
	}
	if s.canManage(tokenUser2, "barn") {
		t.Error("user not in wildcard manage list should be denied")
	}
}

// ── canViewMachine ─────────────────────────────────────────────────

func TestCanViewMachine_ViewerSeesAll(t *testing.T) {
	// A viewer-role token has no machine-specific ACL grant, but
	// canViewMachine should return true regardless because viewers are
	// the read-only console role.
	s := makeStore(stdTokens(), nil)
	if !s.canViewMachine(tokenViewer, "any-machine") {
		t.Error("viewer should be able to view any machine")
	}
	if !s.canViewMachine(tokenViewer, "another") {
		t.Error("viewer should see every machine, not just one")
	}
}

func TestCanViewMachine_NonViewerFallsThroughToConnect(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(stdTokens(), acls)
	if !s.canViewMachine(tokenUser1, "barn") {
		t.Error("user with connect should be able to view")
	}
	if s.canViewMachine(tokenUser2, "barn") {
		t.Error("user without connect should not be able to view")
	}
	if !s.canViewMachine(tokenOwner, "barn") {
		t.Error("owner should be able to view via the canConnect bypass")
	}
}

// ── reload ─────────────────────────────────────────────────────────

func TestReload_ReplacesState(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	if !s.isEnabled() {
		t.Fatal("precondition: store should be enabled")
	}

	// Reload with a different token set.
	newTokens := []tokenEntry{
		{ID: "carol", Token: "tok-carol", HubRole: ""},
	}
	s.reload(&authConfig{Tokens: newTokens})

	if !s.isEnabled() {
		t.Error("reload with non-empty tokens should remain enabled")
	}
	if s.identityID("tok-carol") != "carol" {
		t.Error("new token should be present after reload")
	}
	if s.identityID(tokenOwner) != "" {
		t.Error("old tokens should be gone after reload")
	}
}

func TestReload_NilCfgDisablesStore(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	s.reload(nil)
	if s.isEnabled() {
		t.Error("reload(nil) should disable the store")
	}
	if !s.canConnect(tokenUnknown, "anywhere") {
		t.Error("disabled store should allow connects")
	}
}

func TestReload_EmptyTokensDisablesStore(t *testing.T) {
	s := makeStore(stdTokens(), nil)
	s.reload(&authConfig{}) // no tokens
	if s.isEnabled() {
		t.Error("reload with empty tokens should disable the store")
	}
}

// ── toConfig ───────────────────────────────────────────────────────

func TestToConfig_RoundTrip(t *testing.T) {
	original := stdTokens()
	originalACLs := map[string]machineACL{
		"*":    {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
		"barn": {ManageTokens: []string{tokenUser2}, RegisterToken: tokenAdmin},
	}
	s := makeStore(original, originalACLs)

	exported := s.toConfig()
	if len(exported.Tokens) != len(original) {
		t.Errorf("token count: got %d, want %d", len(exported.Tokens), len(original))
	}

	// Round trip into a fresh store and verify behavior is preserved.
	s2 := newAuthStore(&exported)
	if !s2.canConnect(tokenUser1, "barn") {
		t.Error("round-tripped store should preserve wildcard connect")
	}
	if !s2.canManage(tokenUser2, "barn") {
		t.Error("round-tripped store should preserve per-machine manage")
	}
	if !s2.canRegister(tokenAdmin, "barn") {
		t.Error("round-tripped store should preserve registerToken (and admin bypass)")
	}
}

// ── inTokenList ────────────────────────────────────────────────────

func TestInTokenList(t *testing.T) {
	list := []string{"a", "b", "c"}
	if !inTokenList(list, "b") {
		t.Error("matching token should be found")
	}
	if inTokenList(list, "d") {
		t.Error("non-matching token should not be found")
	}
	if inTokenList(list, "") {
		t.Error("empty token should not match anything in a non-empty list")
	}
	if inTokenList(nil, "anything") {
		t.Error("nil list should never match")
	}
}

// ── generateToken ──────────────────────────────────────────────────

func TestGenerateToken_LengthAndHex(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Errorf("token length = %d, want 64", len(tok))
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Errorf("token is not valid hex: %v", err)
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token at iteration %d", i)
		}
		seen[tok] = true
	}
}

// ── Concurrent access (sanity, not exhaustive) ─────────────────────

func TestConcurrentReads(t *testing.T) {
	// The auth store uses sync.RWMutex; concurrent reads should be safe
	// and produce consistent results. This is a smoke test for the lock
	// shape, not a stress test.
	acls := map[string]machineACL{
		"*":    {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
		"barn": {ManageTokens: []string{tokenUser2}},
	}
	s := makeStore(stdTokens(), acls)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_ = s.canConnect(tokenUser1, "barn")
				_ = s.canManage(tokenUser2, "barn")
				_ = s.identityID(tokenOwner)
				_ = s.isOwnerOrAdmin(tokenAdmin)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestReload_ConcurrentWithReads(t *testing.T) {
	// Mix reload with reads. This should not deadlock or race.
	s := makeStore(stdTokens(), nil)
	stop := make(chan struct{})

	// Background readers.
	for i := 0; i < 4; i++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.canConnect(tokenOwner, "barn")
					_ = s.identityID(tokenUser1)
				}
			}
		}()
	}

	// Reloader.
	for i := 0; i < 50; i++ {
		s.reload(&authConfig{Tokens: stdTokens()})
	}

	close(stop)
}

// ── connectGrant YAML compatibility ───────────────────────────────

// Pre-0.15 configs wrote connectTokens as a plain list of strings.
// The connectGrant.UnmarshalYAML hook must still accept that form so
// operators upgrading to 0.15 do not have to rewrite their config.
func TestConnectGrant_UnmarshalYAML_AcceptsBareString(t *testing.T) {
	src := `
connectTokens:
  - tok-user-1
  - tok-user-2
`
	var acl machineACL
	if err := yaml.Unmarshal([]byte(src), &acl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []connectGrant{
		{Token: "tok-user-1"},
		{Token: "tok-user-2"},
	}
	if !reflect.DeepEqual(acl.ConnectTokens, want) {
		t.Errorf("bare-string form parsed to %#v, want %#v", acl.ConnectTokens, want)
	}
}

func TestConnectGrant_UnmarshalYAML_AcceptsStructForm(t *testing.T) {
	src := `
connectTokens:
  - token: tok-user-1
  - token: tok-user-2
    services:
      - Jellyfin
      - SSH
`
	var acl machineACL
	if err := yaml.Unmarshal([]byte(src), &acl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []connectGrant{
		{Token: "tok-user-1"},
		{Token: "tok-user-2", Services: []string{"Jellyfin", "SSH"}},
	}
	if !reflect.DeepEqual(acl.ConnectTokens, want) {
		t.Errorf("struct form parsed to %#v, want %#v", acl.ConnectTokens, want)
	}
}

func TestConnectGrant_UnmarshalYAML_MixedForms(t *testing.T) {
	// Mixing bare strings and struct entries in one list must work so
	// config files edited partially by the admin API and partially by
	// hand do not break.
	src := `
connectTokens:
  - tok-user-1
  - token: tok-user-2
    services: [SSH]
  - tok-user-3
`
	var acl machineACL
	if err := yaml.Unmarshal([]byte(src), &acl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []connectGrant{
		{Token: "tok-user-1"},
		{Token: "tok-user-2", Services: []string{"SSH"}},
		{Token: "tok-user-3"},
	}
	if !reflect.DeepEqual(acl.ConnectTokens, want) {
		t.Errorf("mixed form parsed to %#v, want %#v", acl.ConnectTokens, want)
	}
}

// ── connectServicesFilter ─────────────────────────────────────────

func TestConnectServicesFilter_NoFilter_MeansAllServices(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
	}
	s := makeStore(stdTokens(), acls)
	names, filtered := s.connectServicesFilter(tokenUser1, "barn")
	if filtered {
		t.Errorf("grant with no services field should report filtered=false, got filtered=true names=%v", names)
	}
	if names != nil {
		t.Errorf("unfiltered grant should return nil names, got %v", names)
	}
}

func TestConnectServicesFilter_EmptyList_MeansAllServices(t *testing.T) {
	// An explicit empty services list is indistinguishable from absent
	// and must be treated as "all services". Otherwise operators who
	// wrote `services: []` would lock themselves out.
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1, Services: []string{}}}},
	}
	s := makeStore(stdTokens(), acls)
	_, filtered := s.connectServicesFilter(tokenUser1, "barn")
	if filtered {
		t.Error("empty services list should not register as a filter")
	}
}

func TestConnectServicesFilter_NamedServices_ReturnedInOrder(t *testing.T) {
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{{
			Token:    tokenUser1,
			Services: []string{"Jellyfin", "SSH"},
		}}},
	}
	s := makeStore(stdTokens(), acls)
	names, filtered := s.connectServicesFilter(tokenUser1, "barn")
	if !filtered {
		t.Fatal("grant with services list should report filtered=true")
	}
	want := []string{"Jellyfin", "SSH"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("filter names = %v, want %v", names, want)
	}
}

func TestConnectServicesFilter_MachineSpecificBeatsWildcard(t *testing.T) {
	// If both a machine-specific grant and a wildcard grant match the
	// same token, the machine-specific entry wins. This lets operators
	// grant blanket "connect to everything" via "*" and then narrow
	// specific machines by name.
	acls := map[string]machineACL{
		"*":    {ConnectTokens: []connectGrant{{Token: tokenUser1}}},
		"barn": {ConnectTokens: []connectGrant{{Token: tokenUser1, Services: []string{"SSH"}}}},
	}
	s := makeStore(stdTokens(), acls)
	names, filtered := s.connectServicesFilter(tokenUser1, "barn")
	if !filtered || !reflect.DeepEqual(names, []string{"SSH"}) {
		t.Errorf("machine-specific should win; got filtered=%v names=%v", filtered, names)
	}
}

func TestConnectServicesFilter_OwnerAdminViewerBypassesFilter(t *testing.T) {
	// Owner, admin, and viewer roles must not be filterable. Operators
	// need to see every service for diagnostics regardless of a stray
	// services entry on their token (which should never exist in
	// practice but must not lock anyone out if it does). Viewers are
	// read-only console observers; filtering their view would silently
	// break monitoring dashboards.
	acls := map[string]machineACL{
		"barn": {ConnectTokens: []connectGrant{
			{Token: tokenOwner, Services: []string{"not-a-real-service"}},
			{Token: tokenAdmin, Services: []string{"not-a-real-service"}},
			{Token: tokenViewer, Services: []string{"not-a-real-service"}},
		}},
	}
	s := makeStore(stdTokens(), acls)
	for _, tok := range []string{tokenOwner, tokenAdmin, tokenViewer} {
		_, filtered := s.connectServicesFilter(tok, "barn")
		if filtered {
			t.Errorf("owner/admin/viewer bypass broken: token %q returned filtered=true", tok)
		}
	}
}

func TestConnectServicesFilter_AuthDisabled_NeverFilters(t *testing.T) {
	s := newAuthStore(nil)
	_, filtered := s.connectServicesFilter(tokenUser1, "barn")
	if filtered {
		t.Error("open hub should never apply a services filter")
	}
}

// ── connectGrant helpers ─────────────────────────────────────────

func TestFindConnectGrant_MatchesByTokenConstantTime(t *testing.T) {
	grants := []connectGrant{
		{Token: "one"},
		{Token: "two", Services: []string{"SSH"}},
	}
	g, ok := findConnectGrant(grants, "two")
	if !ok {
		t.Fatal("findConnectGrant should have matched 'two'")
	}
	if !reflect.DeepEqual(g.Services, []string{"SSH"}) {
		t.Errorf("matched grant carried wrong Services: %v", g.Services)
	}

	if _, ok := findConnectGrant(grants, "missing"); ok {
		t.Error("findConnectGrant should not match an absent token")
	}
}

func TestRemoveConnectGrant_RemovesAllMatching(t *testing.T) {
	// A duplicate shouldn't normally happen but removeConnectGrant
	// should scrub every occurrence defensively.
	grants := []connectGrant{
		{Token: "keep"},
		{Token: "go"},
		{Token: "keep"},
		{Token: "go"},
	}
	out := removeConnectGrant(grants, "go")
	want := []connectGrant{{Token: "keep"}, {Token: "keep"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("removeConnectGrant = %v, want %v", out, want)
	}
}

func TestReplaceConnectGrantToken_PreservesServices(t *testing.T) {
	// Token rotation must rewrite the token string but leave the
	// services filter in place; the filter is a policy set by the
	// operator, not a property of the specific secret.
	grants := []connectGrant{
		{Token: "old"},
		{Token: "old", Services: []string{"SSH"}},
		{Token: "unrelated"},
	}
	changed := replaceConnectGrantToken(grants, "old", "new")
	if !changed {
		t.Fatal("replaceConnectGrantToken should report change=true")
	}
	want := []connectGrant{
		{Token: "new"},
		{Token: "new", Services: []string{"SSH"}},
		{Token: "unrelated"},
	}
	if !reflect.DeepEqual(grants, want) {
		t.Errorf("after rotation grants = %v, want %v", grants, want)
	}
}
