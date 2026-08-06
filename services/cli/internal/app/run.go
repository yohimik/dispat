package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/cli/internal/graph"
	"github.com/yohimik/dispat/services/cli/internal/plan"
	"github.com/yohimik/dispat/services/cli/internal/release"
	"github.com/yohimik/dispat/services/cli/internal/script"
)

// Run script error policies: what a failing run script does to the failed
// package's dependents (the --on-error flag of `dispat run`).
const (
	// OnErrorSkip (default) skips the changed dependents of a failed package
	// — transitively, exactly like a release skips consumers of a failed
	// provider. Independent packages always keep running.
	OnErrorSkip = "skip"
	// OnErrorContinue still runs the dependents; the run's exit code reports
	// the failure either way.
	OnErrorContinue = "continue"
)

// ValidOnError reports whether the value is a known run error policy.
func ValidOnError(v string) bool { return v == OnErrorSkip || v == OnErrorContinue }

// RunScript computes the plan and executes the named space run script inside
// each changed package with the package's full DISPAT_* environment,
// honouring the dependency graph: a package's script starts only after every
// changed provider's has finished, and independent packages run concurrently
// within the build concurrency budget. A changed package whose space does not
// define the script completes as a no-op; a name no space defines at all is
// an error, because running nothing silently is how a typo hides. onError
// decides what a failure does to the failed package's dependents (OnErrorSkip
// or OnErrorContinue); any failure makes the whole command fail.
func (a *App) RunScript(ctx context.Context, name, onError string) error {
	defined := false
	for _, sc := range a.cfg.Spaces {
		if _, ok := sc.RunScript(name); ok {
			defined = true
			break
		}
	}
	if !defined {
		msg := fmt.Sprintf("no space defines run script %q", name)
		if note, ok := lookupNote(name); ok {
			msg = note
		}
		err := errors.New(msg)
		a.log.Error().Err(err).Msg("unknown run script")
		return err
	}
	if onError == "" {
		onError = OnErrorSkip
	}

	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if pl.Fatal() {
		a.log.Error().Msg("refusing to run: the repository cannot produce a correct plan")
		return errors.New("no correct plan exists")
	}

	runner := &script.ShellRunner{Shell: a.cfg.Shell}
	stage := "run:" + name

	// The task graph: one node per changed package, edges from every changed
	// provider — the same shape the release executor schedules, at package
	// rather than stage granularity.
	changed := make(map[string]*plan.Release)
	sched := graph.NewScheduler[string]()
	for _, rel := range pl.Releasing() {
		changed[rel.Pkg.Name] = rel
		sched.Add(rel.Pkg.Name)
	}
	for pkg := range changed {
		for _, prov := range pl.Providers[pkg] {
			if _, ok := changed[prov]; ok {
				sched.AddEdge(prov, pkg)
			}
		}
	}

	type outcome struct{ failed, skipped, ran bool }
	results := make(map[string]*outcome, len(changed))
	var mu sync.Mutex

	execute := func(pkg string) {
		rel := changed[pkg]
		res := &outcome{}
		defer func() {
			mu.Lock()
			results[pkg] = res
			mu.Unlock()
		}()

		// Carry the changed providers' exports in before anything of this
		// package runs: the run-command counterpart of the pipeline's
		// per-package accumulation, transitive by construction (each provider
		// carried its own providers' exports before exporting). Providers
		// merge in name order, so a name two of them export resolves
		// deterministically; the package's own later export overrides either.
		provs := append([]string(nil), pl.Providers[pkg]...)
		sort.Strings(provs)
		for _, prov := range provs {
			if pr, ok := changed[prov]; ok {
				release.MergeOutputs(rel, pr.Outputs)
			}
		}

		if onError == OnErrorSkip {
			// Providers finished before this package was handed out, so their
			// outcomes are final; a skipped provider cascades the skip.
			mu.Lock()
			blocker := ""
			for _, prov := range pl.Providers[pkg] {
				if r, ok := results[prov]; ok && (r.failed || r.skipped) {
					blocker = prov
					break
				}
			}
			mu.Unlock()
			if blocker != "" {
				res.skipped = true
				a.log.Warn().Str("package", pkg).Str("stage", stage).Str("blockedBy", blocker).
					Msg("run script skipped: a dependency's script failed or was skipped")
				return
			}
		}

		cmd, ok := rel.Pkg.Space.RunScripts[strings.ToLower(name)]
		if !ok {
			a.log.Debug().Str("package", pkg).Str("space", rel.Pkg.Space.Name).
				Msgf("space does not define run script %q, skipping", name)
			return
		}
		log := a.log.With().Str("package", pkg).Str("stage", stage).Logger()
		log.Info().Msg("run script started")
		env := release.CommandEnv(pl, pkg, stage, a.log)
		if err := release.RunSequenceWithOutputs(ctx, runner, rel, rel.Pkg.Dir, stage, []string{cmd}, env, log, true); err != nil {
			res.failed = true
			log.Error().Err(err).Msg("run script failed")
			return
		}
		res.ran = true
	}

	// One class, one budget: the run command reuses the executor's pump.
	graph.Drain(sched,
		func(string) struct{} { return struct{}{} },
		func(struct{}) int { return a.cfg.BuildConcurrency },
		execute)

	ran, failed, skipped := 0, 0, 0
	for _, res := range results {
		switch {
		case res.failed:
			failed++
		case res.skipped:
			skipped++
		case res.ran:
			ran++
		}
	}
	a.log.Info().Str("script", name).Int("ran", ran).Int("failed", failed).
		Int("skipped", skipped).Msg("run finished")
	if failed > 0 {
		return fmt.Errorf("%d run script(s) failed", failed)
	}
	return nil
}
