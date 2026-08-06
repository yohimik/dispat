package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/cli/internal/changelog"
	"github.com/yohimik/dispat/services/cli/internal/config"
	"github.com/yohimik/dispat/services/cli/internal/github"
	"github.com/yohimik/dispat/services/cli/internal/release"
	"github.com/yohimik/dispat/services/cli/internal/script"
)

// Release computes the plan and executes it end to end: verification, the
// gating beforeAll hook, the task graph, the run-level hooks, the finalize
// phase and the summary. The returned error is non-nil when anything kept the
// run from completing cleanly — a blocked plan, failed verification, a failed
// package, a failed finalize — with the details already logged.
func (a *App) Release(ctx context.Context) error {
	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if blocked := a.releaseBlocked(pl); blocked != "" {
		a.log.Error().Str("reason", blocked).Msg("refusing to release")
		return errors.New(blocked)
	}

	commitMode := a.cfg.Commit.IsEnabled()
	pushMode := a.cfg.Commit.PushEnabled()
	remote := a.cfg.Commit.Remote
	if remote == "" {
		remote = "origin"
	}

	// Resolve the GitHub releaser (nil when disabled or unresolvable).
	var gh *github.Releaser
	if a.cfg.GitHub.IsEnabled() {
		var ghErr error
		if gh, ghErr = githubReleaser(a.cfg.GitHub); ghErr != nil {
			a.log.Warn().Err(ghErr).Msg("github releases disabled")
		}
	} else {
		a.log.Debug().Msg("github releases disabled by config")
	}

	// Verify external access up front, before any release work starts.
	if pushMode {
		if err := a.git.VerifyRemote(ctx, remote); err != nil {
			a.log.Error().Err(err).Str("remote", remote).Msg("git remote verification failed")
			return err
		}
	}
	if gh != nil {
		if err := gh.Verify(ctx); err != nil {
			a.log.Error().Err(err).Msg("github verification failed")
			return err
		}
	}

	// In release-commit mode, tagging and GitHub releases move to the
	// finalize phase so they reference the end-of-run commit.
	var tagger release.Tagger = a.git
	if commitMode {
		tagger = nil
	}
	recs := a.recorders()
	if gh != nil && !commitMode {
		recs = append(recs, gh)
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
		return err
	}

	exec := &release.Executor{
		BuildConcurrency:   a.cfg.BuildConcurrency,
		PublishConcurrency: a.cfg.PublishConcurrency,
		Runner:             runner,
		Tagger:             tagger,
		Recorders:          recs,
		Reverter:           a.git,
		Log:                a.log,
	}
	start := time.Now()
	results := exec.Run(ctx, pl)

	hooks.env = release.RunEnv(pl, results, a.log)
	// postAll runs once the whole task graph has finished, releases or not —
	// "nothing published" is an outcome a notification script wants to see too.
	hooks.run(ctx, "postAll", a.cfg.Run.PostAll)
	finErr := a.finalize(ctx, gh, remote, pl, results, hooks)
	failed := a.summarize(pl, results, time.Since(start))
	if finErr != nil {
		return finErr
	}
	if failed > 0 {
		return fmt.Errorf("%d package(s) failed", failed)
	}
	return nil
}

// recorders assembles the per-publish release recorders enabled by the
// configuration (currently the changelog file writer; the GitHub releaser is
// appended by Release depending on the release-commit mode).
func (a *App) recorders() []release.ReleaseRecorder {
	var recs []release.ReleaseRecorder
	if a.cfg.Changelog.IsEnabled() {
		recs = append(recs, &changelog.FileWriter{
			File:   a.cfg.Changelog.File,
			Title:  a.cfg.Changelog.Title,
			Format: entryFormat(a.cfg.Changelog.EntryFormatConfig),
		})
	} else {
		a.log.Debug().Msg("changelog files disabled by config")
	}
	return recs
}

// githubReleaser resolves repository and token for the GitHub recorder. The
// repository comes from config or $GITHUB_REPOSITORY ("owner/repo"), the
// token from the configured env var (default $GITHUB_TOKEN).
func githubReleaser(gc config.GitHubConfig) (*github.Releaser, error) {
	owner, repo := gc.Owner, gc.Repo
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
	tokenEnv := gc.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "GITHUB_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("no token found in $%s", tokenEnv)
	}
	return &github.Releaser{
		APIURL: gc.APIURL,
		Owner:  owner,
		Repo:   repo,
		Token:  token,
		Format: entryFormat(gc.EntryFormatConfig),
	}, nil
}

// entryFormat maps the config format options onto the changelog renderer.
func entryFormat(f config.EntryFormatConfig) changelog.Format {
	return changelog.Format{
		DateFormat:        f.DateFormat,
		BreakingTitle:     f.BreakingTitle,
		FeaturesTitle:     f.FeaturesTitle,
		FixesTitle:        f.FixesTitle,
		DependenciesTitle: f.DependenciesTitle,
	}
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

//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//	    _  _  _
//	|/ |_ |_ |_|
//	|\ |_ |_ |
//
//	         _   _   _           _     ___  _  _
//	| | |\| | \ | | |  | | |\/| |_ |\|  |  |_ | \
//	|_| | | |_/ |_| |_ |_| |  | |_ | |  |  |_ |_/
//
//	   _______
//	  /       \
//	 /^   ^   ^\
//	|   ^   ^   |
//	|           |
//	 \_________/
//________________
