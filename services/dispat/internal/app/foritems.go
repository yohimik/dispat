package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// What `dispat for` iterates over when the list is not spelled out on the
// command line. Every domain here is a question about the monorepo, and each
// one costs exactly what it asks: naming packages, spaces or groups is
// discovery, and asking what changed is the same plan every sweeping command
// computes.

// DirEnvVar is the item's folder, absolute, for the domains whose items have
// one. It is `for`'s alone — a release stage already runs inside its package's
// folder and has no use for the name — which is why it lives here rather than
// beside the release variables in internal/plan.
const DirEnvVar = "DISPAT_DIR"

// ForDomain is what a loop's items are.
type ForDomain string

// The domains, one per source flag. The empty value is the literal list, which
// needs nothing resolved and never reaches ForItems.
const (
	ForPackages  ForDomain = "packages"  // -p: the packages the terms name
	ForSpaces    ForDomain = "spaces"    // -s: the spaces themselves
	ForGroups    ForDomain = "groups"    // -g: the versioning groups themselves
	ForChanged   ForDomain = "changed"   // --changed / --since: the window
	ForUnchanged ForDomain = "unchanged" // --unchanged: its complement
)

// ForSelection is one loop's source: which kind of thing to iterate over, and
// the window that decides which of them for the two domains that have one.
type ForSelection struct {
	Domain ForDomain
	// Window is read by ForChanged and ForUnchanged alone. Its Filter narrows
	// the window for those two and carries the -p/-s/-g terms that *are* the
	// source for the other three.
	Window WindowOptions
}

// ForItems resolves a domain onto the items the loop runs over, in the order
// the loop runs them: discovery order for packages, name order for spaces and
// groups, dependency order for the two window domains, which is the order every
// sweep schedules in.
//
// A term matching nothing is an error rather than an empty loop, the same rule
// the filter keeps everywhere: a loop that quietly ran zero times is how a typo
// hides. An empty *window* is not one — nothing changed is an answer — and
// --require-items is how a caller says otherwise.
func (a *App) ForItems(ctx context.Context, sel ForSelection) ([]ForItem, error) {
	switch sel.Domain {
	case ForPackages:
		pkgs, err := a.packages()
		if err != nil {
			return nil, err
		}
		res, err := filter.Resolve(sel.Window.Filter, a.discoveredWorkspace(pkgs))
		if err != nil {
			return nil, err
		}
		items := make([]ForItem, 0, len(res.Names))
		for _, p := range pkgs {
			if res.Has(p.Name) {
				items = append(items, packageItem(p))
			}
		}
		return items, nil
	case ForSpaces:
		return a.spaceItems(sel.Window.Filter.Spaces)
	case ForGroups:
		return a.groupItems(sel.Window.Filter.Groups)
	case ForChanged:
		names, err := a.ChangedSelection(ctx, sel.Window)
		if err != nil {
			return nil, err
		}
		return a.packageItems(names)
	case ForUnchanged:
		names, err := a.UnchangedSelection(ctx, sel.Window)
		if err != nil {
			return nil, err
		}
		return a.packageItems(names)
	}
	return nil, fmt.Errorf("unknown loop domain %q", sel.Domain)
}

// spaceItems resolves the --space terms onto the spaces themselves. The primary
// folder is the same one `--in space:<name>` places a script in, so a loop and
// a script agree about where a space is.
func (a *App) spaceItems(terms []string) ([]ForItem, error) {
	pkgs, err := a.packages()
	if err != nil {
		return nil, err
	}
	names, err := filter.MatchSpaces(a.discoveredWorkspace(pkgs), terms)
	if err != nil {
		return nil, err
	}
	items := make([]ForItem, 0, len(names))
	for _, name := range names {
		sc, ok := a.cfg.Space(name)
		if !ok {
			// MatchSpaces resolved the name against the configured spaces, so
			// this is unreachable; refusing beats iterating over a folder nobody
			// could name.
			return nil, fmt.Errorf("unknown space %q", name)
		}
		items = append(items, ForItem{Value: name, Env: []string{
			"DISPAT_SPACE=" + name,
			DirEnvVar + "=" + absDir(filepath.Join(a.root, filepath.FromSlash(sc.Path.First()))),
		}})
	}
	return items, nil
}

// groupItems resolves the --group terms onto the groups themselves. A group is
// a versioning relationship rather than a folder, so its item carries its name
// and nothing else.
func (a *App) groupItems(terms []string) ([]ForItem, error) {
	pkgs, err := a.packages()
	if err != nil {
		return nil, err
	}
	names, err := filter.MatchGroups(a.discoveredWorkspace(pkgs), terms)
	if err != nil {
		return nil, err
	}
	items := make([]ForItem, 0, len(names))
	for _, name := range names {
		items = append(items, ForItem{Value: name, Env: []string{plan.GroupEnvVar + "=" + name}})
	}
	return items, nil
}

// packageItems turns the names a window selected into items, looking each up in
// the discovered workspace for the folder and the group the plan's names alone
// do not carry.
func (a *App) packageItems(names []string) ([]ForItem, error) {
	pkgs, err := a.packages()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*model.Package, len(pkgs))
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	items := make([]ForItem, 0, len(names))
	for _, name := range names {
		p, ok := byName[name]
		if !ok {
			// The plan and discovery read the same configuration, so a planned
			// package with no discovered twin cannot happen; iterating over an
			// item with no folder silently would be the worse answer.
			return nil, fmt.Errorf("package %q is planned but was not discovered", name)
		}
		items = append(items, packageItem(p))
	}
	return items, nil
}

// packageItem describes one package to the scripts of an iteration. The names
// are the release environment's own — a script reading DISPAT_PACKAGE means the
// same thing inside a stage and inside a loop — and the group is left unset
// rather than empty for an independently versioned package, the same
// unset-not-empty convention DISPAT_COUNTER keeps.
func packageItem(p *model.Package) ForItem {
	space := ""
	if p.Space != nil {
		space = p.Space.Name
	}
	env := []string{
		plan.PackageEnvVar + "=" + p.Name,
		"DISPAT_SPACE=" + space,
		DirEnvVar + "=" + absDir(p.Dir),
	}
	if group := p.VersionGroupName(); group != "" {
		env = append(env, plan.GroupEnvVar+"="+group)
	}
	return ForItem{Value: p.Name, Env: env}
}

// absDir is the folder as a script has to read it. Discovery joins package and
// space folders onto --root as the user spelled it, so a default --root of "."
// leaves them relative — and an iteration does not run inside its item's
// folder, so a relative DISPAT_DIR would resolve against whatever folder the
// script happens to cd into. A path that cannot be made absolute is handed over
// as it stands, which is still the best answer available.
func absDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}
