package portalaggregate

import (
	"context"
	"errors"
	"testing"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portalclient"
)

// ── Synthetic source stub ──────────────────────────────────────────

// stubSource is a fake portal source for unit tests.
type stubSource struct {
	hubs   []portal.HubVisibility
	agents []portalclient.FleetAgent
	err    error
}

func (s *stubSource) ListHubs(_ context.Context) ([]portal.HubVisibility, error) {
	return s.hubs, s.err
}

func (s *stubSource) FleetAgents(_ context.Context) ([]portalclient.FleetAgent, error) {
	return s.agents, s.err
}

// ── Helpers ────────────────────────────────────────────────────────

func ptr(s string) *string { return &s }

// findHub returns the MergedHub with the given hubId, or nil.
func findHub(hubs []MergedHub, id string) *MergedHub {
	for i := range hubs {
		if hubs[i].HubID == id {
			return &hubs[i]
		}
	}
	return nil
}

// findAgent returns the MergedAgent with the matching (hubId, agentId), or nil.
func findAgent(agents []MergedAgent, hubID, agentID string) *MergedAgent {
	for i := range agents {
		if agents[i].HubID == hubID && agents[i].AgentID == agentID {
			return &agents[i]
		}
	}
	return nil
}

// ── Tests ───────────────────────────────────────────────────────────

func TestMerge_EmptySources(t *testing.T) {
	result, err := merge(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 0 || len(result.Agents) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

func TestMerge_SingleSource_SingleHub(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "myhub", URL: "https://hub.example.com", CanManage: true},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 1 {
		t.Fatalf("want 1 hub, got %d", len(result.Hubs))
	}
	h := result.Hubs[0]
	if h.HubID != "hub-001" {
		t.Errorf("HubID = %q, want %q", h.HubID, "hub-001")
	}
	if h.Name != "myhub" {
		t.Errorf("Name = %q, want %q", h.Name, "myhub")
	}
	if h.PreferredSource != "local" {
		t.Errorf("PreferredSource = %q, want %q", h.PreferredSource, "local")
	}
}

// TestMerge_SameHubTwoSources verifies that the same hubId from two
// sources collapses into a single MergedHub with two HubSource entries.
func TestMerge_SameHubTwoSources(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "local-name", URL: "https://hub.example.com", CanManage: true},
			},
		},
		"remote": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "remote-name", URL: "https://hub.example.com", CanManage: false},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 1 {
		t.Fatalf("want 1 hub (deduplicated), got %d", len(result.Hubs))
	}
	h := result.Hubs[0]
	if h.HubID != "hub-001" {
		t.Errorf("HubID = %q, want hub-001", h.HubID)
	}
	// Local source wins for display name per section 7.3.
	if h.Name != "local-name" {
		t.Errorf("Name = %q, want local-name (local source wins)", h.Name)
	}
	if h.PreferredSource != "local" {
		t.Errorf("PreferredSource = %q, want local", h.PreferredSource)
	}
	if len(h.Sources) != 2 {
		t.Errorf("want 2 sources, got %d: %+v", len(h.Sources), h.Sources)
	}
}

// TestMerge_DifferentHubIDs verifies that two distinct hubIds produce
// two distinct MergedHubs even when registered under the same name.
func TestMerge_DifferentHubIDs(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "hub", URL: "https://hub1.example.com", CanManage: true},
				{ID: "hub-002", Name: "hub", URL: "https://hub2.example.com", CanManage: true},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 2 {
		t.Fatalf("want 2 hubs, got %d", len(result.Hubs))
	}
}

// TestMerge_AgentDedup verifies that the same (hubId, agentId) from
// two sources produces one MergedAgent with both source names listed.
func TestMerge_AgentDedup(t *testing.T) {
	agent := portalclient.FleetAgent{
		ID:      "barn",
		AgentID: "agent-001",
		HubID:   "hub-001",
		Hub:     "myhub",
		HubURL:  "https://hub.example.com",
	}
	sources := map[string]source{
		"local": &stubSource{
			hubs:   []portal.HubVisibility{{ID: "hub-001", Name: "myhub", URL: "https://hub.example.com", CanManage: true}},
			agents: []portalclient.FleetAgent{agent},
		},
		"remote": &stubSource{
			hubs:   []portal.HubVisibility{{ID: "hub-001", Name: "myhub", URL: "https://hub.example.com", CanManage: false}},
			agents: []portalclient.FleetAgent{agent},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 1 {
		t.Fatalf("want 1 agent (deduplicated), got %d", len(result.Agents))
	}
	a := result.Agents[0]
	if len(a.Sources) != 2 {
		t.Errorf("want 2 source names, got %d: %v", len(a.Sources), a.Sources)
	}
}

// TestMerge_LinkedAgents verifies that the same agentId on two hubs
// produces two MergedAgents each with LinkedAgentIDs pointing at the other.
func TestMerge_LinkedAgents(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "hub1", URL: "https://h1.example.com", CanManage: true},
				{ID: "hub-002", Name: "hub2", URL: "https://h2.example.com", CanManage: true},
			},
			agents: []portalclient.FleetAgent{
				{ID: "barn", AgentID: "agent-001", HubID: "hub-001", Hub: "hub1", HubURL: "https://h1.example.com"},
				{ID: "barn", AgentID: "agent-001", HubID: "hub-002", Hub: "hub2", HubURL: "https://h2.example.com"},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 2 {
		t.Fatalf("want 2 agents (different hubs), got %d", len(result.Agents))
	}
	a1 := findAgent(result.Agents, "hub-001", "agent-001")
	a2 := findAgent(result.Agents, "hub-002", "agent-001")
	if a1 == nil || a2 == nil {
		t.Fatal("expected both agents to be present")
	}
	if len(a1.LinkedAgentIDs) != 1 {
		t.Errorf("a1.LinkedAgentIDs = %v, want 1 entry", a1.LinkedAgentIDs)
	}
	if len(a2.LinkedAgentIDs) != 1 {
		t.Errorf("a2.LinkedAgentIDs = %v, want 1 entry", a2.LinkedAgentIDs)
	}
}

// TestMerge_UnreachableSource verifies that a source error marks the
// source as unreachable without failing the whole merge.
func TestMerge_UnreachableSource(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				{ID: "hub-001", Name: "myhub", URL: "https://hub.example.com", CanManage: true},
			},
		},
		"broken": &stubSource{err: errors.New("connection refused")},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 1 {
		t.Errorf("want 1 hub from reachable source, got %d", len(result.Hubs))
	}
	if len(result.UnreachableSources) != 1 || result.UnreachableSources[0] != "broken" {
		t.Errorf("UnreachableSources = %v, want [broken]", result.UnreachableSources)
	}
}

// TestMerge_AgentOnline verifies that if any source reports an agent
// as connected, the merged entry is online.
func TestMerge_AgentOnline(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			agents: []portalclient.FleetAgent{
				{ID: "barn", AgentID: "agent-001", HubID: "hub-001", Hub: "h", HubURL: "u",
					AgentConnected: true},
			},
		},
		"remote": &stubSource{
			agents: []portalclient.FleetAgent{
				{ID: "barn", AgentID: "agent-001", HubID: "hub-001", Hub: "h", HubURL: "u",
					AgentConnected: false},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(result.Agents))
	}
	if !result.Agents[0].Online {
		t.Error("want Online=true when any source reports connected")
	}
}

// TestMerge_FallbackKey tests that pre-1.1 hubs without hubId are
// keyed by name+URL, so two sources advertising the same name+URL
// collapse into one MergedHub.
func TestMerge_FallbackKey(t *testing.T) {
	sources := map[string]source{
		"local": &stubSource{
			hubs: []portal.HubVisibility{
				// ID is empty: 1.0 hub
				{Name: "oldhub", URL: "https://old.example.com", CanManage: true},
			},
		},
		"remote": &stubSource{
			hubs: []portal.HubVisibility{
				{Name: "oldhub", URL: "https://old.example.com", CanManage: false},
			},
		},
	}
	result, err := merge(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hubs) != 1 {
		t.Fatalf("want 1 hub (deduplicated by name+URL), got %d", len(result.Hubs))
	}
	if len(result.Hubs[0].Sources) != 2 {
		t.Errorf("want 2 sources, got %d", len(result.Hubs[0].Sources))
	}
}
