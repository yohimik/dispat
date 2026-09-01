package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// runHooks executes the run-level hook sequences (beforeAll, postAll, the
// commit and push hooks) in the monorepo root with a shared environment: the
// workspace listing before the run, the run outcome (release.RunEnv) once the
// task graph has finished. beforeAll is the one gating hook — it runs before
// any release work, so failing it can still stop everything; every other run
// hook only warns on failure, because it runs after the work it observes:
// every command of its sequence runs even when an earlier one failed.
type runHooks struct {
	cfg    *config.File
	runner script.Runner
	root   string
	env    []string // WorkspaceEnv before the run, RunEnv after it
	log    zerolog.Logger
}

// run executes one warn-only hook. A hook with no configured scripts is a
// no-op.
func (h *runHooks) run(ctx context.Context, name string, refs []string) {
	_ = h.exec(ctx, name, refs, false)
}

// runGating executes one gating hook fail-fast; the first error is returned
// and aborts the run.
func (h *runHooks) runGating(ctx context.Context, name string, refs []string) error {
	return h.exec(ctx, name, refs, true)
}

func (h *runHooks) exec(ctx context.Context, name string, refs []string, failFast bool) error {
	commands := h.cfg.Commands(refs)
	if len(commands) == 0 {
		return nil
	}
	log := h.log.With().Str("stage", name).Logger()
	log.Debug().Msg("hook started")
	computed := append(append([]string{}, h.env...), "DISPAT_STAGE="+name)
	// A run hook executes at the repository root with no package in view, so
	// only the top-level static env applies: no space or package layer has a
	// package to belong to here.
	seq := release.Sequence{Runner: h.runner, Dir: h.root, Stage: name, Commands: commands,
		Env: release.StaticEnv(config.EnvPairs(h.cfg.Env), computed), Log: log, FailFast: failFast}
	return seq.Run(ctx)
}

// finalizer bundles what the finalize phase needs from the surrounding
// Release call — the resolved GitHub dispatch (empty when every package is
// disabled), the push remote and the run-level hooks — so finalize does not
// take each of them as a positional parameter.
type finalizer struct {
	gh     *ghDispatch
	remote string
	hooks  *runHooks
	// crit collects what fails in here. Nothing in the finalize phase may
	// abort it: every package it covers has already published.
	crit *criticals
	// skipHooks silences the bracket hooks: an interrupted run still records
	// what published (the commit, the tags, the push) but runs no more of the
	// operator's scripts.
	skipHooks bool
}

// run executes one warn-only bracket hook, unless the run was interrupted.
func (f finalizer) run(ctx context.Context, name string, refs []string) {
	if f.skipHooks {
		return
	}
	f.hooks.run(ctx, name, refs)
}

// finalize runs the end-of-run release-commit phase: one commit staging every
// published package's folder — created here, at the end of the run, right
// before tagging and the push — then tags on that commit, the push (when
// enabled), and the GitHub releases, created last so that with push enabled
// they reference commits and tags that already exist on the remote.
//
// The commit and push hooks bracket their phases — beforeCommit / afterCommit
// around the release commit, postCommit after commit and tags, beforePush /
// afterPush around the push. They are no-ops when the phase they bracket is
// disabled or nothing published, and the "after" hooks only run when the
// bracketed operation succeeded: a hook observing a commit or push that never
// happened would be reporting a lie.
// It returns nothing: every failure it meets is a critical, collected in
// fin.crit and reported by the caller once the whole run is finished.
func (a *App) finalize(ctx context.Context, fin finalizer, pl *plan.Plan, results map[string]*release.Result) {
	if !a.cfg.Commit.IsEnabled() {
		return
	}

	// Two lists, because the two consumers want different things. tags is what
	// this run released, one per package, and it is what the commit message
	// names. pushTags additionally carries the aliases, because a "v1" that
	// exists locally and not on the remote is not the pointer it is for.
	//
	// An alias has no business in the message: it is a moving ref rather than
	// a release, so listing it would make `chore(release): core@1.0.0, v1`
	// out of a release of exactly one thing, and the subject would change
	// shape the day somebody adds an alias to the config. `dispat commit`
	// renders its own message from the release tag alone; this keeps the two
	// spellings of the same placeholder in agreement.
	var pkgs, tags, pushTags, dirs []string
	var rels []*plan.Release
	for _, name := range pl.Order {
		if r, ok := results[name]; ok && r.Status == release.StatusPublished {
			rel := pl.Releases[name]
			pushTags = append(pushTags, rel.TagName())
			for _, alias := range rel.AliasTags() {
				pushTags = append(pushTags, alias.Name)
			}
			dirs = append(dirs, rel.Pkg.Dir)
			rels = append(rels, rel)
			// The commit message names the releases this commit records. A
			// package whose scripts exported PACKAGE_<KEY> made its own
			// commit already — its record is that commit, and naming it here
			// would claim a release the leg's commit already claims.
			if rel.ExportedCommit() == "" {
				pkgs = append(pkgs, name)
				tags = append(tags, rel.TagName())
			}
		}
	}
	if len(rels) == 0 {
		return
	}
	if len(pkgs) == 0 {
		// Every published package recorded itself; whatever this commit still
		// carries (shared include files, stray artifacts) belongs to the run
		// as a whole, so the message names the run's releases.
		for _, rel := range rels {
			pkgs = append(pkgs, rel.Pkg.Name)
			tags = append(tags, rel.TagName())
		}
	}
	dirs = a.appendIncludeDirs(dirs, a.cfg.Commit.Include)

	// Every package in this list has published. From here nothing may abort:
	// a failure is recorded and the phase carries on to the rest of what it
	// owes, because the alternative is a released package with no tag, no
	// changelog entry and no GitHub release, none of which the next run knows
	// to go back for. See critical.go.
	msg := renderCommitMessage(a.cfg.Commit.MessageFormat, pkgs, tags)
	fin.run(ctx, "beforeCommit", a.cfg.Run.BeforeCommit)
	committed, err := a.git.CommitDirs(ctx, dirs, msg)
	switch {
	case err != nil:
		// Tagging still follows: the tags then point at each package's
		// exported commit or at HEAD, which is where they would have pointed
		// had there been no release commit to make.
		fin.crit.record(a.log, plan.CodeCommitFailed, err, "release commit failed", nil)
	case committed:
		a.log.Info().Str("message", msg).Msg("created release commit")
		fin.run(ctx, "afterCommit", a.cfg.Run.AfterCommit)
	default:
		fin.run(ctx, "afterCommit", a.cfg.Run.AfterCommit)
	}
	for _, rel := range rels {
		// A package whose scripts exported PACKAGE_<KEY>=<commitHash> pins
		// its tag to that commit instead of the release commit.
		if err := release.CreateReleaseTag(ctx, a.git, rel, a.cfg.Commit.ForceEnabled(), a.log); err != nil {
			// One package's tag failing says nothing about the next one's.
			fin.crit.record(a.log, release.TagFailureCode(err), err, "tagging failed",
				func(e *zerolog.Event) *zerolog.Event {
					return e.Str("package", rel.Pkg.Name).Str("tag", rel.TagName())
				})
		}
	}
	fin.run(ctx, "postCommit", a.cfg.Run.PostCommit)
	// released is the commit the records name. It is HEAD, except after a
	// recovery: the branch tip is a merge by then, and what the records mean
	// is the release commit that became its first parent. Empty until a
	// recovery happens, so an ordinary run reads HEAD exactly as it always
	// did.
	var released string
	if a.cfg.Commit.PushEnabled() {
		fin.run(ctx, "beforePush", a.cfg.Run.BeforePush)
		report, err := a.git.Push(ctx, fin.remote, pushTags, a.cfg.Commit.ForceEnabled())
		if errors.Is(err, gitx.ErrRejected) {
			// Somebody pushed to the branch while this run was working. The
			// release still owes its commit and tags, and the way to deliver
			// them is to join what landed with what this run made.
			report, released, err = a.mergeAndPush(ctx, fin, tags, pushTags)
		}
		a.reportPush(report, fin.remote)
		if err != nil {
			// The commit and the tags are local records already; the remote
			// copy is what is missing, and a later push sends it. The GitHub
			// releases below still go out — they document the release, and
			// withholding them would lose the second record too.
			fin.crit.record(a.log, plan.CodePushFailed, err, "push failed",
				func(e *zerolog.Event) *zerolog.Event { return e.Str("remote", fin.remote) })
		} else {
			a.log.Info().Str("remote", fin.remote).Strs("tags", pushTags).Msg("pushed release commit and tags")
			fin.run(ctx, "afterPush", a.cfg.Run.AfterPush)
		}
	}
	if fin.gh != nil && !fin.gh.empty() {
		// The releases document the exact release commit and tag in their
		// body, whether or not they were pushed; with push enabled the tag is
		// additionally pinned to the commit via target_commitish (only then
		// does the SHA exist on the remote). Every resolved releaser gets the
		// stamp: the dispatch routes each package to one of them.
		sha, shaErr := released, error(nil)
		if sha == "" {
			sha, shaErr = a.git.HeadSHA(ctx)
		}
		if shaErr != nil {
			a.log.Warn().Err(shaErr).Msg("cannot resolve HEAD, github releases will omit the commit")
		} else {
			for _, gh := range fin.gh.all {
				gh.CommitSHA = sha
				if a.cfg.Commit.PushEnabled() {
					gh.TargetCommitish = sha
				}
			}
		}
		for _, rel := range rels {
			if err := fin.gh.Record(ctx, rel); err != nil {
				fin.crit.record(a.log, plan.CodeRecordFailed, err, "github release failed",
					func(e *zerolog.Event) *zerolog.Event { return e.Str("package", rel.Pkg.Name) })
			}
		}
	}
}

// mergeAndPush recovers a push the remote refused because the branch moved
// under this run, which is what commits landed by another clone look like from
// here.
//
// Nothing this run made is rewritten. The release commit keeps its identity
// and its tags keep naming it, so the tagged tree is still the one the release
// recorded: its changelog entries and its version rewrites are inside it, and
// a consumer resolving the tag gets what the release published. All that
// changes is the branch's tip, which becomes a merge of the release commit and
// what arrived, so both are on the branch.
//
// The commits that arrived were not in this run's plan and do not enter it.
// They are outside the tag's ancestry, which is exactly where they belong: the
// next run plans them and releases them on their own terms.
//
// The warning is the point of the whole recovery. The branch the release went
// out on is not the branch the run was planned against, and nobody reading
// afterwards should have to work that out from the commit graph.
//
// It answers the release commit as well as the push report, because the caller
// cannot read it off HEAD afterwards: HEAD is the merge by then, and the
// records this run still has to write are about the commit underneath it.
func (a *App) mergeAndPush(ctx context.Context, fin finalizer, tags,
	pushTags []string) (gitx.PushReport, string, error) {
	branch, err := a.git.CurrentBranch(ctx)
	if err != nil {
		return gitx.PushReport{}, "", err
	}
	if branch == "" {
		return gitx.PushReport{}, "", fmt.Errorf(
			"commits landed on %s during the release, and this is a detached HEAD with no branch to merge them into",
			fin.remote)
	}
	// Read before the merge, because afterwards HEAD is the merge itself and
	// the release commit is only its first parent.
	release, err := a.git.HeadSHA(ctx)
	if err != nil {
		return gitx.PushReport{}, "", err
	}
	if err := a.git.MergeRemote(ctx, fin.remote, branch, mergeMessage(fin.remote, branch, release, tags)); err != nil {
		return gitx.PushReport{}, release, fmt.Errorf(
			"commits landed on %s/%s during the release and could not be merged with it: %w",
			fin.remote, branch, err)
	}
	a.log.Warn().Str("code", plan.CodePushMerged).Str("remote", fin.remote).Str("branch", branch).
		Msg("pulled the branch during the release to sync changes that landed while it ran; " +
			"the release tags point at the tree that was planned and the release commit was merged on top")
	report, err := a.git.Push(ctx, fin.remote, pushTags, a.cfg.Commit.ForceEnabled())
	return report, release, err
}

// mergeMessage is what the recovery's merge commit says.
//
// A chore(release) subject on purpose. "release" is a nonPackageScope by
// default, the same exemption the release commit above it relies on, so the
// next run reads this commit as naming no package rather than as a change to
// every folder the merge touched. The body names the release commit and the
// tags, because a merge nobody can attribute is a merge nobody can audit.
func mergeMessage(remote, branch, release string, tags []string) string {
	short := release
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf(
		"chore(release): merge %s/%s into the release commit\n\n"+
			"Commits landed on %s/%s while the release ran, so the release\n"+
			"commit %s was merged with them rather than replaced. The tags this\n"+
			"release wrote (%s) still name that commit, so the commits\n"+
			"that arrived are outside the release and belong to the next run.\n",
		remote, branch, remote, branch, short, strings.Join(tags, ", "))
}

// reportPush logs what the push did about tags the remote already carried.
// Both push sites report it identically, because the distinction matters to
// whoever reads the log: a skipped tag means the remote kept what it had,
// a replaced one means this run overwrote a published ref.
func (a *App) reportPush(report gitx.PushReport, remote string) {
	for _, tag := range report.Skipped {
		a.log.Warn().Str("tag", tag).Str("remote", remote).
			Msg("tag already exists on the remote, skipped")
	}
	for _, tag := range report.Replaced {
		a.log.Warn().Str("tag", tag).Str("remote", remote).
			Msg("tag already existed on the remote and was overwritten")
	}
}

// appendIncludeDirs appends the commit.include paths onto the staging list:
// the shared artifacts regenerated outside every package folder (a workspace
// lockfile a syncLock rewrote, say) belong in the same release commit as the
// package folders. A configured path that does not exist is not staged —
// `git add` would refuse an empty pathspec — and is warned about (W227),
// because a typo'd path would otherwise cost the commit its artifact in
// silence, on every release, until a human noticed the file missing.
func (a *App) appendIncludeDirs(dirs []string, include []string) []string {
	for _, p := range include {
		full := filepath.Join(a.root, filepath.FromSlash(p))
		if _, err := os.Stat(full); err != nil {
			a.log.Warn().Str("code", plan.CodeCommitIncludeMissing).Str("path", p).
				Msg("commit.include path does not exist, not staged")
			continue
		}
		dirs = append(dirs, full)
	}
	return dirs
}

// defaultCommitMessageFormat is the release commit's message template when
// commit.messageFormat is unset. Its "release" scope is what nonPackageScopes
// exempts by default, so the commit does not poison the next run.
const defaultCommitMessageFormat = "chore(release): {tags}"

// renderCommitMessage substitutes {tags} and {packages} placeholders.
func renderCommitMessage(format string, pkgs, tags []string) string {
	if format == "" {
		format = defaultCommitMessageFormat
	}
	msg := strings.ReplaceAll(format, "{tags}", strings.Join(tags, ", "))
	return strings.ReplaceAll(msg, "{packages}", strings.Join(pkgs, ", "))
}
