package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// ComputeOptions selects what `dispat compute` does with its suggestions.
type ComputeOptions struct {
	// Write applies every suggestion to the config file.
	Write bool
	// Interactive asks y/N per suggestion on In and applies the accepted
	// ones; it wins over Write.
	Interactive bool
	// Check only reports: the CLI exits non-zero when suggestions remain,
	// which is the CI gate for a drifted dependencies list.
	Check bool
	// Filter scopes the suggestions to the selected packages' own edges —
	// named packages or spaces, or the folder the command was invoked from.
	// Detection still reads every package's manifests: the workspace name
	// index is what resolves a declared dependency onto a provider, so
	// narrowing the scan would turn a perfectly good edge into a removal
	// suggestion.
	Filter filter.Filter
	// In answers interactive prompts (the CLI passes stdin).
	In io.Reader
	// Out receives the suggestion listing (the CLI passes stdout).
	Out io.Writer
}

// The three things compute can suggest about an edge.
const (
	actionAdd    = "add"
	actionRemove = "remove"
	actionKind   = "kind"
)

// suggestion is one proposed change to a declared dependencies list.
type suggestion struct {
	action string
	// entry is the edge in its proposed shape: the entry to append (add),
	// the entry to delete (remove), or the existing entry with its new kind
	// (kind).
	entry config.DependencyConfig
	// src locates the affected declaration — the root config's list, a
	// packages entry's list, or a package folder's own config file. For add
	// it is the root list with index -1: additions always go there.
	src config.DepSource
	// detail is the human-readable evidence: manifest provenance for
	// add/kind, the absence explanation for remove.
	detail string
}

// Compute scans every package's manifests, derives the dependency graph and
// diffs it against the config's declared dependencies. Depending on opts the
// suggestions are printed, confirmed one by one, or applied to the config
// file (previous copy saved with config.BackupSuffix). It returns the number
// of suggestions left unapplied.
//
// The safety contract: compute never touches an edge silently. Removals are
// only suggested for consumers whose manifests were actually parsed, an edge
// marked `keep: true` is never suggested for removal, and nothing is written
// at all outside Write/Interactive.
func (a *App) Compute(ctx context.Context, cfgPath string, opts ComputeOptions) (int, error) {
	// Packages only, deliberately without Discover's dependency validation: a
	// stale edge naming a deleted package must reach diffEdges as a removal
	// suggestion, not abort the one command able to fix it.
	pkgs, declared, err := config.DiscoverPackages(a.cfg, a.root)
	if err != nil {
		a.log.Error().Err(err).Msg("package discovery failed")
		return 0, err
	}
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[p.Name] = true
	}
	sel, err := a.selectPackages(a.discoveredWorkspace(pkgs), opts.Filter)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot compute the dependency graph")
		return 0, err
	}
	// Every package is scanned whatever the filter says — see ComputeOptions
	// — and only the findings are scoped, by the consumer whose declaration
	// they are about.
	detected, hasManifest := a.detectEdges(ctx, pkgs)
	scoped, scopedManifest, scopedDeclared := detected, hasManifest, declared
	if sel.Active() {
		scoped = nil
		for _, e := range detected {
			if sel.Has(e.dep.Consumer) {
				scoped = append(scoped, e)
			}
		}
		scopedManifest = make(map[string]bool, len(hasManifest))
		for name := range hasManifest {
			if sel.Has(name) {
				scopedManifest[name] = true
			}
		}
		scopedDeclared = nil
		for _, d := range declared {
			if sel.Has(d.Consumer) {
				scopedDeclared = append(scopedDeclared, d)
			}
		}
	}
	sugs := a.diffEdges(scoped, scopedManifest, known, scopedDeclared)

	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if len(sugs) == 0 {
		scope := ""
		if sel.Active() {
			scope = " for " + sel.Description
		}
		fmt.Fprintf(out, "dependencies are in sync%s: %d detected edge(s), %d declared\n",
			scope, len(scoped), len(scopedDeclared))
		return 0, nil
	}
	apply, err := a.selectSuggestions(sugs, opts, out)
	if err != nil {
		return len(sugs), err
	}
	if len(apply) == 0 {
		return len(sugs), nil
	}
	if err := a.applySuggestions(cfgPath, apply, declared, out); err != nil {
		return len(sugs), err
	}
	return len(sugs) - len(apply), nil
}

// detectedEdge is one manifest-derived consumer -> provider relation with the
// declaration that produced it.
type detectedEdge struct {
	dep    model.Dependency
	detail string // "apps/web/package.json dependencies "@acme/core": "workspace:*""
}

// detectEdges scans every package folder and matches declared dependencies
// against the workspace: by manifest name first, by declared local path
// (file:, replace, path =) second. It returns the derived edges and the set
// of packages that had at least one parsed manifest.
func (a *App) detectEdges(ctx context.Context, pkgs []*model.Package) ([]detectedEdge, map[string]bool) {
	type parsed struct {
		pkg  *model.Package
		mans []scanner.Manifest
	}
	var all []parsed
	hasManifest := make(map[string]bool)
	byDir := make(map[string]string, len(pkgs))   // cleaned package dir -> name
	owners := make([]scanner.Owner, 0, len(pkgs)) // every package, named or not
	for _, p := range pkgs {
		byDir[filepath.Clean(p.Dir)] = p.Name
		mans, err := a.scan.Scan(ctx, p.Dir)
		if err != nil {
			a.log.Warn().Err(err).Str("package", p.Name).Msg("some manifests failed to parse")
		}
		// Every package is an owner, because one whose manifests declare no
		// name may still have been told what it is called. It only joins the
		// scan of declarations when it has manifests to declare with.
		owners = append(owners, scanner.Owner{Package: p.Name, Names: p.ManifestNames, Manifests: mans})
		if len(mans) > 0 {
			hasManifest[p.Name] = true
			all = append(all, parsed{p, mans})
		}
	}

	// The name map: scanner.NameIndex's rule, shared with the executor's
	// auto-versioning — a stated name binds before a declared one and a root
	// manifest before a nested one, and a same-rank collision is ambiguous:
	// W220, no edges by that name.
	byName, ambiguous := scanner.NameIndex(owners)
	for _, name := range ambiguous {
		a.log.Warn().Str("code", plan.CodeAmbiguousManifestName).
			Str("name", name).
			Msg("two packages declare the same manifest name; no edges derived from it")
	}

	var edges []detectedEdge
	for _, pa := range all {
		for _, m := range pa.mans {
			manifestRel := relPath(a.root, filepath.Join(pa.pkg.Dir, filepath.FromSlash(m.Path)))
			for _, d := range m.Deps {
				provider := byName[d.Name]
				if provider == "" && d.LocalPath != "" {
					provider = scanner.ResolveLocalDir(byDir, pa.pkg.Dir, m.Path, d.LocalPath)
				}
				if provider == "" || provider == pa.pkg.Name {
					continue
				}
				rng := d.Range
				if rng == "" {
					rng = "(no range)"
				}
				edges = append(edges, detectedEdge{
					dep: model.Dependency{
						Consumer: pa.pkg.Name,
						Provider: provider,
						Kind:     model.DepKind(d.Kind),
					},
					detail: fmt.Sprintf("%s %s %q: %q", manifestRel, d.Kind, d.Name, rng),
				})
			}
		}
	}
	return edges, hasManifest
}

// relPath is filepath.Rel with the path itself as the fallback, slashed for
// stable output.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// diffEdges compares the detected edges with the merged declared list and
// produces the suggestions: additions for detected-but-undeclared pairs, kind
// corrections for declared pairs whose field disagrees, removals for declared
// pairs no manifest supports — and for pairs naming a package that no longer
// exists at all, which Discover would refuse to load. A pair is (consumer,
// provider): the kind dimension corrects rather than duplicates, matching how
// the config is authored.
func (a *App) diffEdges(detected []detectedEdge, hasManifest, known map[string]bool, declared []config.DeclaredDependency) []suggestion {
	type pair struct{ consumer, provider string }
	detKinds := make(map[pair][]model.DepKind)
	detDetail := make(map[pair]map[model.DepKind]string)
	for _, e := range detected {
		p := pair{e.dep.Consumer, e.dep.Provider}
		if detDetail[p] == nil {
			detDetail[p] = make(map[model.DepKind]string)
		}
		if _, seen := detDetail[p][e.dep.Kind]; !seen {
			detKinds[p] = append(detKinds[p], e.dep.Kind)
			detDetail[p][e.dep.Kind] = e.detail
		}
	}
	declaredPairs := make(map[pair]bool, len(declared))
	for _, d := range declared {
		declaredPairs[pair{d.Consumer, d.Provider}] = true
	}

	var sugs []suggestion
	rootList := config.DepSource{KeyPath: []string{"dependencies"}, Index: -1}
	// Additions, in deterministic order.
	var addPairs []pair
	for p := range detKinds {
		if !declaredPairs[p] {
			addPairs = append(addPairs, p)
		}
	}
	sort.Slice(addPairs, func(i, j int) bool {
		if addPairs[i].consumer != addPairs[j].consumer {
			return addPairs[i].consumer < addPairs[j].consumer
		}
		return addPairs[i].provider < addPairs[j].provider
	})
	for _, p := range addPairs {
		kind := strongestKind(detKinds[p])
		sugs = append(sugs, suggestion{
			action: actionAdd,
			entry:  config.DependencyConfig{Consumer: p.consumer, Provider: p.provider, Kind: string(kind)},
			src:    rootList,
			detail: detDetail[p][kind],
		})
	}
	// Corrections and removals, in declaration order: the root list first,
	// then each package's lists. A package-declared edge carries no keep, so
	// silencing its removal means redeclaring it at the top level.
	for _, d := range declared {
		p := pair{d.Consumer, d.Provider}
		kinds, found := detKinds[p]
		// An unparseable declared kind is reachable here (compute skips
		// Discover's dependency validation on purpose): treated as "kind
		// disagrees" when the pair is detected, and as an ordinary removal
		// candidate otherwise.
		declaredKind, kindErr := config.DepKind(d.Kind)
		silence := "keep: true silences this"
		if !d.Source.IsRootList() {
			silence = "a top-level entry with keep: true silences this"
		}
		switch {
		case !d.Keep && (!known[d.Consumer] || !known[d.Provider]):
			gone := d.Consumer
			if known[d.Consumer] {
				gone = d.Provider
			}
			sugs = append(sugs, suggestion{
				action: actionRemove,
				entry:  d.DependencyConfig,
				src:    d.Source,
				detail: fmt.Sprintf("package %q no longer exists (%s)", gone, silence),
			})
		case found && (kindErr != nil || !containsKind(kinds, declaredKind)):
			want := strongestKind(kinds)
			entry := d.DependencyConfig
			entry.Kind = string(want)
			from := declaredKind.String()
			if kindErr != nil {
				from = fmt.Sprintf("invalid kind %q", d.Kind)
			}
			sugs = append(sugs, suggestion{
				action: actionKind,
				entry:  entry,
				src:    d.Source,
				detail: fmt.Sprintf("%s -> %s: %s", from, want, detDetail[p][want]),
			})
		case !found && !d.Keep && hasManifest[d.Consumer]:
			sugs = append(sugs, suggestion{
				action: actionRemove,
				entry:  d.DependencyConfig,
				src:    d.Source,
				detail: fmt.Sprintf("no manifest of %q declares %q (%s)", d.Consumer, d.Provider, silence),
			})
		}
	}
	return sugs
}

// strongestKind picks the kind an edge should carry when manifests declare
// several: the most propagation-relevant one, in the model's field order.
func strongestKind(kinds []model.DepKind) model.DepKind {
	rank := func(k model.DepKind) int {
		switch k {
		case model.KindDependencies:
			return 0
		case model.KindPeerDependencies:
			return 1
		case model.KindOptionalDependencies:
			return 2
		default: // devDependencies: the one propagation skips by default
			return 3
		}
	}
	best := kinds[0]
	for _, k := range kinds[1:] {
		if rank(k) < rank(best) {
			best = k
		}
	}
	return best
}

func containsKind(kinds []model.DepKind, k model.DepKind) bool {
	for _, have := range kinds {
		if have == k {
			return true
		}
	}
	return false
}

// selectSuggestions prints every suggestion and returns the ones to apply:
// none for a plain preview or --check, all for --write, the confirmed subset
// for --interactive.
func (a *App) selectSuggestions(sugs []suggestion, opts ComputeOptions, out io.Writer) ([]suggestion, error) {
	interactive := opts.Interactive && !opts.Check
	var (
		apply   []suggestion
		prompts *bufio.Scanner
	)
	if interactive {
		in := opts.In
		if in == nil {
			return nil, errors.New("interactive mode needs an input stream")
		}
		prompts = bufio.NewScanner(in)
	}
	for _, s := range sugs {
		fmt.Fprintln(out, renderSuggestion(s))
		if !interactive {
			continue
		}
		fmt.Fprint(out, "  apply? [y/N] ")
		if !prompts.Scan() {
			fmt.Fprintln(out)
			break // EOF: remaining suggestions stay unapplied
		}
		switch strings.ToLower(strings.TrimSpace(prompts.Text())) {
		case "y", "yes":
			apply = append(apply, s)
		}
	}
	if interactive {
		if err := prompts.Err(); err != nil {
			return nil, err
		}
		return apply, nil
	}
	if opts.Write && !opts.Check {
		return sugs, nil
	}
	if !opts.Check {
		fmt.Fprintf(out, "\n%d suggestion(s); apply all with --write, choose with --interactive\n", len(sugs))
	}
	return nil, nil
}

// renderSuggestion is one suggestion's listing line. Edges declared outside
// the root list carry their source, so the user knows which file an applied
// change touches.
func renderSuggestion(s suggestion) string {
	kind, _ := config.DepKind(s.entry.Kind)
	edge := fmt.Sprintf("%s -> %s (%s)", s.entry.Consumer, s.entry.Provider, kind)
	where := ""
	if !s.src.IsRootList() {
		where = fmt.Sprintf("  [%s]", s.src.Label())
	}
	switch s.action {
	case actionAdd:
		return fmt.Sprintf("+ add    %s  %s", edge, s.detail)
	case actionRemove:
		return fmt.Sprintf("- remove %s  %s%s", edge, s.detail, where)
	default:
		return fmt.Sprintf("~ kind   %s  %s%s", edge, s.detail, where)
	}
}

// applySuggestions rewrites the declared dependency lists with the accepted
// changes, each in the file that holds the declaration: the root config's
// list gets its removals, kind rewrites and every addition; a package-level
// list (a packages entry of the root config, or a package folder's own
// config file) gets its removals — a kind correction there moves the edge to
// the root list, because the provider-string form cannot carry a kind. A
// TOML file cannot be edited format-preservingly, so it gets a rendered
// block to paste and an error.
func (a *App) applySuggestions(cfgPath string, apply []suggestion, declared []config.DeclaredDependency, out io.Writer) error {
	sourceKey := func(s config.DepSource) string {
		return s.File + "\x00" + strings.Join(s.KeyPath, "\x00")
	}
	rootRemove := make(map[int]bool)
	rootKind := make(map[int]string)
	var adds []config.DependencyConfig
	pkgRemove := make(map[string]map[int]bool)
	pkgSource := make(map[string]config.DepSource)
	markPkg := func(s config.DepSource) {
		k := sourceKey(s)
		if pkgRemove[k] == nil {
			pkgRemove[k] = make(map[int]bool)
			pkgSource[k] = s
		}
		pkgRemove[k][s.Index] = true
	}
	for _, s := range apply {
		switch s.action {
		case actionAdd:
			adds = append(adds, s.entry)
		case actionRemove:
			if s.src.IsRootList() {
				rootRemove[s.src.Index] = true
			} else {
				markPkg(s.src)
			}
		case actionKind:
			if s.src.IsRootList() {
				rootKind[s.src.Index] = s.entry.Kind
			} else {
				markPkg(s.src)
				adds = append(adds, s.entry)
			}
		}
	}

	var edited []string
	if len(adds) > 0 || len(rootRemove) > 0 || len(rootKind) > 0 {
		// Declared entries keep their file order; accepted additions append.
		next := make([]config.DependencyConfig, 0, len(a.cfg.Dependencies)+len(adds))
		for i, d := range a.cfg.Dependencies {
			if rootRemove[i] {
				continue
			}
			if k, ok := rootKind[i]; ok {
				d.Kind = k
			}
			next = append(next, d)
		}
		next = append(next, adds...)

		err := config.ReplaceDependencies(cfgPath, []string{"dependencies"}, next)
		if errors.Is(err, config.ErrTOMLEdit) {
			snippet, renderErr := config.RenderDependenciesTOML(next)
			if renderErr != nil {
				return renderErr
			}
			fmt.Fprintf(out, "\n# paste over the [[dependencies]] blocks in %s:\n%s", filepath.Base(cfgPath), snippet)
			a.log.Error().Err(err).Msg("cannot edit a TOML config in place")
			return err
		}
		if err != nil {
			a.log.Error().Err(err).Msg("writing the config failed")
			return err
		}
		// Keep the in-memory view aligned with the file just written; the
		// process exits right after, but a future long-lived caller must not
		// see a config that disagrees with disk.
		a.cfg.Dependencies = next
		edited = append(edited, filepath.Base(cfgPath))
	}

	// Package-level lists, in deterministic order. The remaining provider
	// names are reconstructed from the merged declaration list, which holds
	// every entry of every source in order.
	sources := make([]string, 0, len(pkgRemove))
	for k := range pkgRemove {
		sources = append(sources, k)
	}
	sort.Strings(sources)
	for _, k := range sources {
		src := pkgSource[k]
		remaining := []string{}
		for _, d := range declared {
			if sourceKey(d.Source) != k || pkgRemove[k][d.Source.Index] {
				continue
			}
			remaining = append(remaining, d.Provider)
		}
		target := src.File
		display := filepath.Base(cfgPath)
		if target == "" {
			target = cfgPath
		} else {
			display = relPath(a.root, target)
		}
		err := config.ReplaceStringList(target, src.KeyPath, remaining)
		if errors.Is(err, config.ErrTOMLEdit) {
			snippet, renderErr := config.RenderStringListTOML(src.KeyPath, remaining)
			if renderErr != nil {
				return renderErr
			}
			fmt.Fprintf(out, "\n# paste over the dependencies in %s:\n%s", display, snippet)
			a.log.Error().Err(err).Msg("cannot edit a TOML config in place")
			return err
		}
		if err != nil {
			a.log.Error().Err(err).Msg("writing the config failed")
			return err
		}
		if src.File == "" && len(src.KeyPath) == 3 {
			if e, ok := a.cfg.Packages[src.KeyPath[1]]; ok {
				e.Dependencies = remaining
				a.cfg.Packages[src.KeyPath[1]] = e
			}
		}
		edited = append(edited, display)
	}

	fmt.Fprintf(out, "\napplied %d change(s) to %s (previous copies carry the %s suffix)\n",
		len(apply), strings.Join(edited, ", "), config.BackupSuffix)
	return nil
}
