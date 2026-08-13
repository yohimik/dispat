package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// The standalone step commands — changelog, autoversion, autowriter, commit,
// github — expose the release pipeline's native steps to custom flows, so a
// stage script can run a step at the moment the flow needs it (a changelog
// written before the release commit lands inside the tagged tree) and the
// release stage later finds the work done and skips it.
//
// They cover packages the same way `dispat run` does and sweep them the same
// way, which is what makes one selection rule serve every command in the
// program. What they add is the rule below: a step acts on a package the plan
// is releasing, and a package a wider window pulled in with nothing pending is
// a no-op rather than a failure.

// stepPlan computes the quiet plan the step commands run against: diagnostics
// printed, no graph (a stage-script invocation must not spam the full plan),
// and a fatal plan refused exactly like every other command.
func (a *App) stepPlan(ctx context.Context) (*plan.Plan, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return nil, err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return nil, fmt.Errorf("no correct plan exists")
	}
	return pl, nil
}

// sweepStep runs one step command's sweep and maps it onto the command's
// error. Unlike `dispat run`, a sweep in which no package had anything to do is
// not a mistake here: a custom flow calls these on every release, and a run
// where the selected packages turn out to have nothing pending is the ordinary
// case, so failing it would break the flow over a quiet day.
func (a *App) sweepStep(ctx context.Context, pl *plan.Plan, covered []string,
	work packageWork, onError, headline string) (sweepReport, error) {
	rep, drainErr := a.runSweep(ctx, pl, covered, work, sweepOptions{OnError: onError})
	a.log.Info().Str("stage", work.stage()).Int("ran", rep.Ran).
		Int("failed", rep.Failed).Int("skipped", rep.Skipped).Msg(headline + " finished")
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg(headline + " interrupted")
		return rep, drainErr
	}
	if rep.Failed > 0 {
		return rep, fmt.Errorf("%s failed for %d package(s)", headline, rep.Failed)
	}
	return rep, nil
}

// syncLock runs the named packages' syncLock scripts, serially and in the
// order given. The serial loop is the syncLock budget of 1 by construction:
// shared lock files corrupt under parallel writers, which is why the release
// schedules them on a budget of one too. The caller decides which packages
// need it; a package whose space has no such scripts is stepped over.
func (a *App) syncLock(ctx context.Context, pl *plan.Plan, pkgs []string) error {
	if len(pkgs) == 0 {
		a.log.Debug().Msg("syncLock: nothing was rewritten, nothing to regenerate")
		return nil
	}
	wsVars := release.WorkspaceEnv(pl, a.log)
	runner := &script.ShellRunner{Shell: a.cfg.Shell, Log: a.log}
	for _, name := range pkgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := pl.Releases[name]
		av := rel.Pkg.Space.AutoVersion
		if av == nil || len(av.SyncLock) == 0 {
			continue
		}
		log := a.log.With().Str("package", name).Str("stage", "syncLock").Logger()
		seq := release.Sequence{Runner: runner, Dir: rel.Pkg.Dir, Stage: "syncLock",
			Commands: av.SyncLock, Env: release.CommandEnv(pl, name, "syncLock", wsVars),
			Log: log, FailFast: true}
		if err := seq.Run(ctx); err != nil {
			log.Error().Err(err).Msg("syncLock failed")
			return err
		}
		log.Info().Msg("syncLock succeeded")
	}
	return nil
}

// releasing reports whether the plan is releasing this package, saying so when
// it is not. Only --since and --consumers can put a package with nothing
// pending in front of a step command; the default window is the releasing
// packages themselves.
func (a *App) releasing(rel *plan.Release) bool {
	if rel.Releasing() {
		return true
	}
	a.log.Info().Str("package", rel.Pkg.Name).Msg("package is not releasing, nothing to do")
	return false
}
