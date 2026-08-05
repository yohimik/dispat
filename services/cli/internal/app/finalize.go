package app

import (
	"context"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/cli/internal/config"
	"github.com/yohimik/dispat/services/cli/internal/github"
	"github.com/yohimik/dispat/services/cli/internal/plan"
	"github.com/yohimik/dispat/services/cli/internal/release"
	"github.com/yohimik/dispat/services/cli/internal/script"
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
	env := append(append([]string{}, h.env...), "DISPAT_STAGE="+name)
	return release.RunSequence(ctx, h.runner, h.root, name, commands, env, log, failFast)
}

// finalize runs the end-of-run release-commit phase: one commit staging every
// published package's folder, tags on that commit, the push (when enabled),
// and the GitHub releases — created last, so that with push enabled they
// reference commits and tags that already exist on the remote.
//
// The commit and push hooks bracket their phases — beforeCommit / afterCommit
// around the release commit, postCommit after commit and tags, beforePush /
// afterPush around the push. They are no-ops when the phase they bracket is
// disabled or nothing published, and the "after" hooks only run when the
// bracketed operation succeeded: a hook observing a commit or push that never
// happened would be reporting a lie.
func (a *App) finalize(ctx context.Context, gh *github.Releaser, remote string, pl *plan.Plan, results map[string]*release.Result, hooks *runHooks) error {
	if !a.cfg.Commit.IsEnabled() {
		return nil
	}

	var pkgs, tags, dirs []string
	var rels []*plan.Release
	for _, name := range pl.Order {
		if r, ok := results[name]; ok && r.Status == release.StatusPublished {
			rel := pl.Releases[name]
			pkgs = append(pkgs, name)
			tags = append(tags, rel.TagName())
			dirs = append(dirs, rel.Pkg.Dir)
			rels = append(rels, rel)
		}
	}
	if len(pkgs) == 0 {
		return nil
	}

	msg := renderCommitMessage(a.cfg.Commit.MessageFormat, pkgs, tags)
	hooks.run(ctx, "beforeCommit", a.cfg.Run.BeforeCommit)
	committed, err := a.git.CommitDirs(ctx, dirs, msg)
	if err != nil {
		a.log.Error().Err(err).Msg("release commit failed")
		return err
	}
	if committed {
		a.log.Info().Str("message", msg).Msg("created release commit")
	}
	hooks.run(ctx, "afterCommit", a.cfg.Run.AfterCommit)
	for _, tag := range tags {
		if err := a.git.CreateTag(ctx, tag, "release "+tag); err != nil {
			a.log.Error().Err(err).Str("tag", tag).Msg("tagging failed")
			return err
		}
	}
	hooks.run(ctx, "postCommit", a.cfg.Run.PostCommit)
	if a.cfg.Commit.PushEnabled() {
		hooks.run(ctx, "beforePush", a.cfg.Run.BeforePush)
		if err := a.git.Push(ctx, remote); err != nil { // branch + tags
			a.log.Error().Err(err).Str("remote", remote).Msg("push failed")
			return err
		}
		a.log.Info().Str("remote", remote).Strs("tags", tags).Msg("pushed release commit and tags")
		hooks.run(ctx, "afterPush", a.cfg.Run.AfterPush)
	}
	if gh != nil {
		// The releases document the exact release commit and tag in their
		// body, whether or not they were pushed; with push enabled the tag is
		// additionally pinned to the commit via target_commitish (only then
		// does the SHA exist on the remote).
		if sha, err := a.git.HeadSHA(ctx); err != nil {
			a.log.Warn().Err(err).Msg("cannot resolve HEAD, github releases will omit the commit")
		} else {
			gh.CommitSHA = sha
			if a.cfg.Commit.PushEnabled() {
				gh.TargetCommitish = sha
			}
		}
		for _, rel := range rels {
			if err := gh.Record(ctx, rel); err != nil {
				a.log.Error().Err(err).Str("package", rel.Pkg.Name).Msg("github release failed")
				return err
			}
		}
	}
	return nil
}

// renderCommitMessage substitutes {tags} and {packages} placeholders.
func renderCommitMessage(format string, pkgs, tags []string) string {
	if format == "" {
		format = "chore(release): {tags}"
	}
	msg := strings.ReplaceAll(format, "{tags}", strings.Join(tags, ", "))
	return strings.ReplaceAll(msg, "{packages}", strings.Join(pkgs, ", "))
}
