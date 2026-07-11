package main

import (
	"strings"
	"testing"
)

// sampleProfile is a minimal but well-formed count-mode profile spanning two
// packages: pkg "a" is fully covered, pkg "b" is half covered.
const sampleProfile = `mode: count
github.com/paulmooreparks/tela/internal/a/one.go:10.20,12.2 3 5
github.com/paulmooreparks/tela/internal/a/two.go:1.1,4.10 2 1
github.com/paulmooreparks/tela/internal/b/one.go:5.1,7.2 4 2
github.com/paulmooreparks/tela/internal/b/two.go:9.1,11.2 4 0
`

func TestParseProfile(t *testing.T) {
	stats, err := parseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("parseProfile: %v", err)
	}

	a := stats["github.com/paulmooreparks/tela/internal/a"]
	if a.total != 5 || a.covered != 5 {
		t.Errorf("pkg a: got total=%d covered=%d, want total=5 covered=5", a.total, a.covered)
	}

	b := stats["github.com/paulmooreparks/tela/internal/b"]
	// 4 statements executed (count 2), 4 statements not executed (count 0).
	if b.total != 8 || b.covered != 4 {
		t.Errorf("pkg b: got total=%d covered=%d, want total=8 covered=4", b.total, b.covered)
	}
}

func TestParseProfileMissingModeHeader(t *testing.T) {
	_, err := parseProfile(strings.NewReader("github.com/x/y/z.go:1.1,2.2 1 1\n"))
	if err == nil {
		t.Fatal("expected error for profile without a mode: header, got nil")
	}
	if !strings.Contains(err.Error(), "mode:") {
		t.Errorf("error should mention the missing mode header, got: %v", err)
	}
}

func TestParseProfileMalformedLine(t *testing.T) {
	bad := "mode: count\nthis is not a valid profile line\n"
	if _, err := parseProfile(strings.NewReader(bad)); err == nil {
		t.Fatal("expected error for malformed profile line, got nil")
	}
}

// AC: a package at or above its floor passes.
func TestEvaluatePassAtAndAboveFloor(t *testing.T) {
	floors := map[string]int{
		"github.com/paulmooreparks/tela/internal/a": 90, // measured 100, above
		"github.com/paulmooreparks/tela/internal/b": 50, // measured exactly 50, at floor
	}
	stats := map[string]pkgStats{
		"github.com/paulmooreparks/tela/internal/a": {total: 5, covered: 5},
		"github.com/paulmooreparks/tela/internal/b": {total: 8, covered: 4},
	}
	results := evaluate(floors, stats)
	for _, r := range results {
		if !r.Pass {
			t.Errorf("pkg %s: measured=%.1f floor=%d, expected PASS", r.Pkg, r.Measured, r.Floor)
		}
	}
}

// AC: a package below its floor fails, and the report row names the package,
// its measured percentage, and its floor.
func TestEvaluateFailBelowFloor(t *testing.T) {
	floors := map[string]int{
		"github.com/paulmooreparks/tela/internal/b": 60, // measured 50, below
	}
	stats := map[string]pkgStats{
		"github.com/paulmooreparks/tela/internal/b": {total: 8, covered: 4},
	}
	results := evaluate(floors, stats)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Pass {
		t.Fatalf("expected FAIL for measured 50 below floor 60, got PASS")
	}
	line := r.String()
	for _, want := range []string{"internal/b", "measured=50.0%", "floor=60%", "FAIL"} {
		if !strings.Contains(line, want) {
			t.Errorf("report row %q missing %q", line, want)
		}
	}
}

// AC: a package entirely absent from the profile is treated as 0% and fails
// whenever its floor is above 0.
func TestEvaluateAbsentPackageIsZero(t *testing.T) {
	floors := map[string]int{
		"github.com/paulmooreparks/tela/internal/gone": 1,
	}
	stats := map[string]pkgStats{} // package absent from profile entirely
	results := evaluate(floors, stats)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Measured != 0 {
		t.Errorf("absent package: got measured=%.1f, want 0", r.Measured)
	}
	if r.Pass {
		t.Error("absent package with floor 1 should FAIL, got PASS")
	}
}

// A floor of 0 on an absent package still passes (0 >= 0): documents the clamp
// boundary the internal/client backstop deliberately avoids.
func TestEvaluateAbsentPackageZeroFloorPasses(t *testing.T) {
	floors := map[string]int{"github.com/x/y": 0}
	results := evaluate(floors, map[string]pkgStats{})
	if !results[0].Pass {
		t.Error("absent package with floor 0 should PASS (0 >= 0)")
	}
}

// AC: a malformed floors.yaml fails loudly rather than silently passing.
func TestParseFloorsMalformed(t *testing.T) {
	bad := []byte("packages: [this is not a map: {{{\n")
	if _, err := parseFloors(bad); err == nil {
		t.Fatal("expected error for malformed floors YAML, got nil")
	}
}

// An empty or package-less floors file is also a loud error.
func TestParseFloorsEmpty(t *testing.T) {
	if _, err := parseFloors([]byte("packages: {}\n")); err == nil {
		t.Fatal("expected error for floors file with no packages, got nil")
	}
}

func TestParseFloorsValid(t *testing.T) {
	data := []byte("packages:\n  github.com/x/y: 42\n  github.com/x/z: 7\n")
	floors, err := parseFloors(data)
	if err != nil {
		t.Fatalf("parseFloors: %v", err)
	}
	if floors["github.com/x/y"] != 42 || floors["github.com/x/z"] != 7 {
		t.Errorf("got %v, want y=42 z=7", floors)
	}
}

// AC: a missing floors.yaml fails loudly.
func TestLoadFloorsMissingFile(t *testing.T) {
	if _, err := loadFloors("nonexistent-floors-file.yaml"); err == nil {
		t.Fatal("expected error for missing floors file, got nil")
	}
}
