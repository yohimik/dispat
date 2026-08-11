package app

import (
	"context"
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// GitHubOptions selects what GitHub covers and where it publishes. The
// override fields, when set, replace the corresponding github.* config
// values for every package of the invocation.
type GitHubOptions struct {
	Filter   filter.Filter // which packages the command covers
	Owner    string        // overrides github.owner
	Repo     string        // overrides github.repo
	APIURL   string        // overrides github.apiUrl
	TokenEnv string        // overrides github.tokenEnv
	// Target is sent as target_commitish, so GitHub creates the tag at
	// exactly that commit or branch. Only safe once the commit exists on the
	// remote; empty leaves the choice to GitHub (the default branch head).
	Target string
}

// GitHub creates each covered package's release now — the same release the
// run's recorder would create — so a flow can publish it from its own stage
// instead of waiting for the end of the run. A release the repository
// already carries is a skip (W224), which is also what makes a later run
// skip the releases published here.
//
// Meant for a stage script: the per-package script environment is the
// process environment, so DISPAT_EXPORT_GITHUB (the opt-in, and the list of
// files to attach) is read from there, and DISPAT_PACKAGE says which package
// it belongs to.
func (a *App) GitHub(ctx context.Context, opts GitHubOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	targets, err := a.stepTargets(pl, opts.Filter)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot create the github release")
		return err
	}

	export := a.readExport(targets)
	// One releaser per distinct target the covered policies name, verified
	// once each before any release work — the same shape githubDispatch
	// builds for a run, minus the run.
	releasers := make(map[model.GitHubSpec]*github.Releaser)
	for _, name := range targets {
		rel := pl.Releases[name]
		spec := a.githubSpec(rel.Pkg.GitHub, opts)
		if !spec.Records(rel.IsPrerelease()) {
			github.LogSkip(a.log, spec, rel)
			continue
		}
		gh, seen := releasers[spec]
		if !seen {
			if gh, err = githubReleaser(spec, a.log); err != nil {
				a.log.Error().Err(err).Str("package", name).Msg("cannot create the github release")
				return err
			}
			if err := gh.Verify(ctx); err != nil {
				a.log.Error().Err(err).Msg("github verification failed")
				return err
			}
			releasers[spec] = gh
		}
		gh.TargetCommitish = opts.Target
		if export.covers(name) {
			// A step invocation has no run behind it, so the release carries
			// no outputs of its own; the stage's own environment supplies the
			// opt-in and the attachment list. Without an export the package
			// has not opted in, and only github.allPackages releases it —
			// exactly as during a run.
			rel.Outputs = append(rel.Outputs, plan.Output{Name: plan.GitHubExport, Value: export.value})
		}
		// The releaser reports the outcome itself — created, or skipped with
		// its reason — so every path through it reads the same in the log,
		// whether a run or this command drove it.
		if err := gh.Record(ctx, rel); err != nil {
			a.log.Error().Err(err).Str("package", name).Msg("github release failed")
			return err
		}
	}
	return nil
}

// githubSpec overlays the invocation's flag overrides onto a package's
// resolved policy: an explicit flag beats the layered configuration, for
// every package the command covers.
func (a *App) githubSpec(spec model.GitHubSpec, opts GitHubOptions) model.GitHubSpec {
	spec.Owner = firstOf(opts.Owner, spec.Owner)
	spec.Repo = firstOf(opts.Repo, spec.Repo)
	spec.APIURL = firstOf(opts.APIURL, spec.APIURL)
	spec.TokenEnv = firstOf(opts.TokenEnv, spec.TokenEnv)
	return spec
}

// stepExport is the GitHub opt-in an invocation found in its environment.
// Presence is the opt-in and the value is the list of files to attach, so an
// export set to nothing still means "release this package" — the same rule
// the run's recorder reads off rel.Outputs.
type stepExport struct {
	value   string // the whitespace-separated attachment list, possibly empty
	present bool   // the variable was set at all: the package opted in
	// pkg is the package the environment belongs to, or "" when nothing
	// named one and the export covers every package the invocation covers.
	pkg string
}

// covers reports whether the export applies to the named package.
func (e stepExport) covers(name string) bool {
	return e.present && (e.pkg == "" || e.pkg == name)
}

// readExport reads the opt-in out of the process environment. Inside a stage
// script both variables are set, so the export belongs to that one package.
// Outside one — a hand invocation exporting the variable itself —
// DISPAT_PACKAGE is unset and the export covers every package the invocation
// covers, which is what a single-package invocation means by it.
//
// A DISPAT_PACKAGE naming a package this invocation does not cover is a
// stale environment (a nested invocation narrowed elsewhere): the export is
// dropped rather than applied to the wrong release.
func (a *App) readExport(targets []string) stepExport {
	value, present := os.LookupEnv(plan.GitHubExport)
	e := stepExport{value: value, present: present, pkg: os.Getenv("DISPAT_PACKAGE")}
	if !present || e.pkg == "" {
		return e
	}
	for _, name := range targets {
		if name == e.pkg {
			return e
		}
	}
	a.log.Debug().Str("package", e.pkg).
		Msgf("%s belongs to a package this invocation does not cover, ignoring it", plan.GitHubExport)
	return stepExport{pkg: e.pkg}
}
