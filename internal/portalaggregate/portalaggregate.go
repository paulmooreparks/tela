// Package portalaggregate merges hub and agent data from one or more
// portal sources into deduplicated views keyed by stable UUIDs.
//
// It depends only on internal/portalclient and the protocol types in
// internal/portal. It does not touch the network itself; callers
// provide fully-configured portalclient.Client values.
//
// The aggregation rules follow DESIGN-identity.md sections 7.0-7.4:
//   - One MergedHub per unique hubId (fallback: hub name + URL).
//   - One MergedAgent per unique (hubId, agentId) pair.
//   - Same agent on two hubs = two MergedAgents with LinkedAgentIDs set.
//   - Per-source errors are non-fatal; the source appears in Result.UnreachableSources.
package portalaggregate

import (
	"context"
	"sync"

	"github.com/paulmooreparks/tela/internal/portal"
	"github.com/paulmooreparks/tela/internal/portalclient"
)

// MergedHub is one hub seen across one or more enabled portal sources,
// keyed by hubId. When the same physical hub is registered under
// different names on different sources, they collapse into a single
// MergedHub.
type MergedHub struct {
	// HubID is the hub's stable v4 UUID (DESIGN-identity.md).
	// Empty only for pre-1.1 hubs that do not yet emit a hubId;
	// in that case the entry is keyed by name+URL instead.
	HubID string

	// Name is the display name chosen per section 7.3: Local source
	// wins, then first source alphabetically.
	Name string

	// URL is the hub's public URL from the highest-privilege source
	// that knows this hub.
	URL string

	// Sources records every source that advertises this hub, with
	// per-source metadata (name, URL, canManage, orgName).
	Sources []HubSource

	// PreferredSource is the source name to use for admin actions.
	// Chosen as the first canManage=true source, preferring "local".
	PreferredSource string
}

// HubSource records one source's view of a hub.
type HubSource struct {
	SourceName string
	Name       string // what THIS source calls the hub
	URL        string // what THIS source advertises
	CanManage  bool
	OrgName    string
}

// MergedAgent is one (hubId, agentId) pair seen across enabled sources.
// Two registrations of the same agent on different hubs are two
// MergedAgents (not one), but they share the same AgentID and
// LinkedAgentIDs exposes the relationship.
type MergedAgent struct {
	// AgentID is the agent's stable v4 UUID.
	AgentID string

	// HubID is the hub this agent registration belongs to.
	HubID string

	// HubName is the human-readable name of the hub. Useful when
	// rendering an agent record without joining back to the hub list.
	HubName string

	// MachineRegistrationID is the hub-local UUID for this
	// (agentId, machineName) pair.
	MachineRegistrationID string

	// MachineName is the canonical machine identifier the hub uses for
	// admin API calls (the entry's name in the agent's machines config).
	// Always present.
	MachineName string

	// DisplayName is the optional human-readable label set by the
	// operator in the agent's machine config. Falls back to MachineName
	// when the operator did not set one.
	DisplayName string

	// Online is true when at least one source reports the agent as
	// connected.
	Online bool

	// SessionCount is the number of active client sessions on this
	// agent. When merged across multiple sources, the maximum value
	// from any source wins.
	SessionCount int

	// Hostname, OS, AgentVersion are optional metadata from the agent
	// status (first non-empty source wins on merge).
	Hostname     string
	OS           string
	AgentVersion string

	// Tags, Location, Owner are operator-supplied metadata from the
	// agent's machine config (first non-empty source wins on merge).
	Tags     []string
	Location string
	Owner    string

	// RegisteredAt and LastSeen are RFC3339 timestamps from the hub
	// (first non-empty source wins on merge).
	RegisteredAt string
	LastSeen     string

	// Services is the list of forwarded services the agent exposes.
	Services []ServiceInfo

	// Capabilities is the agent's reported feature set (e.g.
	// management, fileShare). Shape mirrors the agent's capabilities
	// JSON object verbatim.
	Capabilities map[string]any

	// LinkedAgentIDs lists the AgentID+HubID keys of other
	// MergedAgents that share the same AgentID but belong to a
	// different hub. Empty when the agent is on only one hub.
	LinkedAgentIDs []string

	// Sources lists the portal source names that returned this
	// agent registration.
	Sources []string
}

// ServiceInfo describes one forwarded service on an agent.
type ServiceInfo struct {
	Port     int    `json:"port"`
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Result is the output of Merge.
type Result struct {
	Hubs               []MergedHub
	Agents             []MergedAgent
	UnreachableSources []string
}

// source is a named portal source providing hub and agent lists.
// Defined internally so tests can inject synthetic sources without
// needing a live HTTP server.
type source interface {
	ListHubs(ctx context.Context) ([]portal.HubVisibility, error)
	FleetAgents(ctx context.Context) ([]portalclient.FleetAgent, error)
}

// Merge fetches ListHubs and FleetAgents from every client in clients,
// deduplicates by hubId and (hubId, agentId), and returns the merged
// views. Errors from individual clients are non-fatal; the unreachable
// source name is recorded in Result.UnreachableSources.
func Merge(ctx context.Context, clients map[string]*portalclient.Client) (Result, error) {
	named := make(map[string]source, len(clients))
	for name, c := range clients {
		named[name] = c
	}
	return merge(ctx, named)
}

// merge is the internal implementation, parameterised on a source
// interface so unit tests can inject synthetic data.
func merge(ctx context.Context, sources map[string]source) (Result, error) {
	type fetchResult struct {
		name   string
		hubs   []portal.HubVisibility
		agents []portalclient.FleetAgent
		err    error
	}

	ch := make(chan fetchResult, len(sources))
	var wg sync.WaitGroup
	for name, src := range sources {
		wg.Add(1)
		go func(n string, s source) {
			defer wg.Done()
			fr := fetchResult{name: n}
			hubs, err := s.ListHubs(ctx)
			if err != nil {
				fr.err = err
				ch <- fr
				return
			}
			agents, err := s.FleetAgents(ctx)
			if err != nil {
				fr.err = err
				ch <- fr
				return
			}
			fr.hubs = hubs
			fr.agents = agents
			ch <- fr
		}(name, src)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	// hubKey -> {*MergedHub, []HubSource} working state.
	type hubState struct {
		hub     *MergedHub
		sources []HubSource
	}
	hubMap := make(map[string]*hubState)

	// agentKey -> *MergedAgent working state.
	agentMap := make(map[string]*MergedAgent)

	var unreachable []string

	for fr := range ch {
		if fr.err != nil {
			unreachable = append(unreachable, fr.name)
			continue
		}

		// Merge hubs.
		for _, hv := range fr.hubs {
			key := hubKey(hv)
			hs, exists := hubMap[key]
			if !exists {
				merged := &MergedHub{
					HubID: hv.ID,
					Name:  hv.Name,
					URL:   hv.URL,
				}
				hs = &hubState{hub: merged}
				hubMap[key] = hs
			}
			hs.sources = append(hs.sources, HubSource{
				SourceName: fr.name,
				Name:       hv.Name,
				URL:        hv.URL,
				CanManage:  hv.CanManage,
				OrgName:    hv.OrgName,
			})
		}

		// Merge agents.
		for _, fa := range fr.agents {
			key := agentKeyFromFleet(fa)
			ma, exists := agentMap[key]
			if !exists {
				ma = &MergedAgent{
					AgentID:               fa.AgentID,
					HubID:                 fa.HubID,
					HubName:               fa.Hub,
					MachineRegistrationID: fa.MachineRegistrationID,
					MachineName:           fa.ID,
					DisplayName:           agentDisplayName(fa),
					Hostname:              derefStr(fa.Hostname),
					OS:                    derefStr(fa.OS),
					AgentVersion:          derefStr(fa.AgentVersion),
					Tags:                  fa.Tags,
					Location:              derefStr(fa.Location),
					Owner:                 derefStr(fa.Owner),
					RegisteredAt:          derefStr(fa.RegisteredAt),
					LastSeen:              derefStr(fa.LastSeen),
					SessionCount:          fa.SessionCount,
					Services:              convertServices(fa.Services),
					Capabilities:          fa.Capabilities,
				}
				agentMap[key] = ma
			} else {
				// Subsequent source for the same agent: first non-empty
				// scalar wins, max session count wins.
				if ma.HubName == "" {
					ma.HubName = fa.Hub
				}
				if ma.Hostname == "" {
					ma.Hostname = derefStr(fa.Hostname)
				}
				if ma.OS == "" {
					ma.OS = derefStr(fa.OS)
				}
				if ma.AgentVersion == "" {
					ma.AgentVersion = derefStr(fa.AgentVersion)
				}
				if len(ma.Tags) == 0 && len(fa.Tags) > 0 {
					ma.Tags = fa.Tags
				}
				if ma.Location == "" {
					ma.Location = derefStr(fa.Location)
				}
				if ma.Owner == "" {
					ma.Owner = derefStr(fa.Owner)
				}
				if ma.RegisteredAt == "" {
					ma.RegisteredAt = derefStr(fa.RegisteredAt)
				}
				if ma.LastSeen == "" {
					ma.LastSeen = derefStr(fa.LastSeen)
				}
				if fa.SessionCount > ma.SessionCount {
					ma.SessionCount = fa.SessionCount
				}
				if len(ma.Services) == 0 {
					ma.Services = convertServices(fa.Services)
				}
				if ma.Capabilities == nil && fa.Capabilities != nil {
					ma.Capabilities = fa.Capabilities
				}
			}
			// Online if any source says connected.
			if fa.AgentConnected {
				ma.Online = true
			}
			// Record this source if not already present.
			ma.Sources = appendUnique(ma.Sources, fr.name)
		}
	}

	// Finalise hub display names and preferred sources.
	var hubs []MergedHub
	for _, hs := range hubMap {
		hs.hub.Sources = hs.sources
		hs.hub.Name = pickHubName(hs.hub.Name, hs.sources)
		hs.hub.URL = pickHubURL(hs.sources)
		hs.hub.PreferredSource = pickPreferredSource(hs.sources)
		hubs = append(hubs, *hs.hub)
	}

	// Finalise agents: compute LinkedAgentIDs (same agentId, different hubId).
	// Build agentId -> []key index.
	agentIDIndex := make(map[string][]string)
	for key, ma := range agentMap {
		if ma.AgentID != "" {
			agentIDIndex[ma.AgentID] = append(agentIDIndex[ma.AgentID], key)
		}
	}
	var agents []MergedAgent
	for key, ma := range agentMap {
		if ma.AgentID != "" {
			for _, otherKey := range agentIDIndex[ma.AgentID] {
				if otherKey != key {
					ma.LinkedAgentIDs = appendUnique(ma.LinkedAgentIDs, otherKey)
				}
			}
		}
		agents = append(agents, *ma)
	}

	return Result{
		Hubs:               hubs,
		Agents:             agents,
		UnreachableSources: unreachable,
	}, nil
}

// ── Key helpers ────────────────────────────────────────────────────

// hubKey returns the deduplication key for a HubVisibility entry.
// Uses hubId when present (1.1 hubs); falls back to name+URL for 1.0.
func hubKey(hv portal.HubVisibility) string {
	if hv.ID != "" {
		return "id:" + hv.ID
	}
	return "name:" + hv.Name + "|" + hv.URL
}

// agentKeyFromFleet returns the deduplication key for a FleetAgent.
// Uses hubId+agentId when both present; falls back to hub name + agent id.
func agentKeyFromFleet(fa portalclient.FleetAgent) string {
	if fa.HubID != "" && fa.AgentID != "" {
		return fa.HubID + "|" + fa.AgentID
	}
	return fa.Hub + "|" + fa.ID
}

// ── Selection helpers ──────────────────────────────────────────────

// pickHubName returns the display name for a hub. Prefers the "local"
// source per section 7.3; falls back to the first canManage source
// alphabetically, then the first source alphabetically.
func pickHubName(current string, sources []HubSource) string {
	for _, s := range sources {
		if s.SourceName == "local" {
			return s.Name
		}
	}
	// First canManage source wins (stable iteration not guaranteed by
	// map, but sources list is small and names are operator-assigned).
	for _, s := range sources {
		if s.CanManage {
			return s.Name
		}
	}
	if len(sources) > 0 {
		return sources[0].Name
	}
	return current
}

// pickHubURL returns the URL from the first canManage source.
func pickHubURL(sources []HubSource) string {
	for _, s := range sources {
		if s.SourceName == "local" {
			return s.URL
		}
	}
	for _, s := range sources {
		if s.CanManage {
			return s.URL
		}
	}
	if len(sources) > 0 {
		return sources[0].URL
	}
	return ""
}

// pickPreferredSource returns the source name to use for admin
// actions. Prefers "local" among canManage sources, then the first
// canManage source.
func pickPreferredSource(sources []HubSource) string {
	for _, s := range sources {
		if s.CanManage && s.SourceName == "local" {
			return s.SourceName
		}
	}
	for _, s := range sources {
		if s.CanManage {
			return s.SourceName
		}
	}
	if len(sources) > 0 {
		return sources[0].SourceName
	}
	return ""
}

// ── Conversion helpers ─────────────────────────────────────────────

func agentDisplayName(fa portalclient.FleetAgent) string {
	if fa.DisplayName != nil && *fa.DisplayName != "" {
		return *fa.DisplayName
	}
	return fa.ID
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func convertServices(raw []map[string]any) []ServiceInfo {
	if len(raw) == 0 {
		return nil
	}
	out := make([]ServiceInfo, 0, len(raw))
	for _, m := range raw {
		si := ServiceInfo{}
		if v, ok := m["port"].(float64); ok {
			si.Port = int(v)
		}
		if v, ok := m["name"].(string); ok {
			si.Name = v
		}
		if v, ok := m["protocol"].(string); ok {
			si.Protocol = v
		}
		out = append(out, si)
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, existing := range s {
		if existing == v {
			return s
		}
	}
	return append(s, v)
}
