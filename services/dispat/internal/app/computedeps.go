package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The dependency half of compute: manifest declarations in, `dependencies`
// edges out.

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

// render is one suggestion's listing line. Edges declared outside the root
// list carry their source, so the user knows which file an applied change
// touches.
func (s suggestion) render() string {
	kind, _ := config.DepKind(s.entry.Kind)
	edge := fmt.Sprintf("%s -> %s (%s)", s.entry.Consumer, s.entry.Provider, kind)
	where := ""
	if !s.src.IsRootList() {
		where = fmt.Sprintf("  [%s]", s.src.Label())
	}
	switch s.action {
	case actionAdd:
		return fmt.Sprintf("+ add     %s  %s", edge, s.detail)
	case actionRemove:
		return fmt.Sprintf("- remove  %s  %s%s", edge, s.detail, where)
	default:
		return fmt.Sprintf("~ kind    %s  %s%s", edge, s.detail, where)
	}
}

// detectedEdge is one manifest-derived consumer -> provider relation with the
// declaration that produced it.
type detectedEdge struct {
	dep    model.Dependency
	detail string // "apps/web/package.json dependencies "@acme/core": "workspace:*""
}

// detectEdges matches every scanned manifest's declared dependencies against
// the workspace: by manifest name first, by declared local path (file:,
// replace, path =) second. It returns the derived edges and the set of
// packages that had at least one parsed manifest.
func (a *App) detectEdges(scanned []scannedPackage) ([]detectedEdge, map[string]bool) {
	hasManifest := make(map[string]bool, len(scanned))
	byDir := make(map[string]string, len(scanned))   // cleaned package dir -> name
	owners := make([]scanner.Owner, 0, len(scanned)) // every package, named or not
	for _, s := range scanned {
		byDir[filepath.Clean(s.pkg.Dir)] = s.pkg.Name
		// Every package is an owner, because one whose manifests declare no
		// name may still have been told what it is called. It only joins the
		// scan of declarations when it has manifests to declare with.
		owners = append(owners, scanner.Owner{Package: s.pkg.Name, Names: s.pkg.ManifestNames, Manifests: s.mans})
		if len(s.mans) > 0 {
			hasManifest[s.pkg.Name] = true
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
	for _, s := range scanned {
		for _, m := range s.mans {
			manifestRel := relPath(a.root, filepath.Join(s.pkg.Dir, filepath.FromSlash(m.Path)))
			for _, d := range m.Deps {
				provider := byName[d.Name]
				if provider == "" && d.LocalPath != "" {
					provider = scanner.ResolveLocalDir(byDir, s.pkg.Dir, m.Path, d.LocalPath)
				}
				if provider == "" || provider == s.pkg.Name {
					continue
				}
				rng := d.Range
				if rng == "" {
					rng = "(no range)"
				}
				edges = append(edges, detectedEdge{
					dep: model.Dependency{
						Consumer: s.pkg.Name,
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

// collectDepEdits turns the accepted edge suggestions into edits, each aimed
// at the file that holds the declaration: the root config's list gets its
// removals, kind rewrites and every addition; a package-level list (a
// packages entry of the root config, or a package folder's own config file)
// gets its removals — a kind correction there moves the edge to the root
// list, because the provider-string form cannot carry a kind.
//
// The in-memory config is aligned with what the edits say. The process exits
// right after, but a future long-lived caller must not see a config that
// disagrees with disk.
func (a *App) collectDepEdits(edits *fileEdits, cfgPath string, apply []suggestion, declared []config.DeclaredDependency) {
	if len(apply) == 0 {
		return
	}
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
		edits.add(cfgPath, config.Edit{KeyPath: []string{"dependencies"}, Value: next})
		a.cfg.Dependencies = next
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
		if target == "" {
			target = cfgPath
		}
		edits.add(target, config.Edit{KeyPath: src.KeyPath, Value: remaining})
		if src.File == "" && len(src.KeyPath) == 3 {
			if e, ok := a.cfg.Packages[src.KeyPath[1]]; ok {
				e.Dependencies = remaining
				a.cfg.Packages[src.KeyPath[1]] = e
			}
		}
	}
}
