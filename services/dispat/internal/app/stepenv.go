package app

import (
	"fmt"
	"os"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The step commands' run wiring. A step command invoked from inside a running
// release — a flow hook, a publish script — replans, and a fresh plan mid-run
// is not the run's plan: earlier legs' tags have landed, group aggregates have
// moved, and in the worst case the step's own leg already created the very tag
// the step is about to read back as published history. The DISPAT_* environment
// every stage script inherits carries the run's own answers, so the wiring
// reads them back: the step scopes itself to the invoking package, masks the
// leg's own tag out of baseline resolution, and aligns its record to the run's
// version and tag — warning about drift it corrected (W228) and refusing what
// it cannot align (E219), because a failed leg is re-runnable where a drifted
// record is an incident.

// runEnv is the release run a step command was invoked from, as its
// environment states it.
type runEnv struct {
	pkg  string
	tag  string
	next ccme.Version
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
	return &runEnv{pkg: pkg, tag: tag, next: next}, nil
}

// wireStep applies the run's environment to a step invocation before it
// plans: the run's own tag is masked from baseline resolution, and an
// invocation with no filter terms of its own narrows to the invoking package.
// The returned env is what alignStep later holds the plan to; nil means the
// step runs outside any run, exactly as before.
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
	if len(w.Filter.Packages)+len(w.Filter.Spaces)+len(w.Filter.Groups) == 0 {
		w.Filter.Packages = []string{env.pkg}
	}
	a.log.Debug().Str("package", env.pkg).Str("tag", env.tag).
		Str("version", env.next.String()).Msg("step wired to the run's environment")
	return env, nil
}

// alignStep holds a wired step's plan to the run that invoked it. The run's
// version and tag are the authority: a replan that drifted is corrected and
// said out loud (W228), and a plan that cannot be corrected — the package
// missing, not releasing, or rendering a different tag even at the run's own
// version — is refused with nothing written (E219).
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
	return nil
}

// channelOfVersion is the channel a version sits on: its first prerelease
// identifier, or stable.
func channelOfVersion(v ccme.Version) string {
	if len(v.Prerelease) > 0 {
		return v.Prerelease[0]
	}
	return ccme.ChannelStable
}
