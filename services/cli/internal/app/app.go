// Package app is the application: it plans and releases the monorepo. The two
// operations a user can ask for — see the plan, run the plan — are methods on
// App, callable with nothing but a configuration and a logger, so the cli
// package stays a thin command-line controller (flags in, exit code out) and
// any other front end could drive the same struct.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/cli/internal/changelog"
	"github.com/yohimik/dispat/services/cli/internal/config"
	"github.com/yohimik/dispat/services/cli/internal/github"
	"github.com/yohimik/dispat/services/cli/internal/gitx"
	"github.com/yohimik/dispat/services/cli/internal/model"
	"github.com/yohimik/dispat/services/cli/internal/plan"
	"github.com/yohimik/dispat/services/cli/internal/release"
	"github.com/yohimik/dispat/services/cli/internal/script"
)

// App holds everything one run needs: the monorepo root, its validated
// configuration, the logger the run reports through, and the git client.
type App struct {
	root string
	cfg  *config.File
	log  zerolog.Logger
	git  *gitx.CLI
}

// New assembles an App for one monorepo.
func New(root string, cfg *config.File, log zerolog.Logger) *App {
	return &App{root: root, cfg: cfg, log: log, git: &gitx.CLI{Dir: root}}
}

// Status computes the plan and reports it — diagnostics, then the full graph
// with versions and channel transitions — without executing, tagging or
// writing anything.
//
// It returns an error only when no correct plan exists (a repository-scoped
// failure). A unit-scoped finding is reported and tolerated: seeing the plan
// is the point of the operation, and the plan it printed is the one a release
// would use.
func (a *App) Status(ctx context.Context) error {
	pl, err := a.computePlan(ctx)
	if err != nil {
		return err
	}
	if blocked := a.releaseBlocked(pl); blocked != "" {
		a.log.Error().Str("reason", blocked).Msg("refusing to release")
		if pl.Fatal() {
			return errors.New(blocked)
		}
	}
	return nil
}

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

// computePlan discovers the workspace, computes the release plan and reports
// it (diagnostics, then the graph).
func (a *App) computePlan(ctx context.Context) (*plan.Plan, error) {
	pkgs, deps, err := a.cfg.Discover(a.root)
	if err != nil {
		a.log.Error().Err(err).Msg("package discovery failed")
		return nil, err
	}
	pl, err := plan.Compute(ctx, a.git, plan.Options{
		Packages:         pkgs,
		Dependencies:     deps,
		Initials:         a.initialVersions(pkgs),
		Root:             a.root,
		NonPackageScopes: a.cfg.NonPackageScopes,
	})
	if err != nil {
		a.log.Error().Err(err).Msg("planning failed")
		return nil, err
	}
	a.printDiagnostics(pl)
	a.printGraph(pl)
	return pl, nil
}

// releaseBlocked reports why the run must not release, or "" when it may.
//
// Two rules, and the split is §16's. A *repository-scoped* error — a tag that
// cannot be read, a version that goes backwards, a dependency cycle — means no
// correct plan exists, so no partial release may be emitted and no
// configuration may say otherwise. Everything else is an authoring mistake
// whose blast radius is the offending unit, and whether that stops the run is
// a judgement about which failure is worse: releasing without a package whose
// scope was mistyped, or not releasing at all. `commitErrors` is where a
// repository states its answer.
func (a *App) releaseBlocked(pl *plan.Plan) string {
	if pl.Fatal() {
		return "the repository cannot produce a correct plan (§16 repository-scoped error)"
	}
	if a.cfg.CommitErrors == config.CommitErrorsError && pl.HasErrors() {
		return `a commit message has errors and commitErrors is "error"`
	}
	return ""
}

// initialVersions maps the configured initials onto discovered package names.
// Viper lowercases map keys, so matching is case-insensitive; keys that match
// no discovered package are warned about and ignored.
func (a *App) initialVersions(pkgs []*model.Package) map[string]ccme.Version {
	if len(a.cfg.InitialVersions) == 0 {
		return nil
	}
	byLower := make(map[string]string, len(pkgs)) // lowercase -> real name
	for _, p := range pkgs {
		byLower[strings.ToLower(p.Name)] = p.Name
	}
	out := make(map[string]ccme.Version, len(a.cfg.InitialVersions))
	for key, v := range a.cfg.InitialVersions {
		if real, ok := byLower[strings.ToLower(key)]; ok {
			out[real] = v
		} else {
			a.log.Warn().Str("package", key).Msg("initials entry matches no discovered package, ignoring")
		}
	}
	return out
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
