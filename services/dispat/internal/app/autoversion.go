package app

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/filter"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// AutoVersionOptions selects what AutoVersion covers and the policy it runs
// under. The override fields, when set, replace the corresponding fields of
// each target space's autoVersion block for this invocation; a space with no
// block at all is still skipped unless an override forces a policy, which
// then starts from the defaults (every dependency kind, any range, write the
// own version).
type AutoVersionOptions struct {
	Filter       filter.Filter // which packages the command covers
	Range        string        // overrides autoVersion.range
	Match        []string      // overrides autoVersion.match
	Manifests    string        // overrides autoVersion.manifests (root, all or none)
	WriteVersion *bool         // overrides autoVersion.writeVersion
	NoReplace    bool          // skip the autoVersion.replace rules for this invocation
	SyncLock     bool          // run the space's syncLock scripts for changed packages
}

// hasPolicy reports whether any policy override was set, which is what makes
// the command act on a space without an autoVersion block.
func (o AutoVersionOptions) hasPolicy() bool {
	return o.Range != "" || len(o.Match) > 0 || o.Manifests != "" || o.WriteVersion != nil || o.NoReplace
}

// AutoVersion runs the native manifest reconciliation for the covered
// packages and, mirroring the executor, each changed package's syncLock
// scripts, serially. Rewriting already-reconciled manifests changes nothing,
// so re-running is safe.
func (a *App) AutoVersion(ctx context.Context, opts AutoVersionOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	targets, err := a.stepTargets(pl, opts.Filter)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot auto-version")
		return err
	}

	var policy func(*plan.Release) *model.AutoVersion
	if opts.hasPolicy() {
		policy = func(rel *plan.Release) *model.AutoVersion {
			av := effectivePolicy(rel.Pkg.Space.AutoVersion)
			if opts.Range != "" {
				av.Range = opts.Range
			}
			if len(opts.Match) > 0 {
				av.Match = opts.Match
			}
			if opts.Manifests != "" {
				av.Manifests = model.ManifestScope(opts.Manifests)
			}
			if opts.WriteVersion != nil {
				av.WriteVersion = *opts.WriteVersion
			}
			if opts.NoReplace {
				av.Replace = nil
			}
			return av
		}
	}

	changed, err := release.AutoVersionPackages(ctx, pl, targets, a.scan, a.log, policy)
	if err != nil {
		a.log.Error().Err(err).Msg("auto-versioning failed")
		return err
	}
	if !opts.SyncLock {
		return nil
	}
	// The serial loop is the syncLock budget of 1 by construction: shared
	// lock files corrupt under parallel writers.
	wsVars := release.WorkspaceEnv(pl, a.log)
	runner := &script.ShellRunner{Shell: a.cfg.Shell}
	for _, name := range targets {
		rel := pl.Releases[name]
		av := rel.Pkg.Space.AutoVersion
		if av == nil || len(av.SyncLock) == 0 {
			continue
		}
		if !changed[name] {
			a.log.Debug().Str("package", name).Msg("syncLock: no manifest changed, nothing to regenerate")
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

// effectivePolicy clones the space's autoVersion block as the base the flag
// overrides apply onto; a space without one starts from the defaults the
// config loader would resolve (every dependency kind, any range, write the
// own version).
func effectivePolicy(av *model.AutoVersion) *model.AutoVersion {
	if av == nil {
		return &model.AutoVersion{
			Manifests: model.ScopeRoot,
			Kinds: map[model.DepKind]bool{
				model.KindDependencies: true, model.KindDevDependencies: true,
				model.KindPeerDependencies: true, model.KindOptionalDependencies: true,
			},
			WriteVersion: true,
		}
	}
	clone := *av
	return &clone
}
