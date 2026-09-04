package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
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
			report, released, err = a.mergeAndPush(ctx, fin, rels, tags, pushTags)
		}
		a.reportPush(report, fin.remote)
		if err != nil {
			// The commit and the tags are local records already; the remote
			// copy is what is missing, and a later push sends it. The GitHub
			// releases below still go out — they document the release, and
			// withholding them would lose the second record too.
			fin.crit.record(a.log, plan.CodePushFailed, err, "push failed",
				func(e *zerolog.Event) *zerolog.Event { return e.Str("remote", gitx.RedactURL(fin.remote)) })
		} else {
			a.log.Info().Str("remote", gitx.RedactURL(fin.remote)).Strs("tags", pushTags).Msg("pushed release commit and tags")
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
// The merge and the push are attempted up to mergeAttempts times, because the
// window this recovers from is still open while it recovers: another clone can
// land a commit between the fetch and the retry, and answering that with a
// failed release would be the very outcome this exists to prevent. The loop is
// here rather than at the caller because the release commit has to be read
// once, before the first merge; afterwards HEAD is a merge and reading it
// again would name the wrong commit.
//
// The warning is the point of the whole recovery. The branch the release went
// out on is not the branch the run was planned against, and nobody reading
// afterwards should have to work that out from the commit graph.
//
// It answers the release commit as well as the push report, because the caller
// cannot read it off HEAD afterwards: HEAD is the merge by then, and the
// records this run still has to write are about the commit underneath it.
func (a *App) mergeAndPush(ctx context.Context, fin finalizer, rels []*plan.Release,
	tags, pushTags []string) (gitx.PushReport, string, error) {
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
	if err := a.refuseRepublishing(ctx, fin.remote, rels); err != nil {
		return gitx.PushReport{}, release, err
	}

	var report gitx.PushReport
	for attempt := 1; ; attempt++ {
		err := a.git.MergeRemote(ctx, fin.remote, branch, mergeMessage(fin.remote, branch, release, tags))
		var conflict *gitx.MergeConflict
		switch {
		case errors.As(err, &conflict):
			if err := a.settleConflict(ctx, fin, rels, branch, conflict.Paths); err != nil {
				return report, release, err
			}
		case err != nil:
			return report, release, fmt.Errorf(
				"commits landed on %s/%s during the release and could not be merged with it: %w",
				fin.remote, branch, err)
		}
		a.log.Warn().Str("code", plan.CodePushMerged).Str("remote", gitx.RedactURL(fin.remote)).Str("branch", branch).
			Int("attempt", attempt).
			Msg("pulled the branch during the release to sync changes that landed while it ran; " +
				"the release tags point at the tree that was planned and the release commit was merged on top")
		report, err = a.git.Push(ctx, fin.remote, pushTags, a.cfg.Commit.ForceEnabled())
		if !errors.Is(err, gitx.ErrRejected) || attempt >= mergeAttempts {
			return report, release, err
		}
		// Somebody landed another commit while this was merging the last one.
		// Round again, on the tip that now exists.
		a.log.Debug().Str("remote", gitx.RedactURL(fin.remote)).Str("branch", branch).Int("attempt", attempt).
			Msg("the branch moved again during the recovery; merging what arrived and pushing once more")
	}
}

// settleConflict finishes a recovery merge that stopped on content, so that a
// release which has already published still reaches the remote.
//
// Three things happen, and none of them may be skipped. This side of every
// conflicting path wins, because this side is the release: a tree that was
// planned, built, published and tagged, and the tag already names it, so
// taking the other side would publish content the release never saw. The
// other side is pushed to a branch of its own, so the work somebody else did
// stays readable rather than being quietly dropped on the floor. And both
// records say so, naming the files and that branch, because the one thing
// worse than a conflict is a conflict nobody was told about.
//
// What is left is a job for a person: two versions of the same content exist,
// one on the branch and one on the quarantine branch, and no rule dispat could
// follow decides which of them should survive.
//
// The note reaches the changelog through the merge commit, never through the
// release commit: that one is tagged, and a tag whose commit is amended is a
// tag that names nothing. The tagged tree therefore carries the entry without
// the note, which the documentation says out loud.
func (a *App) settleConflict(ctx context.Context, fin finalizer, rels []*plan.Release,
	branch string, paths []string) error {
	quarantine := conflictBranch(rels, time.Now())
	if err := a.git.ResolveOurs(ctx, paths); err != nil {
		return fmt.Errorf("settling the merge of %s/%s: %w", fin.remote, branch, err)
	}
	// Pushed before the merge is committed, so a name that cannot be taken
	// stops the run while the tree is still the one it can explain.
	if err := a.git.PushBranchAt(ctx, fin.remote, "FETCH_HEAD", quarantine); err != nil {
		return fmt.Errorf(
			"commits landed on %s/%s during the release and conflicted with it, and the branch "+
				"that would have kept them could not be pushed: %w", fin.remote, branch, err)
	}
	note := conflictNote(fin.remote, quarantine, paths)
	for _, rel := range rels {
		path, noted, err := changelog.NoteEntry(rel, note)
		if err != nil {
			return fmt.Errorf("noting the conflict in %s's changelog: %w", rel.Pkg.Name, err)
		}
		if !noted {
			continue
		}
		if err := a.git.StageFile(ctx, path); err != nil {
			return fmt.Errorf("staging %s: %w", path, err)
		}
	}
	if err := a.git.CommitMerge(ctx); err != nil {
		return fmt.Errorf("committing the settled merge of %s/%s: %w", fin.remote, branch, err)
	}
	// The GitHub releases are created after the push, so they can still carry
	// it; the changelog could not wait, because it has to be in the tree the
	// merge commits.
	if fin.gh != nil {
		for _, gh := range fin.gh.all {
			gh.Note = "### Note\n\n" + note + "\n"
		}
	}
	a.log.Warn().Str("code", plan.CodePushConflicted).Str("remote", gitx.RedactURL(fin.remote)).Str("branch", branch).
		Strs("paths", paths).Str("keptAt", quarantine).
		Msg("commits landed on the branch during the release and changed the same content; " +
			"this release's side was kept and theirs was pushed to a branch of its own to be reconciled")
	return nil
}

// conflictNote is the sentence both records carry.
func conflictNote(remote, quarantine string, paths []string) string {
	return fmt.Sprintf(
		"Commits landed on %s while this release ran and changed %s, which this release "+
			"changed too. This release's version of those files is what was published; the other "+
			"side is kept on the branch %s, to be reconciled.",
		remote, strings.Join(paths, ", "), quarantine)
}

// conflictBranch names the branch the other side of a conflict is kept on:
// what this leg released, then when, under a prefix that says what the branch
// is for.
//
// Deterministic in its releases, which is what makes it readable, and unique
// in its timestamp, which is what makes it safe. The releases are taken in the
// leg's own order rather than sorted, so the name reads the way the release
// itself does.
func conflictBranch(rels []*plan.Release, now time.Time) string {
	parts := make([]string, 0, len(rels)*2+1)
	for _, rel := range rels {
		parts = append(parts, rel.Pkg.Name, rel.Next.String())
	}
	parts = append(parts, now.UTC().Format("20060102-150405"))
	name := "release-conflicts/" + strings.Join(parts, "-")
	if err := gitx.ValidRefName(name); err != nil {
		// A package named something git will not take in a ref. The timestamp
		// alone still identifies the run, and a branch nobody can push is
		// worse than one nobody can guess.
		return "release-conflicts/" + now.UTC().Format("20060102-150405")
	}
	return name
}

// mergeAttempts bounds the recovery. Each round costs a fetch, a merge and a
// push, so a branch busy enough to lose three of them in a row is one where
// stopping and saying so beats spinning: what fails then is the push, with the
// release commit and its tags still local, which is the outcome the recovery
// was already prepared to report.
const mergeAttempts = 3

// refuseRepublishing stops the recovery when the remote already carries a
// release tag this run is about to push.
//
// The recovery pushes the same tags again, and commit.force defaults on, so
// without this a checkout whose tags are stale enough to have re-planned an
// already published version would force-move that published tag onto its own
// commit. The push this recovers from never sent a tag at all, which is why
// the hazard is new: the branch is refused before the tag refs are reached.
//
// The release tags come from the releases rather than from the commit
// message's list, which leaves out any package that recorded itself through an
// exported commit. Aliases are deliberately not checked: a moving alias exists
// to be moved, and moving it is what every release does.
func (a *App) refuseRepublishing(ctx context.Context, remote string, rels []*plan.Release) error {
	existing, err := a.git.RemoteTags(ctx, remote)
	if err != nil {
		return fmt.Errorf("reading %s's tags before pushing the release again: %w", remote, err)
	}
	for _, rel := range rels {
		tag := rel.TagName()
		if existing[tag] {
			return fmt.Errorf(
				"commits landed on %s during the release, and %s already carries %s: "+
					"this checkout planned a version that is already published, "+
					"so pushing again would move a released tag. Pull and run again",
				remote, remote, tag)
		}
	}
	return nil
}

// mergeMessage is what the recovery's merge commit says.
//
// A chore(release) subject on purpose. "release" is a nonPackageScope by
// default, the same exemption the release commit above it relies on, so the
// next run reads this commit as naming no package rather than as a change to
// every folder the merge touched. The body names the release commit and the
// tags, because a merge nobody can attribute is a merge nobody can audit.
func mergeMessage(remote, branch, release string, tags []string) string {
	short := shortCommit(release)
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
		a.log.Warn().Str("tag", tag).Str("remote", gitx.RedactURL(remote)).
			Msg("tag already exists on the remote, skipped")
	}
	for _, tag := range report.Replaced {
		a.log.Warn().Str("tag", tag).Str("remote", gitx.RedactURL(remote)).
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
