package portal

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fleetClient is the HTTP client used to fetch /api/status from
// upstream hubs during fleet aggregation. Separate from
// adminProxyClient because the timeouts and reuse patterns differ:
// fleet calls go to many hubs in parallel and should fail fast on
// any one slow hub rather than blocking the whole aggregation.
var fleetClient = &http.Client{
	Timeout: 10 * time.Second,
}

// handleFleetAgents implements section 5 of DESIGN-portal.md: the
// cross-hub agent aggregation endpoint.
//
// Iterates the user's manageable hubs, fetches each hub's
// /api/status with its stored viewer token, merges the machines
// arrays into a flat list, and tags each agent record with the hub
// name and URL it belongs to. Per-hub failures are logged and
// skipped (the response includes agents from reachable hubs rather
// than failing the whole aggregation).
//
// The optional orgId query parameter is implementation-defined; the
// generic Server does not interpret it. Stores that model orgs can
// expose org filtering by parsing the parameter inside their
// ListHubsForUser implementation.
func (s *Server) handleFleetAgents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	hubs, err := s.Store.ListHubsForUser(r.Context(), user)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Resolve each manageable hub to its full record (including
	// admin token and viewer token). LookupHubForUser is what gives
	// us the hub URL and the credentials we need for the outbound
	// /api/status call.
	var manageable []*Hub
	for _, hv := range hubs {
		if !hv.CanManage {
			continue
		}
		hub, _, err := s.Store.LookupHubForUser(r.Context(), user, hv.Name)
		if err != nil {
			// User can list it but not look it up? Treat as a
			// store-side race; skip the hub rather than failing
			// the aggregation.
			s.logf("portal: fleet: lookup %q: %v", hv.Name, err)
			continue
		}
		manageable = append(manageable, hub)
	}

	var (
		mu     sync.Mutex
		agents []map[string]any
		wg     sync.WaitGroup
	)
	for _, hub := range manageable {
		wg.Add(1)
		go func(h *Hub) {
			defer wg.Done()
			fetched := s.fetchHubAgents(r, h)
			if len(fetched) == 0 {
				return
			}
			mu.Lock()
			agents = append(agents, fetched...)
			mu.Unlock()
		}(hub)
	}
	wg.Wait()

	// Always return a non-nil agents slice so the JSON encoder
	// produces "agents":[] rather than "agents":null when nothing
	// was reachable. Clients can rely on the field being present.
	if agents == nil {
		agents = []map[string]any{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

// fetchHubAgents queries one hub's /api/status, parses the machines
// array, tags each entry with hub + hubUrl, and returns the result.
// Errors are logged and translated to "no agents from this hub"
// (the empty return) so the parent aggregation does not fail.
func (s *Server) fetchHubAgents(parent *http.Request, hub *Hub) []map[string]any {
	target := strings.TrimRight(hub.URL, "/") + "/api/status"
	req, err := http.NewRequestWithContext(parent.Context(), http.MethodGet, target, nil)
	if err != nil {
		s.logf("portal: fleet: build %s request: %v", hub.Name, err)
		return nil
	}
	if hub.ViewerToken != "" {
		req.Header.Set("Authorization", "Bearer "+hub.ViewerToken)
	}

	resp, err := fleetClient.Do(req)
	if err != nil {
		s.logf("portal: fleet: %s %s: %v", hub.Name, target, err)
		return nil
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		s.logf("portal: fleet: %s %s: HTTP %d", hub.Name, target, resp.StatusCode)
		return nil
	}

	var status struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		s.logf("portal: fleet: %s decode: %v", hub.Name, err)
		return nil
	}

	out := make([]map[string]any, 0, len(status.Machines))
	for _, m := range status.Machines {
		// Add the two fields the spec requires; everything else is
		// passthrough from the hub status shape.
		m["hub"] = hub.Name
		m["hubUrl"] = hub.URL
		out = append(out, m)
	}
	return out
}
