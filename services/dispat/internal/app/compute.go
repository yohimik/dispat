package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
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
	// Filter scopes the suggestions to the selected packages' own edges and
	// baselines — named packages or spaces, or the folder the command was
	// invoked from. Detection still reads every package's manifests: the
	// workspace name index is what resolves a declared dependency onto a
	// provider, so narrowing the scan would turn a perfectly good edge into a
	// removal suggestion.
	Filter filter.Filter
	// In answers interactive prompts (the CLI passes stdin).
	In io.Reader
	// Out receives the suggestion listing (the CLI passes stdout).
	Out io.Writer
}

// change is one proposed edit to the configuration. The interface is what the
// listing and the interactive prompt need; applying stays typed, because the
// two kinds compute derives — a dependency edge and a version baseline — land
// in different keys of the file.
type change interface {
	// render is the change's line in the listing.
	render() string
}

// changeSet is one run's proposals, kept apart by kind: the dependency
// suggestions in declaration order, then the baselines by package name.
type changeSet struct {
	deps     []suggestion
	initials []initialSuggestion
}

// len is the number of proposed changes.
func (c changeSet) len() int { return len(c.deps) + len(c.initials) }

// all is every change in listing order.
func (c changeSet) all() []change {
	out := make([]change, 0, c.len())
	for _, s := range c.deps {
		out = append(out, s)
	}
	for _, s := range c.initials {
		out = append(out, s)
	}
	return out
}

// add files one accepted change back under its own kind. The type switch is
// total: change is implemented in this package and nowhere else.
func (c *changeSet) add(ch change) {
	switch v := ch.(type) {
	case suggestion:
		c.deps = append(c.deps, v)
	case initialSuggestion:
		c.initials = append(c.initials, v)
	}
}

// Compute scans every package's manifests and turns what they declare into
// configuration: the dependency graph, diffed against the declared edges, and
// a starting baseline for every package whose version only its manifests
// know. Depending on opts the suggestions are printed, confirmed one by one,
// or applied to the config file (previous copy saved with
// config.BackupSuffix). It returns the number of suggestions left unapplied.
//
// The safety contract: compute never overrides what someone already decided.
// Removals are only suggested for consumers whose manifests were actually
// parsed, an edge marked `keep: true` is never suggested for removal, an
// existing initials entry is never rewritten, and nothing is written at all
// outside Write/Interactive.
func (a *App) Compute(ctx context.Context, cfgPath string, opts ComputeOptions) (int, error) {
	// Packages only, deliberately without Discover's dependency validation: a
	// stale edge naming a deleted package must reach diffEdges as a removal
	// suggestion, not abort the one command able to fix it.
	pkgs, declared, _, err := config.DiscoverPackages(a.cfg, a.root)
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
	// — and only the findings are scoped, by the package whose declaration
	// they are about.
	scanned := a.scanPackages(ctx, pkgs)
	detected, hasManifest := a.detectEdges(scanned)
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
	sugs := changeSet{deps: a.diffEdges(scoped, scopedManifest, known, scopedDeclared)}
	initials, baselines := a.suggestInitials(ctx, scanned, sel)
	sugs.initials = initials

	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if sugs.len() == 0 {
		scope := ""
		if sel.Active() {
			scope = " for " + sel.Description
		}
		subject := "dependencies"
		if baselines {
			subject = "dependencies and baselines"
		}
		fmt.Fprintf(out, "%s are in sync%s: %d detected edge(s), %d declared\n",
			subject, scope, len(scoped), len(scopedDeclared))
		return 0, nil
	}
	apply, err := a.selectSuggestions(sugs, opts, out)
	if err != nil {
		return sugs.len(), err
	}
	if apply.len() == 0 {
		return sugs.len(), nil
	}
	if err := a.applySuggestions(cfgPath, apply, declared, out); err != nil {
		return sugs.len(), err
	}
	return sugs.len() - apply.len(), nil
}

// scannedPackage is one package beside the manifests one walk of its folder
// found.
type scannedPackage struct {
	pkg  *model.Package
	mans []scanner.Manifest
}

// scanPackages reads every package's manifests once. Both halves of the
// command are derived from this single pass, so a package folder is walked
// once however much the run computes. A folder whose manifests partly failed
// to parse is reported and keeps its readable ones, which is the scanner's
// own contract.
func (a *App) scanPackages(ctx context.Context, pkgs []*model.Package) []scannedPackage {
	scanned := make([]scannedPackage, 0, len(pkgs))
	for _, p := range pkgs {
		mans, err := a.scan.Scan(ctx, p.Dir)
		if err != nil {
			a.log.Warn().Err(err).Str("package", p.Name).Msg("some manifests failed to parse")
		}
		scanned = append(scanned, scannedPackage{pkg: p, mans: mans})
	}
	return scanned
}

// relPath is filepath.Rel with the path itself as the fallback, slashed for
// stable output.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// selectSuggestions prints every suggestion and returns the ones to apply:
// none for a plain preview or --check, all for --write, the confirmed subset
// for --interactive.
func (a *App) selectSuggestions(sugs changeSet, opts ComputeOptions, out io.Writer) (changeSet, error) {
	interactive := opts.Interactive && !opts.Check
	var (
		apply   changeSet
		prompts *bufio.Scanner
	)
	if interactive {
		in := opts.In
		if in == nil {
			return changeSet{}, errors.New("interactive mode needs an input stream")
		}
		prompts = bufio.NewScanner(in)
	}
	for _, s := range sugs.all() {
		fmt.Fprintln(out, s.render())
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
			apply.add(s)
		}
	}
	if interactive {
		if err := prompts.Err(); err != nil {
			return changeSet{}, err
		}
		return apply, nil
	}
	if opts.Write && !opts.Check {
		return sugs, nil
	}
	if !opts.Check {
		fmt.Fprintf(out, "\n%d suggestion(s); apply all with --write, choose with --interactive\n", sugs.len())
	}
	return changeSet{}, nil
}

// fileEdits collects the accepted changes by the file they land in, so each
// file is rewritten exactly once. Two writes to one file would each save a
// backup, and the second would save the first write's output: the pre-edit
// copy the user reaches for would be gone. One run touches the root config
// twice whenever it accepts changes to two of its keys — a top-level edge and
// a packages entry's list, or an edge and a baseline.
type fileEdits struct {
	order  []string // target paths, first touched first
	byFile map[string][]config.Edit
}

// add records one edit, at the file that actually holds the key: a `$ref`
// crossed on the way down moves it into the file that reference names. The
// retargeting happens here rather than at the writer, so that two edits landing
// in one referenced file are still one read, one backup and one write.
func (f *fileEdits) add(path string, e config.Edit) error {
	target, inner, err := config.ResolveEdit(path, e.KeyPath)
	if err != nil {
		return err
	}
	e.KeyPath = inner
	if f.byFile == nil {
		f.byFile = make(map[string][]config.Edit)
	}
	if _, seen := f.byFile[target]; !seen {
		f.order = append(f.order, target)
	}
	f.byFile[target] = append(f.byFile[target], e)
	return nil
}

// applySuggestions writes the accepted changes, one pass per affected file. A
// TOML file cannot be edited format-preservingly, so it gets a rendered block
// to paste and an error.
func (a *App) applySuggestions(cfgPath string, apply changeSet, declared []config.DeclaredDependency, out io.Writer) error {
	var edits fileEdits
	for _, collect := range []func() error{
		func() error { return a.collectDepEdits(&edits, cfgPath, apply.deps, declared) },
		func() error { return a.collectInitialEdits(&edits, cfgPath, apply.initials) },
	} {
		err := collect()
		if errors.Is(err, config.ErrRefEdit) || errors.Is(err, config.ErrMultiRefEdit) {
			a.log.Error().Err(err).Msg("cannot rewrite a key a $ref composes")
			return err
		}
		if err != nil {
			a.log.Error().Err(err).Msg("reading the config failed")
			return err
		}
	}

	// Every file is rendered and validated before any is rewritten: a refusal
	// discovered at the third file of three must not leave the first two
	// already changed, because a config half-matching the suggestions matches
	// neither the state the user had nor the one they asked for.
	prepared := make([]*config.PreparedEdit, 0, len(edits.order))
	displays := make([]string, 0, len(edits.order))
	for _, target := range edits.order {
		display := filepath.Base(cfgPath)
		if target != cfgPath {
			display = relPath(a.root, target)
		}
		p, err := config.PrepareKeys(target, edits.byFile[target])
		if errors.Is(err, config.ErrTOMLEdit) {
			for _, e := range edits.byFile[target] {
				if len(e.KeyPath) == 0 {
					// The file is a fragment a `$ref` names, so it holds the
					// value and nothing else: there is no key to paste over,
					// and TOML cannot be rewritten around one.
					fmt.Fprintf(out, "\n# %s is TOML and holds this value alone; write it there by hand\n", display)
					continue
				}
				what, snippet, renderErr := tomlFallback(e)
				if renderErr != nil {
					return renderErr
				}
				fmt.Fprintf(out, "\n# paste over the %s in %s:\n%s", what, display, snippet)
			}
			a.log.Error().Err(err).Msg("cannot edit a TOML config in place")
			return err
		}
		if err != nil {
			a.log.Error().Err(err).Msg("rendering the config edit failed")
			return err
		}
		prepared = append(prepared, p)
		displays = append(displays, display)
	}

	edited := make([]string, 0, len(prepared))
	for i, p := range prepared {
		if err := p.Commit(); err != nil {
			// Rendering was validated above, so this is the disk refusing the
			// write; the files already rewritten are named so the user knows
			// which ones changed and where their pre-edit copies sit.
			a.log.Error().Err(err).Str("file", displays[i]).
				Strs("alreadyRewritten", edited).Str("backupSuffix", config.BackupSuffix).
				Msg("writing the config failed")
			return err
		}
		edited = append(edited, displays[i])
	}

	fmt.Fprintf(out, "\napplied %d change(s) to %s (previous copies carry the %s suffix)\n",
		apply.len(), strings.Join(edited, ", "), config.BackupSuffix)
	return nil
}

// tomlFallback is what a refused TOML edit prints: the block to paste and the
// name of what it replaces. The root dependency object has a renderer of its
// own, because its entries are objects whose empty fields the config reads as
// absent and a generic marshal would spell out.
func tomlFallback(e config.Edit) (what, block string, err error) {
	if deps, ok := e.Value.(config.Dependencies); ok {
		block, err = config.RenderDependenciesTOML(deps)
		return "[dependencies] table", block, err
	}
	block, err = config.RenderKeyTOML(e.KeyPath, e.Value)
	return e.KeyPath[len(e.KeyPath)-1], block, err
}
