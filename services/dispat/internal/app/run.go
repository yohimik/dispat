package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// Run script error policies: what a failing package does to its dependents
// (the --on-error flag every sweeping command takes).
const (
	// OnErrorSkip (default) skips the changed dependents of a failed package
	// — transitively, exactly like a release skips consumers of a failed
	// provider. Independent packages always keep running.
	OnErrorSkip = "skip"
	// OnErrorContinue still runs the dependents; the command's exit code
	// reports the failure either way.
	OnErrorContinue = "continue"
)

// ValidOnError reports whether the value is a known error policy.
func ValidOnError(v string) bool { return v == OnErrorSkip || v == OnErrorContinue }

// SinceAll is the reserved --since value selecting every package, changed or
// not — "do this everywhere".
const SinceAll = "all"

// RunOptions selects what RunScript runs over, beyond the script name.
type RunOptions struct {
	// OnError is the failure policy for the failed package's dependents:
	// OnErrorSkip (the default when empty) or OnErrorContinue.
	OnError string
	// Window is which packages the run covers: the release window or --since,
	// narrowed by the filter, expanded by --consumers.
	Window WindowOptions
	// Args are the arguments typed after `--`, appended to whatever command
	// each covered package resolves the name to. Every covered package gets
	// them, which is the point: `dispat run test -- --watch` is one intent
	// about the whole selection, not about whichever package happens to run
	// first.
	Args []string
}

// RunScript computes the plan and executes the named script inside each
// changed package that resolves it, with the package's full DISPAT_*
// environment, honouring the dependency graph: a package's script starts only
// after every changed provider's has finished, and independent packages run
// concurrently within the build concurrency budget.
//
// A package resolves the name through its own scripts, then its space's, then
// the top level's, so where a name is defined is what a run covers: a
// top-level script reaches every changed package, a space's reaches that
// space's, and a package's reaches that package alone. A selected package
// that resolves nothing completes as a no-op — but a name nothing defines, or
// a selection in which no package resolves it, is an error, because running
// nothing silently is how a typo hides. opts decides which packages the run
// covers (see RunOptions); any failure makes the whole command fail.
func (a *App) RunScript(ctx context.Context, name string, opts RunOptions) error {
	if !a.scriptDefinedAnywhere(name) {
		msg := fmt.Sprintf("no script %q is defined at the top level, in a space or in a package", name)
		if note, ok := lookupNote(name); ok {
			msg = note
		}
		err := errors.New(msg)
		a.log.Error().Err(err).Msg("unknown run script")
		return err
	}

	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return errors.New("no correct plan exists")
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return err
	}

	work := &scriptWork{
		app: a, pl: pl, name: name, args: opts.Args,
		// The workspace listing depends only on the plan, so it is built once
		// here and shared by every package's environment.
		wsVars:  release.WorkspaceEnv(pl, a.log),
		runner:  &script.ShellRunner{Shell: a.cfg.Shell},
		covered: coveredReleases(pl, covered),
	}
	rep, drainErr := a.runSweep(ctx, pl, covered, work, sweepOptions{OnError: opts.OnError})

	a.log.Info().Str("script", name).Int("ran", rep.Ran).Int("failed", rep.Failed).
		Int("skipped", rep.Skipped).Msg("run finished")
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("run interrupted")
		return drainErr
	}
	// The script exists somewhere — the guard above said so — but not where
	// this run looked. Silence here would read like a clean run of nothing,
	// so the mismatch between the name's level and the selection is an error.
	// A selection that is empty to begin with is not: nothing changed, and a
	// run over no packages is an honest no-op.
	if len(covered) > 0 && rep.Resolved == 0 {
		err := fmt.Errorf("no selected package defines script %q (selected: %s)",
			name, strings.Join(covered, ", "))
		a.log.Error().Err(err).Msg("nothing to run")
		return err
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d run script(s) failed", rep.Failed)
	}
	return nil
}

// scriptDefinedAnywhere reports whether any level of the configuration binds
// the name to a command. It is the typo guard, and it runs before the plan so
// a misspelling costs nothing: a name that exists somewhere may still turn out
// to reach none of the packages a run selects, which the run itself reports.
// The three cheap levels are asked first; only a name none of them knows is
// worth a package discovery, which is the one place a script defined solely in
// a package folder's own config file can be seen.
func (a *App) scriptDefinedAnywhere(name string) bool {
	if _, ok := a.cfg.Script(name); ok {
		return true
	}
	for _, sc := range a.cfg.Spaces {
		if _, ok := sc.Script(name); ok {
			return true
		}
	}
	key := strings.ToLower(name)
	for _, po := range a.cfg.Packages {
		if _, ok := po.Scripts[key]; ok {
			return true
		}
	}
	pkgs, _, err := config.DiscoverPackages(a.cfg, a.root)
	if err != nil {
		return false
	}
	for _, p := range pkgs {
		if _, ok := p.Space.Scripts[key]; ok {
			return true
		}
	}
	return false
}

// scriptWork is `dispat run`'s share of a sweep: one shell command per
// package, in the package's folder, with the release environment the pipeline's
// own stages see.
type scriptWork struct {
	app     *App
	pl      *plan.Plan
	name    string
	args    []string // what followed `--`, appended to every package's command
	wsVars  []string // the shared workspace listing, built once per run
	runner  script.Runner
	covered map[string]*plan.Release
}

func (w *scriptWork) stage() string { return "run:" + w.name }

// resolve carries the changed providers' exports in and looks the script up.
// A package that resolves nothing is a no-op, said out loud at debug level:
// the run as a whole decides whether a selection that resolved nothing at all
// is a mistake.
func (w *scriptWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	pkg := rel.Pkg.Name
	// Before anything of this package runs: the run-command counterpart of
	// the pipeline's per-package accumulation, transitive by construction
	// (each provider carried its own providers' exports before exporting).
	// Providers merge in name order, so a name two of them export resolves
	// deterministically; the package's own later export overrides either.
	provs := append([]string(nil), w.pl.Providers[pkg]...)
	sort.Strings(provs)
	for _, prov := range provs {
		// Only a provider the run actually covered has outputs worth carrying.
		if pr, ok := w.covered[prov]; ok {
			release.MergeOutputs(rel, pr.Outputs)
		}
	}

	// The one resolution: the package's own scripts over its space's over the
	// top level's, already merged into the effective map when the package's
	// space was built.
	cmds, ok := rel.Pkg.Space.Scripts[strings.ToLower(w.name)]
	if !ok {
		w.app.log.Debug().Str("package", pkg).Str("space", rel.Pkg.Space.Name).
			Msgf("package does not define script %q, skipping", w.name)
		return nil, nil
	}
	return func(ctx context.Context) error {
		log := w.app.log.With().Str("package", pkg).Str("stage", w.stage()).Logger()
		log.Info().Msg("run script started")
		seq := release.Sequence{Runner: w.runner, Dir: rel.Pkg.Dir, Stage: w.stage(),
			Commands: script.AppendArgsToLast(cmds, w.args),
			Env:      release.CommandEnv(w.pl, pkg, w.stage(), w.wsVars),
			Log:      log, FailFast: true}
		return seq.RunMergingOutputs(ctx, rel)
	}, nil
}
