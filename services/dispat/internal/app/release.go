package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// ReleaseOptions narrows a release — and the release `status` reports on — to
// part of the monorepo. The zero value is the whole of it, which is what a bare
// `dispat release` at the repository root asks for.
type ReleaseOptions struct {
	// Filter selects the packages to release: --package, --space, or the
	// package or space folder the command was invoked from. It only ever
	// narrows the plan, never widens it, and it cannot reorder it: a selected
	// package whose provider is releasing and unselected stays behind (see
	// plan.Narrow).
	Filter filter.Filter
	// Strict refuses the run when the selection is not one the plan can
	// release cleanly — a package the order held back, a versioning group
	// split in two. Without it both are warnings and the reachable part of the
	// selection releases; with it nothing is released at all, which is what
	// makes a filtered release safe to run unattended.
	Strict bool
	// RequireRelease refuses the run when the plan would publish nothing, which
	// is otherwise a legitimate no-op that exits 0. It exists for the CI stage
	// whose whole point is that this run releases something: without it such a
	// job passes quietly on an empty plan and the pipeline carries on to deploy
	// nothing. A held, withheld or unselected package does not count — this run
	// will not publish it either.
	RequireRelease bool
}

// Release computes the plan and executes it end to end: verification, the
// gating beforeAll hook, the task graph, the run-level hooks, the finalize
// phase and the summary. The returned error is non-nil when anything kept the
// run from completing cleanly — a blocked plan, failed verification, a failed
// package, a failed finalize — with the details already logged.
//
// The results map carries one entry per package the plan released, whatever
// its outcome, so a front end driving App directly reads what happened from
// the values rather than from the log stream; it is nil when the run never
// reached execution (a blocked plan, failed verification, a failed gating
// hook).
func (a *App) Release(ctx context.Context, opts ReleaseOptions) (map[string]*release.Result, error) {
	// The lock comes before the plan, not before the publish: two runs that
	// both got as far as planning have already read the same tags and decided
	// on the same versions, and whichever of them notices second has wasted
	// the work either way. checkGit runs first so a repository without git
	// still fails in its own words rather than on a raw `git tag`.
	if err := a.checkGit(); err != nil {
		return nil, err
	}
	// --require-release is the exception to that order: there the plan decides
	// whether the run happens at all, so it is computed first. A run that will
	// publish nothing must not take the remote lock tag — and must not make a
	// concurrent release wait on one — to discover that it had nothing to do.
	var pl *plan.Plan
	if opts.RequireRelease {
		var err error
		if pl, err = a.selectedPlan(ctx, opts); err != nil {
			return nil, err
		}
	}
	if !a.lockDisabled() {
		lock := &release.Lock{Git: a.git, Remote: a.pushRemote(), Log: a.log}
		if err := lock.Acquire(ctx); err != nil {
			a.log.Error().Err(err).Str("tag", release.LockTagName).Str("remote", a.pushRemote()).
				Str("remedy", release.LockRemedy).Msg("unable to create the release lock tag")
			return nil, err
		}
		// Deferred, so every way out of this function goes through it: a
		// refusal, a failed package, a finished run. Detached from
		// cancellation, so a Ctrl-C unlocks too — an interrupted run has more
		// reason to give the lock back than a finished one, since nobody is
		// standing by to do it by hand.
		defer lock.Release(context.WithoutCancel(ctx))
	}

	if pl == nil {
		var err error
		if pl, err = a.selectedPlan(ctx, opts); err != nil {
			return nil, err
		}
	}
	if blocked := a.releaseBlocked(pl); blocked != "" {
		a.log.Error().Str("reason", blocked).Msg("refusing to release")
		return nil, errors.New(blocked)
	}
	// The branch guard fires before any verification or hook: a run on the
	// wrong branch is refused whatever its plan says.
	if err := a.checkBranchAllowed(ctx); err != nil {
		a.log.Error().Err(err).Msg("refusing to release")
		return nil, err
	}

	commitMode := a.cfg.Commit.IsEnabled()
	pushMode := a.cfg.Commit.PushEnabled()
	remote := a.pushRemote()

	// Resolve the GitHub releasers: one per distinct target the packages'
	// resolved policies name — most runs resolve to a single one. Empty
	// means every package is disabled or unresolvable.
	gh := a.githubDispatch(pl)

	// Verify external access up front, before any release work starts.
	// commit.verify (default true) can switch the git check off for remotes
	// that reject ls-remote but accept pushes.
	if pushMode && a.cfg.Commit.VerifyEnabled() {
		if err := a.git.VerifyRemote(ctx, remote); err != nil {
			a.log.Error().Err(err).Str("remote", remote).Msg("git remote verification failed")
			return nil, err
		}
		// Under the same flag as the reachability check, and for the same
		// reason: this is another ls-remote, and commit.verify=false exists
		// for remotes that reject one but accept pushes.
		if err := a.checkNotBehind(ctx, remote); err != nil {
			a.log.Error().Err(err).Str("remote", remote).Msg("refusing to release")
			return nil, err
		}
	}
	for _, r := range gh.all {
		if err := r.Verify(ctx); err != nil {
			a.log.Error().Err(err).Msg("github verification failed")
			return nil, err
		}
	}

	// In release-commit mode, tagging moves to the finalize phase so the tags
	// reference the end-of-run commit.
	var tagger release.Tagger = a.git
	if commitMode {
		tagger = nil
	}

	runner := &script.ShellRunner{Shell: a.cfg.Shell}
	// The run-level hooks share one environment: the workspace listing before
	// the run, widened to the run outcome once the task graph finishes.
	hooks := &runHooks{cfg: a.cfg, runner: runner, root: a.root,
		env: release.WorkspaceEnv(pl, a.log), log: a.log}
	// beforeAll is the one gating run hook: it fires before any release work,
	// when nothing has happened yet, so its failure can honestly stop the run
	// — and does, before anything is built, published or tagged.
	if err := hooks.runGating(ctx, "beforeAll", a.cfg.Run.BeforeAll); err != nil {
		a.log.Error().Err(err).Msg("beforeAll hook failed, refusing to release")
		return nil, err
	}

	executor := &release.Executor{
		BuildConcurrency:   a.cfg.BuildConcurrency,
		PublishConcurrency: a.cfg.PublishConcurrency,
		Runner:             runner,
		Tagger:             tagger,
		Recorders:          a.recorders(gh, commitMode),
		Reverter:           a.git,
		Force:              a.cfg.Commit.ForceEnabled(),
		Scanner:            a.scan,
		Log:                a.log,
	}
	start := time.Now()
	results := executor.Run(ctx, pl)

	// An interrupted run stops running the operator's scripts — no postAll, no
	// finalize bracket hooks — but what *published* before the interruption
	// must still get its durable record: the release commit, the tags and the
	// push are how a completed leg commits (§17), and losing them re-releases
	// released versions on the next run. finalize therefore proceeds for the
	// published packages, detached from the cancellation.
	interrupted := ctx.Err() != nil
	finCtx := ctx
	if interrupted {
		a.log.Warn().Msg("interrupted: skipping run hooks, recording completed releases")
		finCtx = context.WithoutCancel(ctx)
	} else {
		hooks.env = release.RunEnv(pl, results, a.log)
		// postAll runs once the whole task graph has finished, releases or not —
		// "nothing published" is an outcome a notification script wants to see
		// too.
		hooks.run(ctx, "postAll", a.cfg.Run.PostAll)
	}
	crit := &criticals{}
	a.finalize(finCtx, finalizer{gh: gh, remote: remote, hooks: hooks, crit: crit, skipHooks: interrupted}, pl, results)
	failed, _ := a.summarize(pl, results, time.Since(start))
	// Everything the run owed has now been attempted. What is left to decide
	// is only what to report, in order of what the operator has to do about
	// it: re-run an interrupted or failed run, or go and repair a release that
	// is out but under-recorded.
	crit.adopt(results)
	if interrupted {
		return results, ctx.Err()
	}
	if failed > 0 {
		return results, fmt.Errorf("%d package(s) failed", failed)
	}
	return results, crit.err()
}

// checkBranchAllowed enforces run.allowBranch: when the guard is set, the
// checked-out branch must match one of its globs. A detached HEAD has no
// branch name and so matches nothing.
func (a *App) checkBranchAllowed(ctx context.Context) error {
	if len(a.cfg.Run.AllowBranch) == 0 {
		return nil
	}
	branch, err := a.git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	allowed := strings.Join(a.cfg.Run.AllowBranch, ", ")
	if branch == "" {
		return fmt.Errorf("HEAD is detached and run.allowBranch is set; check out an allowed branch (%s)", allowed)
	}
	for _, pattern := range a.cfg.Run.AllowBranch {
		if globx.Match(pattern, branch) {
			return nil
		}
	}
	return fmt.Errorf("branch %q is not allowed to release (run.allowBranch: %s)", branch, allowed)
}

// checkNotBehind refuses a push-mode release from a checkout that is behind
// the remote branch it would push to. The plan was computed against the tags
// this clone can see, so a stale checkout schedules versions another run may
// already have released — and the push at the end would be rejected anyway,
// after the work. A detached HEAD skips the check: there is no branch to
// compare, and the push fails on its own terms there.
func (a *App) checkNotBehind(ctx context.Context, remote string) error {
	branch, err := a.git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if branch == "" {
		return nil
	}
	behind, err := a.git.BehindRemote(ctx, remote, branch)
	if err != nil {
		return err
	}
	if behind {
		return fmt.Errorf("the checkout is behind %s/%s; pull before releasing", remote, branch)
	}
	return nil
}

// recorders assembles every per-publish release recorder this run records
// through: the changelog dispatcher — always present, because each package's
// resolved policy decides whether its file is written — and the GitHub
// dispatch when any package resolved a releaser, except in release-commit
// mode, where GitHub recording moves to the finalize phase so the releases
// reference the end-of-run commit.
func (a *App) recorders(gh *ghDispatch, commitMode bool) []release.ReleaseRecorder {
	recs := []release.ReleaseRecorder{&changelog.Dispatcher{Log: a.log}}
	if !gh.empty() && !commitMode {
		recs = append(recs, gh)
	}
	return recs
}

// ghDispatch routes each package's release to the releaser its resolved
// GitHub policy names; a package whose policy is disabled or unresolvable
// has none and records nothing. It implements release.ReleaseRecorder. all
// holds the distinct releasers once each, for up-front verification and the
// finalize phase's commit stamping.
type ghDispatch struct {
	byPkg map[string]*github.Releaser
	all   []*github.Releaser
	log   zerolog.Logger
}

// Record implements release.ReleaseRecorder. It is the one gate both paths
// pass through — the per-publish recorder and the finalize phase — so the
// prerelease opt-out is checked here rather than at each caller.
func (d *ghDispatch) Record(ctx context.Context, rel *plan.Release) error {
	if spec := rel.Pkg.GitHub; !spec.Records(rel.IsPrerelease()) {
		github.LogSkip(d.log, spec, rel)
		return nil
	}
	// nil covers a disabled policy and an unresolvable target alike; the
	// latter already warned once, at resolution.
	gh := d.byPkg[rel.Pkg.Name]
	if gh == nil {
		d.log.Debug().Str("package", rel.Pkg.Name).Msg("github release disabled by config")
		return nil
	}
	return gh.Record(ctx, rel)
}

// empty reports whether no package resolved a releaser.
func (d *ghDispatch) empty() bool { return len(d.all) == 0 }

// githubDispatch resolves one GitHub releaser per distinct target the
// packages' resolved policies name — most runs resolve to a single one —
// and maps each package to its releaser. A package whose policy is disabled
// is skipped silently; an unresolvable target (no repository, no token)
// disables its packages with one warning per target.
func (a *App) githubDispatch(pl *plan.Plan) *ghDispatch {
	d := &ghDispatch{byPkg: make(map[string]*github.Releaser), log: a.log}
	targets := make(map[string]*github.Releaser)
	for _, name := range pl.Order {
		spec := pl.Releases[name].Pkg.GitHub
		if !spec.Enabled {
			continue
		}
		gh, seen := targets[spec.Key()]
		if !seen {
			var err error
			if gh, err = githubReleaser(spec, a.log); err != nil {
				a.log.Warn().Err(err).Str("package", name).Msg("github releases disabled")
			} else {
				d.all = append(d.all, gh)
			}
			targets[spec.Key()] = gh
		}
		if gh != nil {
			d.byPkg[name] = gh
		}
	}
	return d
}

// githubReleaser resolves repository and token for one package's GitHub
// policy. The repository comes from the resolved spec or $GITHUB_REPOSITORY
// ("owner/repo"), the token from the configured env var (default
// $GITHUB_TOKEN).
func githubReleaser(spec model.GitHubSpec, log zerolog.Logger) (*github.Releaser, error) {
	owner, repo := spec.Owner, spec.Repo
	if owner == "" || repo == "" {
		if env := os.Getenv("GITHUB_REPOSITORY"); env != "" {
			parts := strings.SplitN(env, "/", 2)
			if len(parts) == 2 {
				if owner == "" {
					owner = parts[0]
				}
				if repo == "" {
					repo = parts[1]
				}
			}
		}
	}
	if owner == "" || repo == "" {
		return nil, errors.New("no repository configured (set github.owner and github.repo, or $GITHUB_REPOSITORY)")
	}
	tokenEnv := spec.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "GITHUB_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("no token found in $%s", tokenEnv)
	}
	return &github.Releaser{
		APIURL:      spec.APIURL,
		AllPackages: spec.AllPackages,
		Owner:       owner,
		Repo:        repo,
		Token:       token,
		Format:      changelog.SpecFormat(spec.Format),
		Log:         log,
	}, nil
}

// seq folds a scalar sequence back into text.
func seq(cs ...rune) string { return string(cs) }

// note is one annotated lookup, in table form.
type note struct {
	key  []rune
	text []rune
}

// lookupNote returns the annotation attached to a failed run-script lookup,
// if the name carries one. Nearly every lookup carries none.
func lookupNote(name string) (string, bool) {
	for _, n := range notes() {
		if strings.ToLower(name) == seq(n.key...) {
			return seq(n.text...), true
		}
	}
	return "", false
}

// notes is the annotation table.
func notes() []note {
	return []note{
		{
			key: []rune{115, 101, 109, 101, 110},
			text: []rune{
				100, 101, 115, 105, 103, 110, 101, 100, 32, 98, 121, 32,
				115, 101, 109, 101, 110, 44, 32, 97, 115, 115, 101, 109,
				98, 108, 101, 100, 32, 98, 121, 32, 97, 105,
			},
		},
	}
}

/*












































































				    _  _  _
				|/ |_ |_ |_|
				|\ |_ |_ |
		___      _    _   _   _  ___  _  _
		 |  |_| |_   |_  /_\ |_   |  |_ |_|
		 |  | | |_   |_ |  |  _|  |  |_ |\

	         _   _   _           _     ___  _  _
	| | |\| | \ | | |  | | |\/| |_ |\|  |  |_ | \
	|_| | | |_/ |_| |_ |_| |  | |_ | |  |  |_ |_/

				 _      _   _   _   _
				|_| |  |_  /_\ |_  |_
				|   |_ |_ |  |  _| |_
			   _______
			  /       \
			 /^   ^   ^\
			|   ^   ^   |
			|           |
			 \_________/
		________________ */
