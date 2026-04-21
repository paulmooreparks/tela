package hub

import (
	"reflect"
	"testing"
)

// ── serviceFilterName ─────────────────────────────────────────────

func TestServiceFilterName_UsesExplicitName(t *testing.T) {
	if got := serviceFilterName(serviceDesc{Name: "Jellyfin", Port: 8096}); got != "Jellyfin" {
		t.Errorf("serviceFilterName named service = %q, want Jellyfin", got)
	}
}

func TestServiceFilterName_UnnamedServiceUsesPortForm(t *testing.T) {
	// Issue #27 design: unnamed services are filterable by a stable
	// "port-<N>" token so operators can grant access without needing
	// to edit the agent config to add a name.
	if got := serviceFilterName(serviceDesc{Port: 22}); got != "port-22" {
		t.Errorf("serviceFilterName unnamed = %q, want port-22", got)
	}
	if got := serviceFilterName(serviceDesc{Port: 9000}); got != "port-9000" {
		t.Errorf("serviceFilterName unnamed high port = %q, want port-9000", got)
	}
}

// ── filterServicesByName ─────────────────────────────────────────

var filterServicesFixture = []serviceDesc{
	{Name: "Jellyfin", Port: 8096},
	{Name: "SSH", Port: 22},
	{Port: 3000}, // unnamed, filterable as "port-3000"
}

func TestFilterServicesByName_EmptyAllowedYieldsEmpty(t *testing.T) {
	// An empty allowed list is not the same as "no filter". Callers
	// that want unfiltered behavior should not call this helper at
	// all. An empty allowed list here means the grant explicitly
	// allows nothing, so return an empty slice.
	got := filterServicesByName(filterServicesFixture, nil)
	if len(got) != 0 {
		t.Errorf("nil allowed should yield empty result, got %v", got)
	}
	got = filterServicesByName(filterServicesFixture, []string{})
	if len(got) != 0 {
		t.Errorf("empty allowed should yield empty result, got %v", got)
	}
}

func TestFilterServicesByName_OnlyNamedMatches(t *testing.T) {
	got := filterServicesByName(filterServicesFixture, []string{"Jellyfin"})
	want := []serviceDesc{{Name: "Jellyfin", Port: 8096}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filter by Jellyfin = %v, want %v", got, want)
	}
}

func TestFilterServicesByName_MatchesUnnamedViaPortForm(t *testing.T) {
	got := filterServicesByName(filterServicesFixture, []string{"port-3000"})
	want := []serviceDesc{{Port: 3000}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filter by port-3000 = %v, want %v", got, want)
	}
}

func TestFilterServicesByName_MultipleMatches(t *testing.T) {
	got := filterServicesByName(filterServicesFixture, []string{"SSH", "port-3000"})
	want := []serviceDesc{
		{Name: "SSH", Port: 22},
		{Port: 3000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filter by SSH+port-3000 = %v, want %v", got, want)
	}
}

func TestFilterServicesByName_CaseSensitive(t *testing.T) {
	// Service names are case-sensitive. An operator who writes
	// "jellyfin" (lowercase) in their services filter does not match
	// the "Jellyfin" service name in the agent config. This avoids
	// surprises where lowercasing the filter subtly changes match
	// semantics. Operators must match the agent's casing.
	got := filterServicesByName(filterServicesFixture, []string{"jellyfin"})
	if len(got) != 0 {
		t.Errorf("filter case-mismatch should not match, got %v", got)
	}
}

func TestFilterServicesByName_UnknownNamesYieldEmpty(t *testing.T) {
	got := filterServicesByName(filterServicesFixture, []string{"not-a-service"})
	if len(got) != 0 {
		t.Errorf("unknown filter name should yield empty, got %v", got)
	}
}
