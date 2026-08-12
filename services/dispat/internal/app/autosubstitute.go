package app

// `dispat autosubstitute` is `dispat replacer` pointed at a selection instead
// of a list of files: the same literal find/write pairs, applied to every
// package the plan picks, over the files each package's globs select.
//
// What makes it automatic rather than a wider `dispat replacer` is the fan-out.
// A --sub carrying {provider}, {providerVersion} or {providerPrevious} is
// rendered once per workspace package the covered package depends on, so one
// pattern keeps every hand-written coordinate in a monorepo in step without
// naming a single dependency. It is autoVersion.replace driven from flags
// rather than from config, and it runs the same engine.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// AutoSubstituteOptions is one `dispat autosubstitute` invocation.
type AutoSubstituteOptions struct {
	// Window is which packages the command covers: the release window or
	// --since, narrowed by the filter, expanded by --consumers. A package that
	// only consumes an updated provider is outside the window by definition,
	// so --consumers is usually what reaches the files that need this.
	Window WindowOptions
	// OnError is the failure policy for a failed package's dependents.
	OnError string
	// Subs are the literal find/write pairs, applied to each file in order.
	// Either half may carry the placeholders.
	Subs []writer.Substitution
	// Files are the globs selecting what each covered package offers up,
	// relative to its own folder.
	Files []string
	// OnlyUpdated narrows the fan-out to the providers this run releases.
	OnlyUpdated bool
	// Strict turns a substitution that matched nothing in any covered package
	// into a failed command.
	Strict bool
	// JSON renders one event per package through the log instead of a listing.
	JSON bool
	// Out receives the listing.
	Out io.Writer
}

// AutoSubstitute applies the invocation's substitutions to every covered
// package's files. Every package is swept in dependency order, exactly as
// `dispat run` sweeps scripts.
//
// Nothing here depends on a package releasing: rewriting the files of a package
// with no pending change is a perfectly good thing to ask for, which is why
// --since all reaches the whole monorepo.
func (a *App) AutoSubstitute(ctx context.Context, opts AutoSubstituteOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot substitute")
		return err
	}

	work := a.newSubstituteWork(ctx, pl, opts)
	rep, drainErr := a.runSweep(ctx, pl, covered, work, sweepOptions{OnError: opts.OnError})

	files, occurrences := work.tally()
	if opts.JSON {
		a.log.Info().Int("packages", rep.Resolved).
			Int("files", files).Int("occurrences", occurrences).
			Msg("autosubstitute complete")
	} else {
		fmt.Fprintf(listing(opts.Out), "%d package(s): %d file(s), %d occurrence(s)\n",
			rep.Resolved, files, occurrences)
	}
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("autosubstitute interrupted")
		return drainErr
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d package(s) failed to substitute", rep.Failed)
	}
	// The strict gate asks whether a substitution found its text, so it only
	// has an answer once something was opened. A selection the window emptied
	// tried nothing, and calling every pattern stale for that would turn
	// "nothing changed today" into a failure.
	if opts.Strict && rep.Resolved > 0 {
		return work.stale()
	}
	return nil
}

// substituteWork is `dispat autosubstitute`'s share of a sweep: one package's
// providers resolved, then its files rewritten.
//
// The rules are the same for every package, so they are built once. What
// differs per package is the facts the placeholders render against and the
// providers the fan-out covers, which is what resolve computes. The counts and
// which substitutions ever matched are guarded by mu.
type substituteWork struct {
	app   *App
	pl    *plan.Plan
	rules []model.ReplaceRule
	names map[string]string // manifest name -> package name
	dirs  map[string]string // cleaned package folder -> package name

	onlyUpdated bool

	mu          sync.Mutex
	files       int
	occurrences int
	matched     map[int]bool // --sub index -> its text was found somewhere

	json bool
	out  io.Writer
}

// newSubstituteWork resolves the invocation into the work a sweep can run. The
// workspace index is always needed: the fan-out is nothing but the question of
// which declarations name a package here.
func (a *App) newSubstituteWork(ctx context.Context, pl *plan.Plan, opts AutoSubstituteOptions) *substituteWork {
	names, dirs := release.WorkspaceNames(ctx, a.scan, pl, a.log)

	// One rule per --sub, all sharing the --files globs: the config form pairs
	// each rule with its own globs, and the command line has one set to give.
	rules := make([]model.ReplaceRule, 0, len(opts.Subs))
	for _, s := range opts.Subs {
		rules = append(rules, model.ReplaceRule{Files: opts.Files, Find: s.Find, Write: s.Write})
	}
	return &substituteWork{
		app: a, pl: pl, rules: rules, names: names, dirs: dirs,
		onlyUpdated: opts.OnlyUpdated,
		matched:     map[int]bool{},
		json:        opts.JSON, out: opts.Out,
	}
}

func (w *substituteWork) stage() string { return "autosubstitute" }

// resolve prepares one package's substitution. Every covered package has files
// to offer, so unlike the writer's sweep this one never resolves to nothing:
// whether the globs select anything is the walk's answer, not this one's.
func (w *substituteWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	sub := release.Substituter{
		Dir:   rel.Pkg.Dir,
		Rules: w.rules,
		Facts: release.PackageFacts{
			Name:       rel.Pkg.Name,
			Version:    plannedVersion(rel),
			Previous:   rel.Previous().String(),
			Prerelease: rel.IsPrerelease(),
		},
		Providers: w.providers(rel),
		Owned:     w.ownedBy(rel),
		Log:       w.app.log.With().Str("package", rel.Pkg.Name).Str("stage", w.stage()).Logger(),
	}
	return func(ctx context.Context) error {
		rep, err := sub.Run(ctx)
		if err != nil {
			return err
		}
		w.record(rel.Pkg.Name, rep)
		return nil
	}, nil
}

// providers is the workspace packages this one declares, as the fan-out sees
// them.
//
// They come from the manifests rather than from the configured `dependencies`
// graph, which is the same index --link-local resolves against, so the two
// commands never disagree about which declarations are internal. Nothing is
// published on the strength of it, so an edge the config does not declare is
// not the warning here that it is during a release.
func (w *substituteWork) providers(rel *plan.Release) []release.ProviderFacts {
	mans, err := w.app.scan.ScanRoot(context.Background(), rel.Pkg.Dir)
	if err != nil {
		w.app.log.Debug().Err(err).Str("package", rel.Pkg.Name).
			Msg("some root manifests failed to parse")
	}
	seen := map[string]bool{}
	var out []release.ProviderFacts
	for _, m := range mans {
		for _, d := range m.Deps {
			provider := w.names[d.Name]
			if provider == "" && d.LocalPath != "" {
				provider = scanner.ResolveLocalDir(w.dirs, rel.Pkg.Dir, m.Path, d.LocalPath)
			}
			if provider == "" || provider == rel.Pkg.Name || seen[provider] {
				continue
			}
			pr := w.pl.Releases[provider]
			if pr == nil {
				continue
			}
			if w.onlyUpdated && !pr.Releasing() {
				w.app.log.Debug().Str("package", rel.Pkg.Name).Str("provider", provider).
					Msg("provider dropped from the fan-out: this run does not update it")
				continue
			}
			seen[provider] = true
			out = append(out, release.ProviderFacts{
				Name:       provider,
				Version:    plannedVersion(pr),
				Previous:   pr.Previous().String(),
				Releasing:  pr.Releasing(),
				Prerelease: pr.IsPrerelease(),
			})
		}
	}
	return out
}

// ownedBy is the nested-package guard: a package whose folder contains another
// package's folder must not rewrite that package's files, because its owner's
// own turn in the sweep will, and the two would otherwise write one file from
// two goroutines.
func (w *substituteWork) ownedBy(rel *plan.Release) func(string) bool {
	return func(relPath string) bool {
		folder := filepath.Dir(filepath.Join(rel.Pkg.Dir, filepath.FromSlash(relPath)))
		for {
			if owner, ok := w.dirs[filepath.Clean(folder)]; ok {
				return owner == rel.Pkg.Name
			}
			parent := filepath.Dir(folder)
			if parent == folder || len(parent) < len(filepath.Clean(rel.Pkg.Dir)) {
				return true
			}
			folder = parent
		}
	}
}

// record folds one package's outcome into the run-wide tally.
func (w *substituteWork) record(pkg string, rep release.SubstituteReport) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files += rep.Files
	w.occurrences += rep.Occurrences
	for rule := range rep.Matched {
		w.matched[rule] = true
	}
	if rep.Files == 0 {
		return
	}
	if w.json {
		w.app.log.Info().Str("package", pkg).Int("files", rep.Files).Msg("files substituted")
		return
	}
	fmt.Fprintf(listing(w.out), "%s\n  %d file(s) rewritten\n", pkg, rep.Files)
}

func (w *substituteWork) tally() (files, occurrences int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.files, w.occurrences
}

// stale is the --strict gate, asked across the whole sweep rather than per
// package: a substitution missing from one package's files is the ordinary case
// when one invocation covers twenty of them, while one no package anywhere
// carries is a pattern that has gone stale. It is the same rule `dispat
// replacer` applies to its substitutions.
//
// The question is asked per --sub rather than per rendered text, because a
// template is what the operator wrote: a fan-out pattern matching for any one
// provider has done its job, and a converged re-run is answered by the probe
// the engine pairs with every substitution.
func (w *substituteWork) stale() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var stale int
	for i, rule := range w.rules {
		if w.matched[i] {
			continue
		}
		w.app.log.Error().Str("find", rule.Find).Msg("substitution matched nothing")
		stale++
	}
	if stale == 0 {
		return nil
	}
	err := fmt.Errorf("%d substitution(s) matched nothing", stale)
	w.app.log.Error().Err(err).Msg("substitutions are not clean")
	return err
}
