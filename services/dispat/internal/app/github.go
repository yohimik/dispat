package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// GitHubOptions selects what GitHub covers and where it publishes. The
// override fields, when set, replace the corresponding github.* config
// values for every package of the invocation.
type GitHubOptions struct {
	Window   WindowOptions // which packages the command covers
	OnError  string        // what a failure does to the failed package's dependents
	Owner    string        // overrides github.owner
	Repo     string        // overrides github.repo
	APIURL   string        // overrides github.apiUrl
	TokenEnv string        // overrides github.tokenEnv
	// Target is sent as target_commitish, so GitHub creates the tag at
	// exactly that commit or branch. Only safe once the commit exists on the
	// remote; empty leaves the choice to GitHub (the default branch head).
	Target string
	// ReleaseName overrides github.releaseName, the release's name. It is
	// interpolated like the configured value.
	ReleaseName string
	// Authors overrides the github.authors object, field by field.
	Authors AuthorOptions
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
	if err := opts.Authors.validate(); err != nil {
		a.log.Error().Err(err).Msg("cannot create the github release")
		return err
	}
	env, err := a.wireStep(&opts.Window)
	if err != nil {
		return err
	}
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	if err := a.alignStep(pl, env); err != nil {
		return err
	}
	if env != nil {
		// Ordering smell, said out loud before anything is created: a release
		// for a tag nobody made yet has GitHub invent the tag at the default
		// branch head — the wrong commit, looking plausible.
		if exists, terr := a.git.TagExists(ctx, env.tag); terr == nil && !exists {
			a.log.Warn().Str("code", plan.CodeStepBeforeTag).Str("tag", env.tag).
				Msg("github step before the run's tag exists; the commit step belongs first")
		}
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot create the github release")
		return err
	}

	work := &githubWork{app: a, opts: opts, export: a.readExport(covered),
		releasers: make(map[string]*github.Releaser)}
	_, err = a.sweepStep(ctx, pl, covered, work, opts.OnError, "github release")
	return err
}

// githubWork is `dispat github`'s share of a sweep: one package's release
// created through the API.
//
// It is serial. Nothing here would corrupt under two goroutines, but the
// releases of one run land in a readable order this way, and a repository's
// API budget is one shared resource whatever the build budget says. That is
// also what lets the releaser cache be a plain map: one releaser per distinct
// target the covered policies name, verified once each, exactly as a run's
// dispatch builds it.
type githubWork struct {
	app       *App
	opts      GitHubOptions
	export    stepExport
	releasers map[string]*github.Releaser
}

func (w *githubWork) stage() string { return "github" }
func (w *githubWork) serial() bool  { return true }

func (w *githubWork) resolve(ctx context.Context, rel *plan.Release) (task, error) {
	if !w.app.releasing(rel) {
		return nil, nil
	}
	spec := w.app.githubSpec(rel.Pkg.GitHub, w.opts)
	if !spec.Records(rel.Channel) {
		github.LogSkip(w.app.log, spec, rel)
		return nil, nil
	}
	gh, seen := w.releasers[spec.Key()]
	if !seen {
		var err error
		if gh, err = githubReleaser(spec, w.app.log); err != nil {
			return nil, err
		}
		if err := gh.Verify(ctx); err != nil {
			return nil, fmt.Errorf("github verification failed: %w", err)
		}
		gh.TargetCommitish = w.opts.Target
		w.releasers[spec.Key()] = gh
	}
	return func(ctx context.Context) error {
		if w.export.covers(rel.Pkg.Name) {
			// A step invocation has no run behind it, so the release carries
			// no outputs of its own; the stage's own environment supplies the
			// opt-in and the attachment list. Without an export the package
			// has not opted in, and only github.allPackages releases it —
			// exactly as during a run.
			rel.Outputs = append(rel.Outputs, plan.Output{Name: plan.GitHubExport, Value: w.export.value})
		}
		// The releaser reports the outcome itself — created, or skipped with
		// its reason — so every path through it reads the same in the log,
		// whether a run or this command drove it.
		if err := gh.Record(ctx, rel); err != nil {
			return fmt.Errorf("github release failed: %w", err)
		}
		return nil
	}, nil
}

// githubSpec overlays the invocation's flag overrides onto a package's
// resolved policy: an explicit flag beats the layered configuration, for
// every package the command covers.
func (a *App) githubSpec(spec model.GitHubSpec, opts GitHubOptions) model.GitHubSpec {
	spec.Owner = firstOf(opts.Owner, spec.Owner)
	spec.Repo = firstOf(opts.Repo, spec.Repo)
	spec.APIURL = firstOf(opts.APIURL, spec.APIURL)
	spec.TokenEnv = firstOf(opts.TokenEnv, spec.TokenEnv)
	spec.Format.ReleaseName = firstOf(opts.ReleaseName, spec.Format.ReleaseName)
	// The authors overrides land on the format before the spec is keyed, so
	// two packages the flags now differentiate stop sharing one releaser
	// exactly as two packages the configuration differentiates do.
	spec.Format = opts.Authors.apply(spec.Format)
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

// dedupeFields drops repeated whitespace-separated entries, keeping first
// occurrences in order: a path restated across the environment and the output
// file is one attachment, not two upload attempts.
func dedupeFields(value string) string {
	fields := strings.Fields(value)
	seen := make(map[string]bool, len(fields))
	kept := fields[:0]
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
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
	e := stepExport{value: value, present: present, pkg: os.Getenv(plan.PackageEnvVar)}
	// An export written to $DISPAT_OUTPUT earlier in the same script has not
	// reached the environment yet — the run parses the file between stages —
	// so the file is read too, and being the fresher record, its last word
	// wins over the inherited variable. Winning means replacing: the two
	// spellings of one export must not concatenate into a doubled attachment
	// list, and the surviving list is deduplicated for the same reason.
	if path := os.Getenv(release.OutputEnvVar); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if v, ok := strings.CutPrefix(line, plan.GitHubExport+"="); ok {
					e.value, e.present = strings.TrimSpace(v), true
				}
			}
		}
	}
	e.value = dedupeFields(e.value)
	if !e.present || e.pkg == "" {
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
