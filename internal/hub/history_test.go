package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// These tests pin the current behavior of the session-history ring buffer
// (recordEvent / snapshotHistory) and its /api/history handler
// (handleAPIHistory) before the audit-log retention work (tela-26 /
// GitHub #25) changes this subsystem. They are test-only; no production
// code in internal/hub/hub.go is modified.
//
// The subject is a fixed-size ring: recordEvent writes at historyHead,
// advances historyHead = (historyHead + 1) % maxHistory, and increments
// historyCount until it saturates at maxHistory. snapshotHistory walks
// backward from historyHead-1 for historyCount entries, so the contract
// is most-recent-event-first, oldest event evicted once more than
// maxHistory events have been recorded.
//
// State isolation: history, historyHead, and historyCount are
// package-level globals shared across the whole test binary. Every test
// here calls resetHistoryForTest, which zeroes the ring and registers a
// Cleanup that restores the prior contents under historyMu. This mirrors
// the save/restore-under-lock pattern TestAdminLogs_DefaultAndLimit uses
// for the log ring, and is applied consistently across every test in this
// file (no ResetForTesting()) so no test leaks ring state to another.

// resetHistoryForTest zeroes the package-level history ring buffer and
// registers a Cleanup that restores whatever the buffer held before the
// test ran. history is a fixed-size array, so the save and restore copy
// it by value.
func resetHistoryForTest(t *testing.T) {
	t.Helper()
	historyMu.Lock()
	savedHistory := history
	savedHead := historyHead
	savedCount := historyCount
	for i := range history {
		history[i] = historyEvent{}
	}
	historyHead = 0
	historyCount = 0
	historyMu.Unlock()
	t.Cleanup(func() {
		historyMu.Lock()
		history = savedHistory
		historyHead = savedHead
		historyCount = savedCount
		historyMu.Unlock()
	})
}

// historyCountUnderLock reads historyCount while holding historyMu so the
// concurrency-sensitive tests never reach into the global from outside the
// lock the production code uses.
func historyCountUnderLock() int {
	historyMu.Lock()
	defer historyMu.Unlock()
	return historyCount
}

// AC 7637: empty buffer returns a non-nil, zero-length slice.
func TestSnapshotHistory_Empty(t *testing.T) {
	resetHistoryForTest(t)
	got := snapshotHistory()
	if got == nil {
		t.Fatal("snapshotHistory() on a freshly reset ring returned nil, want a non-nil zero-length slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

// AC 7638: one recorded event comes back exactly.
func TestSnapshotHistory_SingleElement(t *testing.T) {
	resetHistoryForTest(t)
	recordEvent("m1", "connect", "e0")
	got := snapshotHistory()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MachineID != "m1" || got[0].Event != "connect" || got[0].Detail != "e0" {
		t.Errorf("event = %+v, want {MachineID:m1 Event:connect Detail:e0}", got[0])
	}
}

// AC 7639: partial fill (k < maxHistory) returns k events, most-recent-first.
func TestSnapshotHistory_PartialFill(t *testing.T) {
	resetHistoryForTest(t)
	const k = 5
	for i := 0; i < k; i++ {
		recordEvent("m1", "connect", fmt.Sprintf("e%d", i))
	}
	got := snapshotHistory()
	if len(got) != k {
		t.Fatalf("len = %d, want %d", len(got), k)
	}
	for i := 0; i < k; i++ {
		want := fmt.Sprintf("e%d", k-1-i)
		if got[i].Detail != want {
			t.Errorf("got[%d].Detail = %q, want %q (most-recent-first)", i, got[i].Detail, want)
		}
	}
}

// AC 7640: exactly maxHistory events, all returned, most-recent-first, none dropped.
func TestSnapshotHistory_ExactCapacity(t *testing.T) {
	resetHistoryForTest(t)
	for i := 0; i < maxHistory; i++ {
		recordEvent("m1", "connect", fmt.Sprintf("e%d", i))
	}
	got := snapshotHistory()
	if len(got) != maxHistory {
		t.Fatalf("len = %d, want %d", len(got), maxHistory)
	}
	for i := 0; i < maxHistory; i++ {
		want := fmt.Sprintf("e%d", maxHistory-1-i)
		if got[i].Detail != want {
			t.Errorf("got[%d].Detail = %q, want %q", i, got[i].Detail, want)
		}
	}
}

// AC 7641: wrap-around past capacity for two overflow amounts. The
// snapshot never grows past the cap and returns the last maxHistory
// recorded events (the oldest n evicted), most-recent-first.
func TestSnapshotHistory_WrapAround(t *testing.T) {
	for _, n := range []int{1, 37} {
		n := n
		t.Run(fmt.Sprintf("overflow=%d", n), func(t *testing.T) {
			resetHistoryForTest(t)
			total := maxHistory + n
			for i := 0; i < total; i++ {
				recordEvent("m1", "connect", fmt.Sprintf("e%d", i))
			}
			got := snapshotHistory()
			if len(got) != maxHistory {
				t.Fatalf("len = %d, want %d (must never grow past the cap)", len(got), maxHistory)
			}
			if c := historyCountUnderLock(); c != maxHistory {
				t.Errorf("historyCount = %d, want %d (saturated, must not exceed cap)", c, maxHistory)
			}
			// The last maxHistory recorded are e(n)..e(total-1); the oldest
			// n (e0..e(n-1)) were evicted. Verify most-recent-first.
			for i := 0; i < maxHistory; i++ {
				want := fmt.Sprintf("e%d", total-1-i)
				if got[i].Detail != want {
					t.Errorf("got[%d].Detail = %q, want %q", i, got[i].Detail, want)
				}
			}
			// The evicted events must not appear anywhere in the snapshot.
			evicted := make(map[string]bool, n)
			for i := 0; i < n; i++ {
				evicted[fmt.Sprintf("e%d", i)] = true
			}
			for _, e := range got {
				if evicted[e.Detail] {
					t.Errorf("evicted event %q still present in snapshot", e.Detail)
				}
			}
		})
	}
}

// AC 7642: multiple wrap cycles (2*maxHistory + 3) still yield the correct
// last-100 window, most-recent-first, with no off-by-one on the second lap.
func TestSnapshotHistory_MultipleWrapCycles(t *testing.T) {
	resetHistoryForTest(t)
	total := 2*maxHistory + 3
	for i := 0; i < total; i++ {
		recordEvent("m1", "connect", fmt.Sprintf("e%d", i))
	}
	got := snapshotHistory()
	if len(got) != maxHistory {
		t.Fatalf("len = %d, want %d", len(got), maxHistory)
	}
	for i := 0; i < maxHistory; i++ {
		want := fmt.Sprintf("e%d", total-1-i)
		if got[i].Detail != want {
			t.Errorf("got[%d].Detail = %q, want %q (off-by-one across the second lap)", i, got[i].Detail, want)
		}
	}
}

// AC 7643: explicit ordering pin. With Detail values e0,e1,e2 the snapshot
// is the reverse of insertion order and snapshot[0] is the most recent.
//
// Per the resolved open question on timestamp granularity, correctness
// assertions rely on Detail plus positional (most-recent-first) ordering.
// recordEvent stamps time.Now().UTC() at RFC3339 second resolution, so
// events recorded within one wall-clock second share an identical
// Timestamp string; equality or strict-monotonicity assertions across
// them would be flaky by construction. The only Timestamp assertion made
// here is presence/shape (non-empty and RFC3339-parseable) to prove the
// field is wired, not a magic string.
func TestSnapshotHistory_OrderingPin(t *testing.T) {
	resetHistoryForTest(t)
	inserted := []string{"e0", "e1", "e2"}
	for _, d := range inserted {
		recordEvent("m1", "connect", d)
	}
	got := snapshotHistory()
	if len(got) != len(inserted) {
		t.Fatalf("len = %d, want %d", len(got), len(inserted))
	}
	if got[0].Detail != "e2" {
		t.Errorf("got[0].Detail = %q, want e2 (most recent first)", got[0].Detail)
	}
	for i := range got {
		want := inserted[len(inserted)-1-i]
		if got[i].Detail != want {
			t.Errorf("got[%d].Detail = %q, want %q (reverse of insertion order)", i, got[i].Detail, want)
		}
	}
	// Timestamp presence/shape only (see doc comment above).
	if got[0].Timestamp == "" {
		t.Error("Timestamp is empty, want an RFC3339 stamp")
	}
	if _, err := time.Parse(time.RFC3339, got[0].Timestamp); err != nil {
		t.Errorf("Timestamp %q does not parse as RFC3339: %v", got[0].Timestamp, err)
	}
}

// AC 7644: concurrent append-while-snapshot. Fixed iteration counts (no
// wall-clock duration bound), matching the repo's existing concurrency
// precedent in auth_test.go (TestConcurrentReads / TestReload_
// ConcurrentWithReads). The test's job is to create enough concurrent
// read/write traffic that a broken historyMu would trip the -race
// detector; CI's `go test -race -count=1 ./...` gate is the enforcement
// mechanism for the mutex claim. Locally (without -race) it still asserts
// no panic, every snapshot len <= maxHistory, and every returned event is
// well-formed (MachineID/Event drawn from a known fixture set, never a
// torn/corrupted string).
func TestHistory_ConcurrentAppendAndSnapshot(t *testing.T) {
	resetHistoryForTest(t)

	machineIDs := []string{"m0", "m1", "m2", "m3"}
	eventTypes := []string{"connect", "disconnect", "register"}
	knownMachines := map[string]bool{"m0": true, "m1": true, "m2": true, "m3": true}
	knownEvents := map[string]bool{"connect": true, "disconnect": true, "register": true}

	const (
		writers         = 20
		writesPerWriter = 50
		readers         = 8
		readsPerReader  = 200
	)

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	fail := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				m := machineIDs[(w+i)%len(machineIDs)]
				e := eventTypes[(w+i)%len(eventTypes)]
				recordEvent(m, e, "d")
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				snap := snapshotHistory()
				if len(snap) > maxHistory {
					fail(fmt.Errorf("snapshot len = %d, exceeds maxHistory %d", len(snap), maxHistory))
					return
				}
				for _, ev := range snap {
					if !knownMachines[ev.MachineID] {
						fail(fmt.Errorf("torn/corrupted MachineID %q not in fixture set", ev.MachineID))
						return
					}
					if !knownEvents[ev.Event] {
						fail(fmt.Errorf("torn/corrupted Event %q not in fixture set", ev.Event))
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	// After all writers finish, the ring is saturated (writers*writesPerWriter
	// = 1000 > maxHistory) and stable.
	if c := historyCountUnderLock(); c != maxHistory {
		t.Errorf("final historyCount = %d, want %d", c, maxHistory)
	}
}

// AC 7645: handleAPIHistory with auth disabled returns 200 with an events
// array matching recorded events and a timestamp field, no error key.
func TestHandleAPIHistory_AuthDisabled(t *testing.T) {
	resetHistoryForTest(t)
	restore := withTestConfig(t, &hubConfig{}) // no tokens -> disabled/open hub
	defer restore()

	recordEvent("m1", "connect", "e0")
	recordEvent("m2", "disconnect", "e1")

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rr := httptest.NewRecorder()
	handleAPIHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("response carries an error key, want none: %s", rr.Body.String())
	}
	if _, ok := raw["timestamp"]; !ok {
		t.Error("response missing timestamp field")
	}
	var events []historyEvent
	if err := json.Unmarshal(raw["events"], &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	// Most-recent-first: e1 then e0.
	if events[0].Detail != "e1" || events[1].Detail != "e0" {
		t.Errorf("events order = [%q, %q], want [e1, e0]", events[0].Detail, events[1].Detail)
	}
	if events[0].MachineID != "m2" || events[1].MachineID != "m1" {
		t.Errorf("event machine ids = [%q, %q], want [m2, m1]", events[0].MachineID, events[1].MachineID)
	}
}

// AC 7646: handleAPIHistory with auth enabled and no token returns 401
// with body {"error":"auth_required","events":[]} (events present as an
// empty array, not omitted or null).
func TestHandleAPIHistory_AuthEnabledNoToken(t *testing.T) {
	resetHistoryForTest(t)
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{Tokens: []tokenEntry{
			{ID: "owner", Token: "tok-owner", HubRole: "owner"},
		}},
	})
	defer restore()

	recordEvent("m1", "connect", "e0")

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rr := httptest.NewRecorder()
	handleAPIHistory(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var gotErr string
	if err := json.Unmarshal(raw["error"], &gotErr); err != nil {
		t.Fatalf("decode error field: %v", err)
	}
	if gotErr != "auth_required" {
		t.Errorf("error = %q, want auth_required", gotErr)
	}
	evRaw, ok := raw["events"]
	if !ok {
		t.Fatal("events key missing, want an empty array")
	}
	var events []historyEvent
	if err := json.Unmarshal(evRaw, &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if events == nil {
		t.Error("events decoded to nil, want an empty (non-null) array")
	}
	if len(events) != 0 {
		t.Errorf("events len = %d, want 0", len(events))
	}
}

// AC 7647: handleAPIHistory with auth enabled and a token restricted to
// one machine returns only that machine's events (server-side
// canViewMachine filtering); other machines' events are excluded.
func TestHandleAPIHistory_PerMachineVisibility(t *testing.T) {
	resetHistoryForTest(t)
	restore := withTestConfig(t, &hubConfig{
		Auth: authConfig{
			Tokens: []tokenEntry{
				{ID: "owner", Token: "tok-owner", HubRole: "owner"},
				{ID: "alice", Token: "tok-alice"}, // plain user, no hub role
			},
			Machines: map[string]machineACL{
				// alice may connect to (and therefore view) machine-a only.
				"machine-a": {ConnectTokens: []connectGrant{{Token: "tok-alice"}}},
			},
		},
	})
	defer restore()

	recordEvent("machine-a", "connect", "a0")
	recordEvent("machine-b", "connect", "b0")
	recordEvent("machine-a", "disconnect", "a1")

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	rr := httptest.NewRecorder()
	handleAPIHistory(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp struct {
		Events []historyEvent `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events len = %d, want 2 (machine-a only)", len(resp.Events))
	}
	for _, e := range resp.Events {
		if e.MachineID != "machine-a" {
			t.Errorf("saw event for %q, want only machine-a (machine-b must be filtered out)", e.MachineID)
		}
	}
}

// AC 7648: OPTIONS preflight returns 204 No Content with CORS headers set
// and no events processed (empty body).
func TestHandleAPIHistory_OptionsPreflight(t *testing.T) {
	resetHistoryForTest(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/history", nil)
	rr := httptest.NewRecorder()
	handleAPIHistory(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods not set on preflight")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body len = %d, want 0 (no events processed on preflight)", rr.Body.Len())
	}
}

// AC 7649: the handler ignores query parameters today. An arbitrary query
// string (e.g. ?limit=1) returns the same full filtered snapshot as one
// without, pinning the absence of a limit/lines param (unlike the sibling
// handleAdminLogs). If a future card adds one, this test's change makes
// the behavior shift visible in the diff.
func TestHandleAPIHistory_QueryParamsIgnored(t *testing.T) {
	resetHistoryForTest(t)
	restore := withTestConfig(t, &hubConfig{}) // open hub, no filtering
	defer restore()

	const recorded = 5
	for i := 0; i < recorded; i++ {
		recordEvent("m1", "connect", fmt.Sprintf("e%d", i))
	}

	getEvents := func(target string) []historyEvent {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rr := httptest.NewRecorder()
		handleAPIHistory(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d for %q, want 200", rr.Code, target)
		}
		var resp struct {
			Events []historyEvent `json:"events"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %q: %v", target, err)
		}
		return resp.Events
	}

	plain := getEvents("/api/history")
	limited := getEvents("/api/history?limit=1")
	if len(plain) != recorded {
		t.Fatalf("plain events = %d, want %d", len(plain), recorded)
	}
	if len(limited) != len(plain) {
		t.Errorf("?limit=1 returned %d events, want %d (query params must be ignored today)", len(limited), len(plain))
	}
}
