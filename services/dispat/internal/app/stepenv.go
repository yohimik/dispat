package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The step commands' run wiring. A step command invoked from inside a running
// release — a flow hook, a publish script — replans, and a fresh plan mid-run
// is not the run's plan: earlier legs' tags have landed, group aggregates have
// moved, and in the worst case the step's own leg already created the very tag
// the step is about to read back as published history. The DISPAT_* environment
// every stage script inherits carries the run's own answers, so the wiring
// reads them back: the step scopes itself to the invoking package, masks every
// tag the run has already written out of baseline resolution (so the replan
// reads the baselines the run itself started from), and aligns its record to
// the run's version, tag and provider updates — warning about drift it
// corrected (W228) and refusing what it cannot align (E219), because a failed
// leg is re-runnable where a drifted record is an incident.

// runEnv is the release run a step command was invoked from, as its
// environment states it.
type runEnv struct {
	pkg  string
	tag  string
	next ccme.Version
	// updates is the run's live provider-update listing (DISPAT_UPDATED_*).
	// updatesListed distinguishes an empty listing — the run says there are
	// no live updates, and the record is aligned to none — from an
	// environment that carries no listing at all.
	updates       []plan.ProviderUpdate
	updatesListed bool
	// releasing is every package the run's workspace listing marks as
	// releasing, with the version the run gives it: the input tag masking
	// reproduces the run's baselines from.
	releasing []releasingPackage
}

// releasingPackage is one releasing entry of the run's workspace listing.
type releasingPackage struct {
	name    string
	version ccme.Version
}

// stepRunEnv reads the wiring out of the process environment: nil outside a
// run (any of the three variables absent), an error for an environment that
// names a run but pins a version that does not parse.
func stepRunEnv() (*runEnv, error) {
	pkg := os.Getenv(plan.PackageEnvVar)
	tag := os.Getenv(plan.TagEnvVar)
	raw := os.Getenv(plan.NewVersionEnvVar)
	if pkg == "" || tag == "" || raw == "" {
		return nil, nil
	}
	next, err := ccme.ParseVersion(raw)
	if err != nil {
		return nil, fmt.Errorf("%s=%q does not parse: %w", plan.NewVersionEnvVar, raw, err)
	}
	env := &runEnv{pkg: pkg, tag: tag, next: next}
	if err := env.readUpdates(); err != nil {
		return nil, err
	}
	env.readReleasing()
	return env, nil
}

// readUpdates reads the run's live provider updates (DISPAT_UPDATED_*). A
// listing that names a key whose variables do not describe an update is an
// error, the same rule as an unparseable pinned version: the record's
// dependency lines are at stake, and writing them wrong is worse than
// failing a leg that can re-run.
//
// The provider's tag is read beside the movement and deliberately stays out
// of that strictness. It is what a record's `dependencyLink: auto` hangs the
// line off, and a step invoked by a run older than the variable inherits an
// environment that never carried one; the update is still a true statement of
// the movement, and the renderer already declines an "auto" link it has no tag
// for. Refusing the leg over an absent tag would turn a cosmetic difference
// into a failed release.
func (e *runEnv) readUpdates() error {
	list, ok := os.LookupEnv(plan.UpdatedPackagesEnvVar)
	if !ok {
		return nil
	}
	e.updatesListed = true
	for _, key := range strings.Fields(list) {
		pre := plan.UpdatedEnvPrefix + key
		name := os.Getenv(pre + "_NAME")
		from, fromErr := ccme.ParseVersion(os.Getenv(pre + "_OLD_VERSION"))
		to, toErr := ccme.ParseVersion(os.Getenv(pre + "_NEW_VERSION"))
		if name == "" || fromErr != nil || toErr != nil {
			return fmt.Errorf("%s names %s, but the %s_* variables do not describe an update",
				plan.UpdatedPackagesEnvVar, key, pre)
		}
		e.updates = append(e.updates, plan.ProviderUpdate{
			Name: name, From: from, To: to, Tag: os.Getenv(pre + "_TAG"),
		})
	}
	return nil
}

// readReleasing collects the run's releasing packages from the workspace
// listing. Best-effort by design: an entry that does not resolve is skipped,
// because the listing only feeds tag masking — a reproduction aid — and
// alignStep still holds the record to the run.
func (e *runEnv) readReleasing() {
	for _, key := range strings.Fields(os.Getenv(plan.WorkspacePackagesEnvVar)) {
		pre := plan.WorkspaceEnvPrefix + key
		if os.Getenv(pre+"_RELEASING") != "true" {
			continue
		}
		name := os.Getenv(pre + "_NAME")
		version, err := ccme.ParseVersion(os.Getenv(pre + "_VERSION"))
		if name == "" || err != nil {
			continue
		}
		e.releasing = append(e.releasing, releasingPackage{name: name, version: version})
	}
}

// wireStep applies the run's environment to a step invocation before it
// plans: every tag the run has already written is masked from baseline
// resolution, and an invocation with no filter terms of its own narrows to
// the invoking package. The returned env is what alignStep later holds the
// plan to; nil means the step runs outside any run, exactly as before.
func (a *App) wireStep(w *WindowOptions) (*runEnv, error) {
	env, err := stepRunEnv()
	if err != nil {
		a.log.Error().Err(err).Str("code", plan.CodeStepUnalignable).
			Msg("step invoked inside a run it cannot honor")
		return nil, err
	}
	if env == nil {
		return nil, nil
	}
	a.ignoreTags = append(a.ignoreTags, env.tag)
	a.maskRunTags(env)
	if len(w.Filter.Packages)+len(w.Filter.Spaces)+len(w.Filter.Groups) == 0 {
		w.Filter.Packages = []string{env.pkg}
	}
	a.log.Debug().Str("package", env.pkg).Str("tag", env.tag).
		Str("version", env.next.String()).Msg("step wired to the run's environment")
	return env, nil
}

// maskRunTags masks the release tag of every package the run is releasing,
// so the replan reads the same baselines the run itself started from. A tag
// an earlier leg has already written would otherwise become a baseline the
// run never had — and, for a versioning group, a floor above the run's own
// aggregate, drifting every groupmate one step further. Best-effort: a name
// the workspace no longer carries is skipped, and a tag that was never
// written masks nothing.
func (a *App) maskRunTags(env *runEnv) {
	if len(env.releasing) == 0 {
		return
	}
	pkgs, err := a.packages()
	if err != nil {
		a.log.Debug().Err(err).Msg("cannot resolve packages to mask the run's tags")
		return
	}
	byName := make(map[string]*model.Package, len(pkgs))
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	for _, r := range env.releasing {
		p := byName[r.name]
		if p == nil {
			continue
		}
		a.ignoreTags = append(a.ignoreTags, plan.TagFormatFor(p).Render(r.name, r.version))
	}
}

// alignStep holds a wired step's plan to the run that invoked it. The run's
// version, tag and provider updates are the authority: a replan that drifted
// is corrected and said out loud (W228), and a plan that cannot be corrected
// — the package missing, not releasing, or rendering a different tag even at
// the run's own version — is refused with nothing written (E219).
func (a *App) alignStep(pl *plan.Plan, env *runEnv) error {
	if env == nil {
		return nil
	}
	rel := pl.Releases[env.pkg]
	if rel == nil || !rel.Releasing() {
		err := fmt.Errorf("the run releases %s, but the step's own plan does not", env.pkg)
		a.log.Error().Err(err).Str("code", plan.CodeStepUnalignable).Str("package", env.pkg).
			Msg("step cannot align to the run, nothing written")
		return err
	}
	if rel.Next.String() != env.next.String() {
		a.log.Warn().Str("code", plan.CodeStepAligned).Str("package", env.pkg).
			Str("stepPlanned", rel.Next.String()).Str("run", env.next.String()).
			Msg("step plan drifted from the run, aligned to the environment")
		rel.Next = env.next
		rel.Channel = channelOfVersion(env.next)
	}
	if rel.TagName() != env.tag {
		err := fmt.Errorf("the aligned version renders tag %q where the run created %q", rel.TagName(), env.tag)
		a.log.Error().Err(err).Str("code", plan.CodeStepUnalignable).Str("package", env.pkg).
			Msg("step cannot align to the run, nothing written")
		return err
	}
	if env.updatesListed && !sameUpdates(rel.Updates, env.updates) {
		// The dependencies section of the record states the run's provider
		// movements; a replan that saw an earlier leg's tag as a foreign
		// baseline would misstate them — a provider dropped as "already
		// released", or a groupmate reported one step past where the run
		// put it.
		a.log.Warn().Str("code", plan.CodeStepAligned).Str("package", env.pkg).
			Str("stepPlanned", renderUpdates(rel.Updates)).Str("run", renderUpdates(env.updates)).
			Msg("step plan's dependency updates drifted from the run, aligned to the environment")
		rel.Updates = env.updates
	}
	return nil
}

// sameUpdates reports whether two update listings state the same movements
// in the same order.
//
// Movements only: the providers' tags are carried by both listings and
// compared by neither. Drift is what the alignment exists to correct, and a
// tag is not a movement — two listings naming the same providers at the same
// versions describe the same run whatever their tags are spelled like, so a
// tag alone must not raise W228 over a record that says the right thing.
//
// Nothing is lost by leaving it out, because of which listing survives in each
// branch. Agreement keeps the replan's own updates, whose tags this process
// rendered through each provider's real tagFormat. Drift replaces them
// wholesale with the environment's, whose tags the run rendered the same way
// and wrote into DISPAT_UPDATED_<KEY>_TAG. Either way the surviving listing's
// tags came from the configuration that owns them.
func sameUpdates(a, b []plan.ProviderUpdate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].From.String() != b[i].From.String() ||
			a[i].To.String() != b[i].To.String() {
			return false
		}
	}
	return true
}

// renderUpdates renders a listing for the drift warning, one clause per
// provider, the way the changelog's dependencies section spells a movement.
func renderUpdates(updates []plan.ProviderUpdate) string {
	lines := make([]string, 0, len(updates))
	for _, u := range updates {
		lines = append(lines, u.Name+": "+u.From.String()+" -> "+u.To.String())
	}
	return strings.Join(lines, ", ")
}

// channelOfVersion is the channel a version sits on: its first prerelease
// identifier, or stable.
func channelOfVersion(v ccme.Version) string {
	if len(v.Prerelease) > 0 {
		return v.Prerelease[0]
	}
	return ccme.ChannelStable
}
