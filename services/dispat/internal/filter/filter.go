// Package filter answers the one question every package-selecting command
// asks: which packages does this invocation act on? `dispat release`,
// `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`, `github`
// and `compute` all resolve their --package, --space and --group terms here,
// so they cannot disagree about what a name, a glob or a folder means.
//
// The three flags are the three ways a set of packages is addressed: by their
// own names, by the space folder they live in, and by the versioning group
// they share a version with. They compose by union, so naming a package twice
// over selects it once.
//
// The folder a command was invoked from is not a second mechanism: an
// invocation inside a package folder is turned into the very Filter the user
// would have typed, and then resolved by the same code. Explicit terms beat
// that inference entirely — a flag on the command line is the whole answer.
//
// A term that matches nothing is an error, never an empty selection: a
// command that quietly acts on nothing is how a typo hides.
package filter

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Filter is the raw selection one invocation carries: the flag terms as
// written, plus the folder the command was invoked from. Dir is consulted only
// when neither list holds a term.
type Filter struct {
	// Packages are the --package terms: package names, or globs over them
	// ("*" is every package, "@acme/*" every package under a prefix).
	// Matching is case-insensitive, and no term is reserved — a package named
	// "all" is selected by "all" and by nothing else.
	Packages []string
	// Spaces are the --space terms: the names of configured spaces, or globs
	// over them. A standalone package (a `packages` entry with a path)
	// belongs to no space, so "*" here means every space's packages and not
	// literally every package.
	Spaces []string
	// Groups are the --group terms: the names of versioning groups, or globs
	// over them. A group is a declared versionGroups entry or the implicit
	// group of a space that versions as one, so a group may span spaces and
	// may hold a standalone package no --space term reaches.
	Groups []string
	// Dir is the folder the command was invoked from — the --root flag as the
	// user spelled it. It stands in for the terms they did not type: inside a
	// package folder it selects that package, inside a space folder that
	// space, anywhere else nothing at all.
	Dir string
}

// terms reports whether the command line named anything explicitly.
func (f Filter) terms() bool {
	return len(f.Packages) > 0 || len(f.Spaces) > 0 || len(f.Groups) > 0
}

// Workspace is what a Filter resolves against.
type Workspace struct {
	// Packages are the candidates, in the order the selection must come out
	// in: the plan's dependency order for the commands that have a plan,
	// discovery order for compute.
	Packages []*model.Package
	// Spaces maps every configured space onto its root-relative folder. A
	// standalone package has no entry here by construction, which is exactly
	// why only --package reaches it.
	Spaces map[string]string
	// Groups are the declared versionGroups names. Membership is not listed:
	// a package carries its own group, so the groups a term can match are
	// these plus the ones the packages themselves name — a space that
	// versions as a group declares nothing, and a declared group that nobody
	// joined has no member to carry it.
	Groups []string
	// Root is the monorepo root the space paths are relative to; package dirs
	// were already joined onto it.
	Root string
}

// Result is a resolved selection. The zero value is the inactive one: no
// narrowing in force, which every command reads as "your usual selection".
type Result struct {
	// Names are the selected packages, in Workspace.Packages order; nil when
	// the result is inactive.
	Names []string
	// Description names the selection for a log line or a message —
	// "core", "core, web", `space "libs"` — and is empty when inactive.
	Description string

	active bool
	set    map[string]bool
}

// Active reports whether any narrowing is in force. An inactive result is what
// no terms and an uninformative folder produce.
func (r Result) Active() bool { return r.active }

// Has reports whether the named package is selected — true for every name
// when the result is inactive.
func (r Result) Has(name string) bool {
	if !r.active {
		return true
	}
	return r.set[name]
}

// Keep intersects names with the selection, preserving the caller's order (the
// plan's, so scheduling stays deterministic). An inactive result keeps
// everything, returning names untouched.
func (r Result) Keep(names []string) []string {
	if !r.active {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if r.set[name] {
			out = append(out, name)
		}
	}
	return out
}

// Resolve maps a Filter onto the packages it selects. Explicit terms are
// resolved as given; with none, the invocation folder is turned into the terms
// it stands for and resolved identically.
func Resolve(f Filter, ws Workspace) (Result, error) {
	if !f.terms() {
		if f.Dir == "" {
			return Result{}, nil
		}
		f = infer(f.Dir, ws)
		if !f.terms() {
			return Result{}, nil
		}
	}

	res := Result{active: true, set: make(map[string]bool)}
	for _, term := range f.Packages {
		matched := false
		for _, pkg := range ws.Packages {
			if globx.Match(fold(term), fold(pkg.Name)) {
				res.set[pkg.Name] = true
				matched = true
			}
		}
		if !matched {
			return Result{}, unknownPackage(term, ws)
		}
	}
	for _, term := range f.Spaces {
		names := spaceNames(term, ws)
		if len(names) == 0 {
			return Result{}, unknownSpace(term, ws)
		}
		matched := false
		for _, pkg := range ws.Packages {
			if names[spaceOf(pkg, ws)] {
				res.set[pkg.Name] = true
				matched = true
			}
		}
		if !matched {
			return Result{}, fmt.Errorf("--space %q matches no package (%s holds none)",
				term, nameLabel("space", sortedSet(names)))
		}
	}
	for _, term := range f.Groups {
		names := sortedMatches(term, knownGroups(ws))
		if len(names) == 0 {
			return Result{}, unknownGroup(term, ws)
		}
		want := foldSet(names)
		matched := false
		for _, pkg := range ws.Packages {
			if want[fold(pkg.VersionGroupName())] {
				res.set[pkg.Name] = true
				matched = true
			}
		}
		if !matched {
			return Result{}, fmt.Errorf("--group %q matches no package (%s holds none)",
				term, nameLabel("versioning group", names))
		}
	}

	for _, pkg := range ws.Packages {
		if res.set[pkg.Name] {
			res.Names = append(res.Names, pkg.Name)
		}
	}
	res.Description = describe(f, res.Names)
	return res, nil
}

// infer turns the invocation folder into the terms the user did not type: the
// package whose folder they stand in, or failing that the space. The deepest
// match wins, so standing inside a standalone package nested under another
// package's folder selects the inner one; a package wins an exact-depth tie,
// being the more specific of the two.
//
// A versioning group is never inferred: it is a versioning relationship and
// not a folder, so nowhere is inside one and only --group names it.
func infer(dir string, ws Workspace) Filter {
	target := absClean(dir)
	best, bestLen := Filter{}, -1
	root := absClean(ws.Root)
	for _, pkg := range ws.Packages {
		pkgDir := absClean(pkg.Dir)
		if under(target, pkgDir) && len(pkgDir) > bestLen {
			best, bestLen = Filter{Packages: []string{pkg.Name}}, len(pkgDir)
		}
	}
	for _, name := range sortedKeys(ws.Spaces) {
		spaceDir := absClean(filepath.Join(ws.Root, filepath.FromSlash(ws.Spaces[name])))
		if spaceDir == root {
			// A space rooted at the monorepo root would make standing at the
			// top a narrowing, silently changing what a bare command covers.
			continue
		}
		if under(target, spaceDir) && len(spaceDir) > bestLen {
			best, bestLen = Filter{Spaces: []string{name}}, len(spaceDir)
		}
	}
	return best
}

// spaceNames resolves one --space term onto the configured spaces it matches.
func spaceNames(term string, ws Workspace) map[string]bool {
	out := make(map[string]bool)
	for name := range ws.Spaces {
		if globx.Match(fold(term), fold(name)) {
			out[name] = true
		}
	}
	return out
}

// knownGroups lists every versioning group a --group term may name: the
// declared versionGroups entries, and the groups the packages themselves
// carry. The second half is what makes a space that versions as a group
// selectable — it declares no group, its members simply hold its name.
func knownGroups(ws Workspace) []string {
	seen := make(map[string]bool, len(ws.Groups)+len(ws.Packages))
	out := make([]string, 0, len(ws.Groups))
	add := func(name string) {
		if name == "" || seen[fold(name)] {
			return
		}
		seen[fold(name)] = true
		out = append(out, name)
	}
	for _, name := range ws.Groups {
		add(name)
	}
	for _, pkg := range ws.Packages {
		add(pkg.VersionGroupName())
	}
	sort.Strings(out)
	return out
}

// foldSet keys names for a case-insensitive lookup. A group name arrives from
// two places — a versionGroups key and a space name — and comparing them as
// typed is how the same group stops matching itself.
func foldSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[fold(name)] = true
	}
	return out
}

// spaceOf reports which configured space a package belongs to, by folder
// parenthood: discovery makes every space package a direct sub-folder of its
// space's path. The package's own Space.Name cannot answer this — a standalone
// package carries a synthetic space named after itself, and that name is free
// to collide with a configured space's.
func spaceOf(pkg *model.Package, ws Workspace) string {
	parent := filepath.Dir(absClean(pkg.Dir))
	for _, name := range sortedKeys(ws.Spaces) {
		if parent == absClean(filepath.Join(ws.Root, filepath.FromSlash(ws.Spaces[name]))) {
			return name
		}
	}
	return ""
}

// unknownPackage explains a --package term that matched nothing, and points at
// the flag that would have reached it when the term belongs to another.
func unknownPackage(term string, ws Workspace) error {
	msg := fmt.Sprintf("--package %q matches no package (discovered: %s)", term, join(packageNames(ws)))
	msg += hint(term, sortedKeys(ws.Spaces), "space", "--space")
	msg += hint(term, knownGroups(ws), "versioning group", "--group")
	return fmt.Errorf("%s", msg)
}

// unknownSpace explains a --space term that matched no configured space. A
// standalone package's name lands here, which is why the mirror hints matter:
// it belongs to no space, and only --package or the group it joined reach it.
func unknownSpace(term string, ws Workspace) error {
	spaces := sortedKeys(ws.Spaces)
	if len(spaces) == 0 {
		return fmt.Errorf("--space %q matches no configured space "+
			"(this repository configures none; every package is standalone — select it with --package)", term)
	}
	msg := fmt.Sprintf("--space %q matches no configured space (configured: %s)", term, join(spaces))
	msg += hint(term, packageNames(ws), "package", "--package")
	msg += hint(term, knownGroups(ws), "versioning group", "--group")
	return fmt.Errorf("%s", msg)
}

// unknownGroup explains a --group term that named no versioning group. The
// common mistake is naming a space that versions its packages independently:
// it has no group to speak of, and --space is what covers it.
func unknownGroup(term string, ws Workspace) error {
	groups := knownGroups(ws)
	if len(groups) == 0 {
		return fmt.Errorf("--group %q matches no versioning group "+
			"(this repository configures none; every package versions on its own — select it with --package or --space)", term)
	}
	msg := fmt.Sprintf("--group %q matches no versioning group (configured: %s)", term, join(groups))
	msg += hint(term, sortedKeys(ws.Spaces), "space", "--space")
	msg += hint(term, packageNames(ws), "package", "--package")
	return fmt.Errorf("%s", msg)
}

// hint is the pointer a failed term earns when another flag would have reached
// it: the whole reason a typo and a misfiled name read differently.
func hint(term string, candidates []string, noun, flag string) string {
	found := sortedMatches(term, candidates)
	if len(found) == 0 {
		return ""
	}
	return fmt.Sprintf("; %s is a %s — select it with %s", join(found), noun, flag)
}

// packageNames lists the workspace's packages, in resolution order.
func packageNames(ws Workspace) []string {
	out := make([]string, 0, len(ws.Packages))
	for _, pkg := range ws.Packages {
		out = append(out, pkg.Name)
	}
	return out
}

// nameLabel renders one or more names of one kind for a message: "space
// "libs"", "versioning groups "a", "b"".
func nameLabel(noun string, names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	if len(names) == 1 {
		return noun + " " + quoted[0]
	}
	return noun + "s " + join(quoted)
}

// describe names a selection for a message. A single literal package term is
// rendered as what it resolved to, so an inferred filter and a typed name read
// the same and carry the package's configured spelling; everything else is
// rendered as the terms, which is what the reader typed.
func describe(f Filter, names []string) string {
	if len(f.Spaces) == 0 && len(f.Groups) == 0 && len(f.Packages) == 1 && !isGlob(f.Packages[0]) {
		return join(names)
	}
	var parts []string
	switch {
	case len(f.Packages) == 1 && !isGlob(f.Packages[0]):
		parts = append(parts, "package "+f.Packages[0])
	case len(f.Packages) == 1:
		parts = append(parts, fmt.Sprintf("packages matching %q", f.Packages[0]))
	case len(f.Packages) > 1:
		parts = append(parts, "packages "+join(f.Packages))
	}
	if part := describeTerms("space", f.Spaces); part != "" {
		parts = append(parts, part)
	}
	if part := describeTerms("versioning group", f.Groups); part != "" {
		parts = append(parts, part)
	}
	return strings.Join(parts, " and ")
}

// describeTerms renders the terms of one flag: the names as typed, or the glob
// that stood for them, which is what the reader recognises from their own
// command line.
func describeTerms(noun string, terms []string) string {
	switch {
	case len(terms) == 0:
		return ""
	case len(terms) == 1 && isGlob(terms[0]):
		return fmt.Sprintf("%ss matching %q", noun, terms[0])
	default:
		return nameLabel(noun, terms)
	}
}

func isGlob(term string) bool { return strings.Contains(term, "*") }

// sortedSet orders a matched-name set for a message.
func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedMatches returns the candidates a term matches, in order.
func sortedMatches(term string, candidates []string) []string {
	var out []string
	for _, c := range candidates {
		if globx.Match(fold(term), fold(c)) {
			out = append(out, c)
		}
	}
	return out
}

// sortedKeys keeps map iteration out of every message and every match.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func join(names []string) string { return strings.Join(names, ", ") }

func fold(s string) string { return strings.ToLower(s) }

// absClean brings a path to the one shape comparisons happen in. Both sides go
// through it and neither through filepath.EvalSymlinks: resolving one side of
// a comparison and not the other is how a macOS /var vs /private/var pair
// stops matching.
func absClean(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// under reports whether dir is base or sits inside it. The separator matters:
// without it "core-extra" would count as inside "core".
func under(dir, base string) bool {
	return dir == base || strings.HasPrefix(dir, base+string(filepath.Separator))
}
