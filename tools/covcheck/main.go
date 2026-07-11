// Command covcheck enforces per-package statement-coverage floors for Tela's
// security-critical packages (Andoneer tela-19, GitHub #13).
//
// It reads a Go coverage profile (the text format produced by
// `go test -covermode=count|atomic -coverprofile=<path> ./...`) and a
// floors.yaml keyed by full Go import path, sums statement counts per package,
// and fails (exit 1) when any target package's coverage is below its floor.
//
// A target package that is entirely absent from the profile is treated as 0%
// coverage, not skipped: a package that quietly loses all of its tests must
// fail loudly rather than disappear from the report.
//
// This is CI-only tooling. It lives under tools/ (not cmd/, which holds the
// shipped binaries, and not internal/, which would trip release.yml's path
// filter) and is run via `go run ./tools/covcheck`. It adds no new module
// dependency: floors.yaml is parsed with gopkg.in/yaml.v3, already required.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// pkgStats accumulates statement counts for a single package.
type pkgStats struct {
	total   int // total statements attributed to the package
	covered int // statements executed at least once
}

// floorsFile is the on-disk shape of floors.yaml.
type floorsFile struct {
	Packages map[string]int `yaml:"packages"`
}

// result is the evaluation of one target package against its floor.
type result struct {
	Pkg      string
	Measured float64
	Floor    int
	Pass     bool
}

// String renders the one-line-per-package report row.
func (r result) String() string {
	status := "PASS"
	if !r.Pass {
		status = "FAIL"
	}
	return fmt.Sprintf("%s  measured=%.1f%%  floor=%d%%  %s", r.Pkg, r.Measured, r.Floor, status)
}

func main() {
	profilePath := flag.String("profile", "", "path to a Go coverage profile (go test -coverprofile output)")
	floorsPath := flag.String("floors", "", "path to floors.yaml")
	flag.Parse()

	code, err := run(*profilePath, *floorsPath, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covcheck: %v\n", err)
		os.Exit(2)
	}
	os.Exit(code)
}

// run performs the whole check and returns the process exit code (0 = all
// packages at or above floor, 1 = at least one below floor) plus an operational
// error for the caller to map to a distinct exit code. Splitting run out of
// main keeps the logic testable end to end.
func run(profilePath, floorsPath string, out io.Writer) (int, error) {
	if profilePath == "" || floorsPath == "" {
		return 0, fmt.Errorf("both -profile and -floors are required")
	}

	floors, err := loadFloors(floorsPath)
	if err != nil {
		return 0, err
	}

	f, err := os.Open(profilePath)
	if err != nil {
		return 0, fmt.Errorf("opening coverage profile: %w", err)
	}
	defer f.Close()

	stats, err := parseProfile(f)
	if err != nil {
		return 0, err
	}

	results := evaluate(floors, stats)
	failed := 0
	for _, r := range results {
		fmt.Fprintln(out, r)
		if !r.Pass {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(out, "covcheck: FAIL (%d package(s) below floor)\n", failed)
		return 1, nil
	}
	fmt.Fprintf(out, "covcheck: PASS (all %d package(s) at or above floor)\n", len(results))
	return 0, nil
}

// loadFloors reads and parses floors.yaml from disk. A missing or malformed
// file is a loud error, never a silent pass.
func loadFloors(pth string) (map[string]int, error) {
	data, err := os.ReadFile(pth)
	if err != nil {
		return nil, fmt.Errorf("reading floors file: %w", err)
	}
	return parseFloors(data)
}

// parseFloors decodes the floors.yaml bytes. Split from loadFloors so tests can
// exercise malformed content without a temp file.
func parseFloors(data []byte) (map[string]int, error) {
	var ff floorsFile
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parsing floors YAML: %w", err)
	}
	if len(ff.Packages) == 0 {
		return nil, fmt.Errorf("floors file defines no packages under 'packages:'")
	}
	return ff.Packages, nil
}

// parseProfile reads a Go coverage profile and returns per-package statement
// totals. Each data line has the shape
//
//	<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
//
// The package import path is the file path minus its final segment.
func parseProfile(r io.Reader) (map[string]pkgStats, error) {
	stats := map[string]pkgStats{}
	sc := bufio.NewScanner(r)
	// Coverage profile lines are short, but raise the cap so an unusually long
	// path never trips the default 64 KiB token limit.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	sawHeader := false
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !sawHeader {
			sawHeader = true
			if strings.HasPrefix(line, "mode:") {
				continue
			}
			return nil, fmt.Errorf("coverage profile missing 'mode:' header line (got %q)", line)
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed profile line %d: %q", lineNo, line)
		}

		block := fields[0]
		colon := strings.LastIndex(block, ":")
		if colon < 0 {
			return nil, fmt.Errorf("malformed profile line %d: no file:block separator in %q", lineNo, block)
		}
		pkg := path.Dir(block[:colon])

		numStmt, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("malformed statement count on profile line %d: %w", lineNo, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("malformed execution count on profile line %d: %w", lineNo, err)
		}

		s := stats[pkg]
		s.total += numStmt
		if count > 0 {
			s.covered += numStmt
		}
		stats[pkg] = s
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage profile: %w", err)
	}
	return stats, nil
}

// evaluate compares each floored package against its measured coverage. Packages
// absent from stats measure 0% (total == 0), so a package that lost all of its
// tests fails whenever its floor is above 0. Results are sorted by import path
// for deterministic output.
func evaluate(floors map[string]int, stats map[string]pkgStats) []result {
	pkgs := make([]string, 0, len(floors))
	for p := range floors {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	out := make([]result, 0, len(pkgs))
	for _, p := range pkgs {
		s := stats[p]
		var measured float64
		if s.total > 0 {
			measured = 100 * float64(s.covered) / float64(s.total)
		}
		out = append(out, result{
			Pkg:      p,
			Measured: measured,
			Floor:    floors[p],
			Pass:     measured >= float64(floors[p]),
		})
	}
	return out
}
