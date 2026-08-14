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

// listEdit accumulates everything one dependency list is about to have done
// to it. The three happen together — an edge removed, one corrected, one
// added — and the list is rewritten whole, so they are collected per source
// rather than applied one at a time. One type serves every kind of list: the
// root object, a space's object and a package's own providers differ in how
// they are written, not in what happens to them.
type listEdit struct {
	src    config.DepSource
	remove map[int]bool   // entry positions to drop
	kind   map[int]string // entry positions whose kind changes, and to what
	add    []config.DependencyConfig
}

// listEdits collects the edits by the list they are aimed at.
type listEdits struct {
	byKey map[string]*listEdit
}

func newListEdits() *listEdits { return &listEdits{byKey: map[string]*listEdit{}} }

// at returns the edit accumulating for src's list, creating it on first
// touch.
func (l *listEdits) at(src config.DepSource) *listEdit {
	k := listKey(src)
	if l.byKey[k] == nil {
		l.byKey[k] = &listEdit{src: src, remove: map[int]bool{}, kind: map[int]string{}}
	}
	return l.byKey[k]
}

// keys returns the touched lists in a fixed order, so one config always
// produces one sequence of edits.
func (l *listEdits) keys() []string {
	out := make([]string, 0, len(l.byKey))
	for k := range l.byKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	// Corrections and removals, in declaration order: the root object first,
	// then each package's lists.
	for _, d := range declared {
		p := pair{d.Consumer, d.Provider}
		kinds, found := detKinds[p]
		// An unparseable declared kind is reachable here (compute skips
		// Discover's dependency validation on purpose): treated as "kind
		// disagrees" when the pair is detected, and as an ordinary removal
		// candidate otherwise.
		declaredKind, kindErr := config.DepKind(d.Kind)
		// Every declaration carries keep, wherever it is written, so the way
		// to silence a removal is the same everywhere.
		const silence = "keep: true silences this"
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
// at the file that holds the declaration: an edge is removed and corrected
// exactly where it was written, whether that is the root config's
// `dependencies` object, a space's object, a packages entry, or a config file
// inside a space or package folder.
//
// An addition goes where its consumer already declares its own providers, and
// to the root object when it declares none. A config that keeps a package's
// dependencies next to the rest of that package's configuration stays that
// way, instead of growing a second home for edges the next compute finds.
//
// The in-memory config is aligned with what the edits say. The process exits
// right after, but a future long-lived caller must not see a config that
// disagrees with disk.
func (a *App) collectDepEdits(edits *fileEdits, cfgPath string, apply []suggestion, declared []config.DeclaredDependency) error {
	if len(apply) == 0 {
		return nil
	}
	// Where each consumer already declares its own providers, so an addition
	// can join it. The first package-level source wins: a consumer declaring
	// in two layers has them merged anyway, and the nearest is not more
	// correct than the first. An object keyed by consumer is not a candidate:
	// the root object is where an addition goes when nothing else claims it,
	// and a space's object may only hold edges that touch the space, which is
	// not something compute is in a position to decide.
	pkgListOf := make(map[string]config.DepSource)
	for _, d := range declared {
		if d.Source.IsObjectList() {
			continue
		}
		if _, seen := pkgListOf[d.Consumer]; !seen {
			pkgListOf[d.Consumer] = d.Source
		}
	}

	lists := newListEdits()
	rootSrc := config.DepSource{KeyPath: []string{"dependencies"}}
	for _, s := range apply {
		switch s.action {
		case actionAdd:
			if src, ok := pkgListOf[s.entry.Consumer]; ok {
				lists.at(src).add = append(lists.at(src).add, s.entry)
				continue
			}
			lists.at(rootSrc).add = append(lists.at(rootSrc).add, s.entry)
		case actionRemove:
			lists.at(s.src).remove[s.src.Index] = true
		case actionKind:
			// Every list carries a kind as readily as the root object does,
			// so a correction is applied where the edge already lives.
			lists.at(s.src).kind[s.src.Index] = s.entry.Kind
		}
	}

	// One edit per touched list, in deterministic order. The surviving entries
	// are reconstructed from the merged declaration list, which holds every
	// entry of every source in order.
	for _, k := range lists.keys() {
		edit := lists.byKey[k]
		src := edit.src
		var kept []config.DependencyConfig
		for _, d := range declared {
			if listKey(d.Source) != k || edit.remove[d.Source.Index] {
				continue
			}
			entry := d.DependencyConfig
			if kind, ok := edit.kind[d.Source.Index]; ok {
				entry.Kind = kind
			}
			kept = append(kept, entry)
		}
		kept = append(kept, edit.add...)

		target := src.File
		if target == "" {
			target = cfgPath
		}
		if err := edits.add(target, config.Edit{KeyPath: src.KeyPath, Value: listValue(src, kept)}); err != nil {
			return err
		}
		a.alignDeclaredList(src, kept)
	}
	return nil
}

// listKey identifies the one list a source names, so every edit aimed at it
// lands in the same bucket. A space's object and a package folder file's list
// share the key path ["dependencies"] in a file of their own, which is why
// the space is part of the identity.
func listKey(s config.DepSource) string {
	return s.Space + "\x00" + s.File + "\x00" + strings.Join(s.KeyPath, "\x00")
}

// listValue renders the surviving entries in the shape the list is written
// in: an object keyed by consumer for the root and space objects, a plain
// array of providers for a package's own list, whose key already says which
// package the entries belong to.
func listValue(src config.DepSource, kept []config.DependencyConfig) any {
	if src.IsObjectList() {
		return config.Dependencies(kept)
	}
	list := config.ProviderList{}
	for _, e := range kept {
		e.Consumer = ""
		list = append(list, e)
	}
	return list
}

// alignDeclaredList keeps the in-memory config in step with what an edit
// says. The process exits right after, but a future long-lived caller must
// not see a config that disagrees with disk. Only the root file's own lists
// are reachable from here; a list in a folder's config file was never loaded
// into this struct.
func (a *App) alignDeclaredList(src config.DepSource, kept []config.DependencyConfig) {
	if src.File != "" {
		return
	}
	switch {
	case src.IsRootList():
		a.cfg.Dependencies = config.Dependencies(kept)
	case src.Space != "":
		if s, ok := a.cfg.Spaces[src.Space]; ok {
			s.Dependencies = config.Dependencies(kept)
			a.cfg.Spaces[src.Space] = s
		}
	case len(src.KeyPath) == 3 && src.KeyPath[0] == "packages":
		if e, ok := a.cfg.Packages[src.KeyPath[1]]; ok {
			e.Dependencies = listValue(src, kept).(config.ProviderList)
			a.cfg.Packages[src.KeyPath[1]] = e
		}
	}
}
