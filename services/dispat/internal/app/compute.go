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

// suggestion is one proposed change to the config's dependencies list.
type suggestion struct {
	action string
	// entry is the edge in its proposed shape: the entry to append (add),
	// the entry to delete (remove), or the existing entry with its new kind
	// (kind).
	entry config.DependencyConfig
	// index locates the affected entry in cfg.Dependencies; -1 for add.
	index int
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
	pkgs, err := config.DiscoverPackages(a.cfg, a.root)
	if err != nil {
		a.log.Error().Err(err).Msg("package discovery failed")
		return 0, err
	}
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[p.Name] = true
	}
	detected, hasManifest := a.detectEdges(ctx, pkgs)
	sugs := a.diffEdges(detected, hasManifest, known)

	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if len(sugs) == 0 {
		fmt.Fprintf(out, "dependencies are in sync: %d detected edge(s), %d declared\n",
			len(detected), len(a.cfg.Dependencies))
		return 0, nil
	}
	apply, err := a.selectSuggestions(sugs, opts, out)
	if err != nil {
		return len(sugs), err
	}
	if len(apply) == 0 {
		return len(sugs), nil
	}
	if err := a.applySuggestions(cfgPath, apply, out); err != nil {
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
	byDir := make(map[string]string, len(pkgs)) // cleaned package dir -> name
	for _, p := range pkgs {
		byDir[filepath.Clean(p.Dir)] = p.Name
		mans, err := a.scan.Scan(ctx, p.Dir)
		if err != nil {
			a.log.Warn().Err(err).Str("package", p.Name).Msg("some manifests failed to parse")
		}
		if len(mans) > 0 {
			hasManifest[p.Name] = true
			all = append(all, parsed{p, mans})
		}
	}

	// The name map: scanner.NameIndex's rule, shared with the executor's
	// auto-versioning — root manifests bind before nested ones, and a
	// same-priority collision is ambiguous: W220, no edges by that name.
	owners := make([]scanner.Owner, 0, len(all))
	for _, pa := range all {
		owners = append(owners, scanner.Owner{Package: pa.pkg.Name, Manifests: pa.mans})
	}
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

// diffEdges compares the detected edges with the config's declared list and
// produces the suggestions: additions for detected-but-undeclared pairs, kind
// corrections for declared pairs whose field disagrees, removals for declared
// pairs no manifest supports — and for pairs naming a package that no longer
// exists at all, which Discover would refuse to load. A pair is (consumer,
// provider): the kind dimension corrects rather than duplicates, matching how
// the config is authored.
func (a *App) diffEdges(detected []detectedEdge, hasManifest, known map[string]bool) []suggestion {
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
	declared := make(map[pair]bool, len(a.cfg.Dependencies))
	for _, d := range a.cfg.Dependencies {
		declared[pair{d.Consumer, d.Provider}] = true
	}

	var sugs []suggestion
	// Additions, in deterministic order.
	var addPairs []pair
	for p := range detKinds {
		if !declared[p] {
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
			index:  -1,
			detail: detDetail[p][kind],
		})
	}
	// Corrections and removals, in the config's own order.
	for i, d := range a.cfg.Dependencies {
		p := pair{d.Consumer, d.Provider}
		kinds, found := detKinds[p]
		// An unparseable declared kind is reachable here (compute skips
		// Discover's dependency validation on purpose): treated as "kind
		// disagrees" when the pair is detected, and as an ordinary removal
		// candidate otherwise.
		declaredKind, kindErr := config.DepKind(d.Kind)
		switch {
		case !d.Keep && (!known[d.Consumer] || !known[d.Provider]):
			gone := d.Consumer
			if known[d.Consumer] {
				gone = d.Provider
			}
			sugs = append(sugs, suggestion{
				action: actionRemove,
				entry:  d,
				index:  i,
				detail: fmt.Sprintf("package %q no longer exists (keep: true silences this)", gone),
			})
		case found && (kindErr != nil || !containsKind(kinds, declaredKind)):
			want := strongestKind(kinds)
			entry := d
			entry.Kind = string(want)
			from := declaredKind.String()
			if kindErr != nil {
				from = fmt.Sprintf("invalid kind %q", d.Kind)
			}
			sugs = append(sugs, suggestion{
				action: actionKind,
				entry:  entry,
				index:  i,
				detail: fmt.Sprintf("%s -> %s: %s", from, want, detDetail[p][want]),
			})
		case !found && !d.Keep && hasManifest[d.Consumer]:
			sugs = append(sugs, suggestion{
				action: actionRemove,
				entry:  d,
				index:  i,
				detail: fmt.Sprintf("no manifest of %q declares %q (keep: true silences this)", d.Consumer, d.Provider),
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

// renderSuggestion is one suggestion's listing line.
func renderSuggestion(s suggestion) string {
	kind, _ := config.DepKind(s.entry.Kind)
	edge := fmt.Sprintf("%s -> %s (%s)", s.entry.Consumer, s.entry.Provider, kind)
	switch s.action {
	case actionAdd:
		return fmt.Sprintf("+ add    %s  %s", edge, s.detail)
	case actionRemove:
		return fmt.Sprintf("- remove %s  %s", edge, s.detail)
	default:
		return fmt.Sprintf("~ kind   %s  %s", edge, s.detail)
	}
}

// applySuggestions rewrites the config's dependencies list with the accepted
// changes. A TOML config cannot be edited format-preservingly, so it gets the
// rendered blocks to paste and an error, having written nothing.
func (a *App) applySuggestions(cfgPath string, apply []suggestion, out io.Writer) error {
	remove := make(map[int]bool)
	newKind := make(map[int]string)
	var adds []config.DependencyConfig
	for _, s := range apply {
		switch s.action {
		case actionAdd:
			adds = append(adds, s.entry)
		case actionRemove:
			remove[s.index] = true
		case actionKind:
			newKind[s.index] = s.entry.Kind
		}
	}
	// Declared entries keep their file order; accepted additions append.
	next := make([]config.DependencyConfig, 0, len(a.cfg.Dependencies)+len(adds))
	for i, d := range a.cfg.Dependencies {
		if remove[i] {
			continue
		}
		if k, ok := newKind[i]; ok {
			d.Kind = k
		}
		next = append(next, d)
	}
	next = append(next, adds...)

	err := config.ReplaceDependencies(cfgPath, next)
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
	// Keep the in-memory view aligned with the file just written; the process
	// exits right after, but a future long-lived caller must not see a config
	// that disagrees with disk.
	a.cfg.Dependencies = next
	fmt.Fprintf(out, "\napplied %d change(s) to %s (previous copy at %s)\n",
		len(apply), filepath.Base(cfgPath), filepath.Base(cfgPath)+config.BackupSuffix)
	return nil
}
