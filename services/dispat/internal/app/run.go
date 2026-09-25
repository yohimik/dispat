package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
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

// IsValidOnError reports whether the value is a known error policy.
func IsValidOnError(v string) bool { return v == OnErrorSkip || v == OnErrorContinue }

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
// space's, and a package's reaches that package alone. A covered package that
// resolves nothing completes as a no-op. A name nothing defines is an error,
// and so is an explicit selection — a --package, --space or --group term, or
// the package or space folder the command was invoked from — in which no
// package resolves it, because running nothing against what the user named is
// how a typo hides. A selection the window assembled on its own that resolves
// nothing completes as a reported no-op instead: the window claims only that
// packages changed, not that the script reaches them. opts decides which
// packages the run covers (see RunOptions); any failure makes the whole
// command fail.
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

	// Who may start a sweep that reaches other machines, and whether it could
	// be coordinated at all: the refusals a release is held to, asked before
	// anything is planned. A sweep with no worker links asks nothing and is
	// named nothing, which keeps it the run it always was.
	if a.cfg.Execution.IsDistributed() {
		if err := a.checkExecutionEntry(ctx, runSweep); err != nil {
			return err
		}
		a.startExecutionRun()
	}
	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if pl.IsFatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return errors.New("no correct plan exists")
	}
	sel, covered, err := a.coveredSelection(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return err
	}

	work := &scriptWork{
		app: a, pl: pl, name: name, args: opts.Args,
		// The workspace listing depends only on the plan, so it is built once
		// here and shared by every package's environment.
		wsVars:  release.WorkspaceEnv(pl, a.log),
		runner:  a.packageRunner(),
		covered: coveredReleases(pl, covered),
	}
	if err := a.checkSweepPlacements(work, covered); err != nil {
		return err
	}
	// With worker links the pool is opened here, and the coordinator owns
	// every ref the sweep creates until the deferred close deletes them.
	coordinator, err := a.openSweepDispatch(ctx, work, covered)
	if coordinator != nil {
		defer a.closeCoordinator(ctx, coordinator)
	}
	if err != nil {
		return err
	}
	started := time.Now()
	rep, drainErr := a.runSweep(ctx, pl, covered, work, sweepOptions{OnError: opts.OnError})
	mergeErr := a.finishSweepDispatch(ctx, coordinator, pl, rep, started)

	a.log.Info().Str("script", name).Int("ran", rep.Ran).Int("failed", rep.Failed).
		Int("skipped", rep.Skipped).Msg("run finished")
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("run interrupted")
		return drainErr
	}
	// A selection that is empty to begin with is a clean no-op: nothing
	// changed, and a run over no packages has nothing to say.
	if len(covered) > 0 && rep.Resolved == 0 {
		if err := a.reportNothingResolved(name, sel, covered); err != nil {
			return err
		}
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d run script(s) failed", rep.Failed)
	}
	return mergeErr
}

// reportNothingResolved decides what a sweep that resolved the script in no
// covered package means. The script exists somewhere — the typo guard said so
// — but not where this run looked. A selection the user spelled out, with a
// --package, --space or --group term or by invoking the command from a
// package or space folder, is a claim that the script reaches those packages,
// and running nothing against that claim is how a typo hides — so it errors.
// A selection the window assembled on its own claims nothing: a changed
// window that happens to hold only packages outside the script's reach is an
// honest no-op, said out loud at info so a green sweep still explains itself.
func (a *App) reportNothingResolved(name string, sel filter.Result, covered []string) error {
	if sel.IsActive() {
		err := fmt.Errorf("no selected package defines script %q (selected: %s)",
			name, strings.Join(covered, ", "))
		a.log.Error().Err(err).Msg("nothing to run")
		return err
	}
	a.log.Info().Str("script", name).Strs("covered", covered).
		Msg("script resolves in no covered package, nothing to do")
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
	configs := []*config.File{a.cfg}
	if a.workspace != nil {
		configs = configs[:0]
		for _, repo := range a.workspace.Repositories {
			if repo.Control || repo.Imported {
				configs = append(configs, repo.Config)
			}
		}
	}
	for _, cfg := range configs {
		if _, ok := cfg.Script(name); ok {
			return true
		}
		for _, sc := range cfg.Spaces {
			if _, ok := sc.Script(name); ok {
				return true
			}
		}
		for _, po := range cfg.Packages {
			if _, _, ok := public.FoldLookup(po.Scripts, name); ok {
				return true
			}
		}
	}
	if a.workspace != nil {
		if spaces, err := config.ResolvedWorkspaceSpaceConfigs(a.workspace); err == nil {
			for _, sc := range spaces {
				if _, ok := sc.Script(name); ok {
					return true
				}
			}
		}
		pkgs, err := a.packages()
		if err != nil {
			return false
		}
		for _, p := range pkgs {
			if _, ok := p.Space.Script(name); ok {
				return true
			}
		}
		return false
	}
	// The two levels only the filesystem knows come last, cheapest first: a
	// space folder's own config file defines its scripts whether or not the
	// space discovered a single package, which the package walk below — the
	// one place a script defined solely in a package folder's file can be
	// seen — cannot answer for an empty space.
	if spaces, err := config.ResolvedSpaceConfigs(a.cfg, a.root); err == nil {
		for _, sc := range spaces {
			if _, ok := sc.Script(name); ok {
				return true
			}
		}
	}
	pkgs, _, _, err := config.DiscoverPackages(a.cfg, a.root)
	if err != nil {
		return false
	}
	for _, p := range pkgs {
		if _, ok := p.Space.Script(name); ok {
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
	runner  script.Runnerx
	covered map[string]*plan.Release
	// coordinator places every package's task on the pool when the sweep has
	// worker links, and is nil for a sweep that runs everything here.
	coordinator *execution.Coordinator
	// roots are the folders the entry configuration declares the script
	// writes, which a task placed on another machine carries back.
	roots []string
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
	cmds, ok := rel.Pkg.Space.Script(w.name)
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
		if w.coordinator == nil {
			return seq.RunMergingOutputs(ctx, rel)
		}
		return w.placeTask(ctx, rel, seq)
	}, nil
}

// placeTask runs one package's task wherever the sweep's pool places it, and
// merges what it exported onto the release exactly as the sequence run here
// merges it, so a consumer reads its providers' exports whichever machine
// produced them.
//
// The environment travels in its two halves, as a stage frame's does: the
// DISPAT_* pairs computed from the plan as they are, and the configuration's
// own pairs unresolved, so a value naming a secret is expanded on the node
// that runs the commands and never enters a mailbox (§28.3).
func (w *scriptWork) placeTask(ctx context.Context, rel *plan.Release, seq release.Sequence) error {
	pkg := rel.Pkg.Name
	outcome, err := w.coordinator.Sweep(ctx, execution.SweepTask{
		Request: release.StageRequest{
			Release:   rel,
			Stage:     w.stage(),
			Frame:     release.StageFrame{Commands: seq.Commands},
			Env:       release.ComputedCommandEnv(w.pl, pkg, w.stage(), w.wsVars),
			StaticEnv: rel.Pkg.Space.Env,
			Dir:       rel.Pkg.Dir,
		},
		Script: w.name,
		Roots:  w.roots,
		Here: func(ctx context.Context) ([]plan.Output, error) {
			return seq.RunCollectingOutputs(ctx, pkg+":"+seq.Stage)
		},
	})
	release.MergeOutputs(rel, outcome.Exports)
	return err
}
