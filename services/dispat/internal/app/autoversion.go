package app

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// AutoVersionOptions selects what AutoVersion covers and the policy it runs
// under. The override fields, when set, replace the corresponding fields of
// each target space's autoVersion block for this invocation; a space with no
// block at all is still skipped unless an override forces a policy, which
// then starts from the defaults (every dependency kind, any range, write the
// own version).
type AutoVersionOptions struct {
	Window       WindowOptions // which packages the command covers
	OnError      string        // what a failure does to the failed package's dependents
	Range        string        // overrides autoVersion.range
	Match        []string      // overrides autoVersion.match
	Manifests    string        // overrides autoVersion.manifests (root, all or none)
	WriteVersion *bool         // overrides autoVersion.writeVersion
	NoReplace    bool          // skip the autoVersion.replace rules for this invocation
	OnlyUpdated  bool          // rewrite only what this run updates
	SyncLock     bool          // run the space's syncLock scripts for changed packages
}

// hasPolicy reports whether any policy override was set, which is what makes
// the command act on a space without an autoVersion block.
func (o AutoVersionOptions) hasPolicy() bool {
	return o.Range != "" || len(o.Match) > 0 || o.Manifests != "" ||
		o.WriteVersion != nil || o.NoReplace || o.OnlyUpdated
}

// policy resolves each covered package's effective autoVersion block: its
// space's, with this invocation's overrides laid over it. One resolver serves
// the reconciliation and the syncLock pass alike — reading the space's block
// directly in the second would mean a flag override reconciled files whose
// lock nothing then regenerated.
func (o AutoVersionOptions) policy() func(*plan.Release) *model.AutoVersion {
	if !o.hasPolicy() {
		return func(rel *plan.Release) *model.AutoVersion { return rel.Pkg.Space.AutoVersion }
	}
	return func(rel *plan.Release) *model.AutoVersion {
		av := effectivePolicy(rel.Pkg.Space.AutoVersion)
		if o.Range != "" {
			av.Range = o.Range
		}
		if len(o.Match) > 0 {
			av.Match = o.Match
		}
		if o.Manifests != "" {
			av.Manifests = model.ManifestScope(o.Manifests)
		}
		if o.WriteVersion != nil {
			av.WriteVersion = *o.WriteVersion
		}
		if o.NoReplace {
			av.Replace = nil
		}
		if o.OnlyUpdated {
			av.OnlyUpdated = true
		}
		return av
	}
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
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot auto-version")
		return err
	}

	work := &autoVersionWork{
		app: a, versioner: release.NewAutoVersioner(ctx, pl, a.scan, a.log), policy: opts.policy(),
	}
	if _, err := a.sweepStep(ctx, pl, covered, work, opts.OnError, "auto-versioning"); err != nil {
		return err
	}
	if !opts.SyncLock {
		return nil
	}
	return a.syncLock(ctx, pl, work.needSyncLock(pl, covered))
}

// autoVersionWork is `dispat autoversion`'s share of a sweep: one package's
// manifests reconciled to the planned versions.
type autoVersionWork struct {
	app       *App
	versioner *release.AutoVersioner
	policy    func(*plan.Release) *model.AutoVersion
}

func (w *autoVersionWork) stage() string { return "autoversion" }

func (w *autoVersionWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	if !w.app.releasing(rel) {
		return nil, nil
	}
	if w.policy(rel) == nil {
		w.app.log.Debug().Str("package", rel.Pkg.Name).
			Msg("space has no autoVersion block, nothing to reconcile")
		return nil, nil
	}
	return func(ctx context.Context) error {
		return w.versioner.Package(ctx, rel, w.policy)
	}, nil
}

// needSyncLock lists, in the order they were covered, the packages whose lock
// files have to be regenerated: the ones whose manifests this run actually
// changed, plus the ones whose space reconciles nothing at all. A space with
// neither strategy produces no change to key off, so its scripts run every
// time, exactly as they do in a release.
func (w *autoVersionWork) needSyncLock(pl *plan.Plan, covered []string) []string {
	var out []string
	for _, name := range covered {
		av := w.policy(pl.Releases[name])
		if av == nil || len(av.SyncLock) == 0 {
			continue
		}
		if !w.versioner.Changed(name) && av.Reconciles() {
			w.app.log.Debug().Str("package", name).
				Msg("syncLock: nothing was reconciled, nothing to regenerate")
			continue
		}
		out = append(out, name)
	}
	return out
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
