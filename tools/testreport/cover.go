package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// modulePrefix is the workspace's module path. Profile lines name their files
// by import path, and the docs want workspace-relative ones.
const modulePrefix = "github.com/yohimik/dispat/"

// coverage accumulates coverage profiles keyed by *block* rather than by line.
//
// This is the whole reason the aggregation is not `go tool cover -func`
// arithmetic: the merged profile is a concatenation of the seven profiles a
// full run produces, so the same block appears once per profile that compiled
// it — the CLI's own tests and the instrumented binary between them cover most
// of services/dispat twice. Summing the lines would count those statements
// two, three or seven times and report a percentage of a denominator that does
// not exist. Keyed by block, a repeat costs nothing: the statement count is
// recorded once and the block counts as covered if *any* profile reached it,
// which is exactly what `go tool cover` does when it merges them for the
// badge.
//
// It also means the profiles' modes (the unit ones are `atomic`, the
// integration one arrives from `go tool covdata textfmt` as `set`) never
// matter: nothing here reads an execution count except to compare it with
// zero.
type coverage struct {
	statements map[string]int
	covered    map[string]bool
}

func newCoverage() *coverage {
	return &coverage{statements: map[string]int{}, covered: map[string]bool{}}
}

// addFile folds one coverage profile into the accumulator.
func (c *coverage) addFile(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := c.add(f); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// add folds one coverage profile into the accumulator.
//
// The format is one block per line, `<file>:<from>,<to> <statements> <count>`,
// under a single `mode:` header. Anything else is a corrupt profile and stops
// the report rather than skewing it.
func (c *coverage) add(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}
		// Split from the right: the block key holds colons and commas, the
		// two trailing fields are plain integers.
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return fmt.Errorf("line %d: expected `<block> <statements> <count>`, got %q", line, text)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return fmt.Errorf("line %d: statement count: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("line %d: execution count: %w", line, err)
		}
		key := fields[0]
		if stmts > c.statements[key] {
			c.statements[key] = stmts
		}
		if count > 0 {
			c.covered[key] = true
		}
	}
	return sc.Err()
}

// stats totals every block held.
func (c *coverage) stats() Stats {
	var s Stats
	for key, stmts := range c.statements {
		s.Statements += stmts
		if c.covered[key] {
			s.Covered += stmts
		}
	}
	s.Percent = percent(s.Covered, s.Statements)
	return s
}

// modules groups the blocks into the workspace's modules and the packages
// inside them, sorted by path so the report is byte-identical for the same
// input.
func (c *coverage) modules() []Module {
	byPackage := map[string]*Stats{}
	for key, stmts := range c.statements {
		pkg := packageOf(key)
		s := byPackage[pkg]
		if s == nil {
			s = &Stats{}
			byPackage[pkg] = s
		}
		s.Statements += stmts
		if c.covered[key] {
			s.Covered += stmts
		}
	}

	byModule := map[string]*Module{}
	for pkg, s := range byPackage {
		s.Percent = percent(s.Covered, s.Statements)
		mod := moduleOf(pkg)
		m := byModule[mod]
		if m == nil {
			m = &Module{Path: mod}
			byModule[mod] = m
		}
		m.Statements += s.Statements
		m.Covered += s.Covered
		m.Packages = append(m.Packages, Package{Path: pkg, Stats: *s})
	}

	out := make([]Module, 0, len(byModule))
	for _, m := range byModule {
		m.Percent = percent(m.Covered, m.Statements)
		sort.Slice(m.Packages, func(i, j int) bool { return m.Packages[i].Path < m.Packages[j].Path })
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// packageOf turns a block key into the workspace-relative package that holds
// it: `github.com/yohimik/dispat/pkg/ccme/parse.go:12.34,15.2` -> `pkg/ccme`.
func packageOf(blockKey string) string {
	file := blockKey
	if i := strings.LastIndex(file, ":"); i >= 0 {
		file = file[:i]
	}
	return strings.TrimPrefix(path.Dir(file), modulePrefix)
}

// moduleOf names the module a workspace-relative package belongs to, from the
// list go.work declares.
//
// It used to be a rule instead: every module sits exactly two segments deep.
// That was true of six of them and false of `tools`, which is one segment,
// so `tools/testreport` reported as a module of its own and the docs table
// grew a row for a package rather than for the module holding it. The rule
// stays as the fallback for a caller with no workspace above it.
func moduleOf(pkg string) string {
	return workspaceUse().module(pkg)
}

// useList is the modules go.work declares, workspace-relative.
type useList []string

// module names the module a package belongs to: the longest declared module
// the package sits in, or the two-segment fallback when the list holds none.
//
// Longest rather than first, because nothing forbids a workspace from
// declaring both a folder and something under it, and the nearer module is
// the one a package actually belongs to.
func (u useList) module(pkg string) string {
	best := ""
	for _, candidate := range u {
		if pkg != candidate && !strings.HasPrefix(pkg, candidate+"/") {
			continue
		}
		if len(candidate) > len(best) {
			best = candidate
		}
	}
	if best != "" {
		return best
	}
	parts := strings.SplitN(pkg, "/", 3)
	if len(parts) < 2 {
		return pkg
	}
	return parts[0] + "/" + parts[1]
}

// The workspace's use list, read once per process.
//
// Once because moduleOf runs for every coverage block of every profile, which
// is tens of thousands of calls per report, and because the answer cannot
// change while the program runs. The two values are a memoised lookup rather
// than state: nothing writes them again, and nothing reads them expecting
// anything but the file's contents.
var (
	useOnce sync.Once
	useOf   useList
)

func workspaceUse() useList {
	useOnce.Do(func() { useOf = readUse() })
	return useOf
}

// readUse reads the workspace's use list from the go.work above the working
// directory, and answers with none when there is no answer to be had.
//
// It is a function rather than the memoised closure's body so that both ways
// of having none are reachable more than once in a process: no workspace
// above the caller at all, and a go.work that is there and unreadable. Each
// is a warning rather than a failure, because the two-segment fallback still
// names a module for every package, and a report is worth more than a
// refusal.
func readUse() useList {
	root, err := repoRoot()
	if err != nil {
		logf(levelWarn, "%v; modules are grouped by the two-segment rule instead", err)
		return nil
	}
	body, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		logf(levelWarn, "could not read %s: %v; modules are grouped by the two-segment rule instead",
			filepath.Join(root, "go.work"), err)
		return nil
	}
	out := parseUse(string(body))
	logf(levelDebug, "go.work declares %d modules: %s", len(out), strings.Join(out, " "))
	return out
}

// parseUse reads the `use` directives of a go.work, in both the block form
// this workspace writes and the one-line form.
//
// A parse rather than `go list`: this program runs inside a container stage
// whose whole point is that it needs no module graph resolved, and the
// directive is three lines of syntax.
func parseUse(body string) useList {
	var out useList
	block := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case block && line == ")":
			block = false
		case block:
			out = append(out, cleanUse(line))
		case line == "use (":
			block = true
		case strings.HasPrefix(line, "use "):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "use "))
			if rest == "(" {
				block = true
				continue
			}
			out = append(out, cleanUse(rest))
		}
	}
	return out
}

// cleanUse turns one `use` path into the workspace-relative form coverage
// keys carry: no quotes, no leading `./`, no trailing slash.
func cleanUse(path string) string {
	path = strings.Trim(path, `"`)
	path = strings.TrimPrefix(path, "./")
	return strings.TrimSuffix(path, "/")
}

// percent is the one place a coverage ratio is turned into a number, so the
// empty-profile case cannot divide by zero in one caller and not another.
func percent(covered, statements int) float64 {
	if statements == 0 {
		return 0
	}
	// Rounded to the tenth the docs and the badge both print, so the JSON
	// carries no precision the site would have to decide how to trim.
	return float64(int(float64(covered)/float64(statements)*1000+0.5)) / 10
}
