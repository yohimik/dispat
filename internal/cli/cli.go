// Package cli wires the tool together: flags, configuration, planning,
// execution and reporting.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"

	"github.com/yohimik/monorel/internal/changelog"
	"github.com/yohimik/monorel/internal/config"
	"github.com/yohimik/monorel/internal/github"
	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/model"
	"github.com/yohimik/monorel/internal/plan"
	"github.com/yohimik/monorel/internal/release"
	"github.com/yohimik/monorel/internal/script"
	"github.com/yohimik/monorel/internal/semver"
)

// Commands accepted by Run.
const (
	cmdRelease = "release" // build and publish changed packages (default)
	cmdStatus  = "status"  // only print the graph and new versions
)

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("monorel", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "monorepo root folder")
	cfgName := fs.String("config", "monorel.json", "config file name, relative to --root")
	fs.IntSlice("concurrency", nil, "override the configured concurrency: one value for both stages, or build,publish (e.g. 4,2)")
	fs.String("log-level", "", "override the configured logLevel (pretty, trace, debug, info, warn, error)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `usage: monorel [command] [flags]

commands:
  release  build and publish changed packages (default)
  status   print the project graph and new versions, without building

flags:
%s`, fs.FlagUsages())
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Config errors are reported with a bootstrap logger since the configured
	// log level is not known yet.
	bootLog := zerolog.New(zerolog.ConsoleWriter{Out: stderr, TimeFormat: "15:04:05"}).
		With().Timestamp().Logger()

	cmd := cmdRelease
	if rest := fs.Args(); len(rest) > 0 {
		cmd = rest[0]
		if cmd != cmdRelease && cmd != cmdStatus {
			bootLog.Error().Str("command", cmd).Msg("unknown command (want release or status)")
			fs.Usage()
			return 2
		}
		if len(rest) > 1 {
			bootLog.Error().Strs("args", rest[1:]).Msg("unexpected arguments")
			return 2
		}
	}

	cfg, err := config.Load(filepath.Join(*root, *cfgName), fs)
	if err != nil {
		bootLog.Error().Err(err).Msg("invalid configuration")
		return 1
	}
	log := newLogger(cfg.LogLevel, stdout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pkgs, deps, err := cfg.Discover(*root)
	if err != nil {
		log.Error().Err(err).Msg("package discovery failed")
		return 1
	}

	git := &gitx.CLI{Dir: *root}
	pl, err := plan.Compute(ctx, git, pkgs, deps, initialVersions(cfg, pkgs, log))
	if err != nil {
		log.Error().Err(err).Msg("planning failed")
		return 1
	}
	printGraph(log, pl)
	if cmd == cmdStatus {
		return 0
	}

	commitMode := cfg.Commit.IsEnabled()
	pushMode := cfg.Commit.PushEnabled()
	remote := cfg.Commit.Remote
	if remote == "" {
		remote = "origin"
	}

	// Resolve the GitHub releaser (nil when disabled or unresolvable).
	var gh *github.Releaser
	if cfg.GitHub.IsEnabled() {
		var ghErr error
		if gh, ghErr = githubReleaser(cfg.GitHub); ghErr != nil {
			log.Warn().Err(ghErr).Msg("github releases disabled")
		}
	} else {
		log.Debug().Msg("github releases disabled by config")
	}

	// Verify external access up front, before any release work starts.
	if pushMode {
		if err := git.VerifyRemote(ctx, remote); err != nil {
			log.Error().Err(err).Str("remote", remote).Msg("git remote verification failed")
			return 1
		}
	}
	if gh != nil {
		if err := gh.Verify(ctx); err != nil {
			log.Error().Err(err).Msg("github verification failed")
			return 1
		}
	}

	// In release-commit mode, tagging and GitHub releases move to the
	// finalize phase so they reference the end-of-run commit.
	var tagger release.Tagger = git
	if commitMode {
		tagger = nil
	}
	recs := recorders(cfg, log)
	if gh != nil && !commitMode {
		recs = append(recs, gh)
	}

	exec := &release.Executor{
		BuildConcurrency:   cfg.BuildConcurrency,
		PublishConcurrency: cfg.PublishConcurrency,
		Runner:             &script.ShellRunner{Shell: cfg.Shell},
		Tagger:             tagger,
		Recorders:          recs,
		Reverter:           git,
		Log:                log,
	}
	start := time.Now()
	results := exec.Run(ctx, pl)
	finErr := finalize(ctx, cfg, git, gh, remote, pl, results, log)
	code := summarize(log, pl, results, time.Since(start))
	if finErr != nil && code == 0 {
		code = 1
	}
	return code
}

// finalize runs the end-of-run release-commit phase: one commit staging every
// published package's folder, tags on that commit, the push (when enabled),
// and the GitHub releases — created last, so that with push enabled they
// reference commits and tags that already exist on the remote.
func finalize(ctx context.Context, cfg *config.File, git *gitx.CLI, gh *github.Releaser, remote string, pl *plan.Plan, results map[string]*release.Result, log zerolog.Logger) error {
	if !cfg.Commit.IsEnabled() {
		return nil
	}

	var pkgs, tags, dirs []string
	var rels []*plan.Release
	for _, name := range pl.Order {
		if r, ok := results[name]; ok && r.Status == release.StatusPublished {
			rel := pl.Releases[name]
			pkgs = append(pkgs, name)
			tags = append(tags, gitx.TagName(name, rel.Next))
			dirs = append(dirs, rel.Pkg.Dir)
			rels = append(rels, rel)
		}
	}
	if len(pkgs) == 0 {
		return nil
	}

	msg := renderCommitMessage(cfg.Commit.MessageFormat, pkgs, tags)
	committed, err := git.CommitDirs(ctx, dirs, msg)
	if err != nil {
		log.Error().Err(err).Msg("release commit failed")
		return err
	}
	if committed {
		log.Info().Str("message", msg).Msg("created release commit")
	}
	for _, tag := range tags {
		if err := git.CreateTag(ctx, tag, "release "+tag); err != nil {
			log.Error().Err(err).Str("tag", tag).Msg("tagging failed")
			return err
		}
	}
	if cfg.Commit.PushEnabled() {
		if err := git.Push(ctx, remote); err != nil { // branch + tags
			log.Error().Err(err).Str("remote", remote).Msg("push failed")
			return err
		}
		log.Info().Str("remote", remote).Strs("tags", tags).Msg("pushed release commit and tags")
	}
	if gh != nil {
		// The releases document the exact release commit and tag in their
		// body, whether or not they were pushed; with push enabled the tag is
		// additionally pinned to the commit via target_commitish (only then
		// does the SHA exist on the remote).
		if sha, err := git.HeadSHA(ctx); err != nil {
			log.Warn().Err(err).Msg("cannot resolve HEAD, github releases will omit the commit")
		} else {
			gh.CommitSHA = sha
			if cfg.Commit.PushEnabled() {
				gh.TargetCommitish = sha
			}
		}
		for _, rel := range rels {
			if err := gh.Record(ctx, rel); err != nil {
				log.Error().Err(err).Str("package", rel.Pkg.Name).Msg("github release failed")
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

// initialVersions maps the configured initials onto discovered package names.
// Viper lowercases map keys, so matching is case-insensitive; keys that match
// no discovered package are warned about and ignored.
func initialVersions(cfg *config.File, pkgs []*model.Package, log zerolog.Logger) map[string]semver.Version {
	if len(cfg.InitialVersions) == 0 {
		return nil
	}
	byLower := make(map[string]string, len(pkgs)) // lowercase -> real name
	for _, p := range pkgs {
		byLower[strings.ToLower(p.Name)] = p.Name
	}
	out := make(map[string]semver.Version, len(cfg.InitialVersions))
	for key, v := range cfg.InitialVersions {
		if real, ok := byLower[strings.ToLower(key)]; ok {
			out[real] = v
		} else {
			log.Warn().Str("package", key).Msg("initials entry matches no discovered package, ignoring")
		}
	}
	return out
}

// recorders assembles the per-publish release recorders enabled by the
// configuration (currently the changelog file writer; the GitHub releaser is
// appended by Run depending on the release-commit mode).
func recorders(cfg *config.File, log zerolog.Logger) []release.ReleaseRecorder {
	var recs []release.ReleaseRecorder
	if cfg.Changelog.IsEnabled() {
		recs = append(recs, &changelog.FileWriter{
			File:   cfg.Changelog.File,
			Title:  cfg.Changelog.Title,
			Format: entryFormat(cfg.Changelog.EntryFormatConfig),
		})
	} else {
		log.Debug().Msg("changelog files disabled by config")
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

// newLogger builds the run logger. "pretty" renders human-friendly console
// output; any other level emits machine-readable JSON for CI pipelines.
func newLogger(level string, out io.Writer) zerolog.Logger {
	if level == "pretty" {
		w := zerolog.ConsoleWriter{Out: out, TimeFormat: "15:04:05"}
		return zerolog.New(w).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	}
	lvl, err := zerolog.ParseLevel(level)
	if err != nil { // config validation makes this unreachable
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(out).Level(lvl).With().Timestamp().Logger()
}

// printGraph prints the whole project graph in dependency order, highlighting
// changed packages with their version transition.
func printGraph(log zerolog.Logger, pl *plan.Plan) {
	changedCount := 0
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		ev := log.Info().
			Str("package", name).
			Str("space", rel.Pkg.Space.Name)
		if provs := pl.Providers[name]; len(provs) > 0 {
			ev = ev.Strs("dependsOn", provs)
		}
		if rel.Changed() {
			changedCount++
			if rel.FromInitials {
				ev = ev.Bool("baselineFromInitials", true)
			}
			ev.Str("bump", rel.Bump.String()).
				Str("version", rel.Current.String()+" -> "+rel.Next.String()).
				Int("ownCommits", len(rel.Commits)).
				Strs("dueToProviders", rel.DueTo).
				Msg("● changed")
		} else {
			ev.Str("version", rel.Current.String()).Msg("unchanged")
		}
	}
	log.Info().Int("packages", len(pl.Order)).Int("changed", changedCount).Msg("release plan ready")
}

// summarize prints one line per processed package plus totals, and returns the
// process exit code (1 when anything failed).
func summarize(log zerolog.Logger, pl *plan.Plan, results map[string]*release.Result, took time.Duration) int {
	published, failed, skipped := 0, 0, 0
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok {
			continue
		}
		var ev *zerolog.Event
		switch res.Status {
		case release.StatusPublished:
			published++
			ev = log.Info().Str("tag", gitx.TagName(name, res.To))
		case release.StatusFailed:
			failed++
			ev = log.Error().Err(res.Err).Str("failedStage", res.FailedStage)
		default:
			skipped++
			ev = log.Warn()
		}
		ev.Str("package", name).
			Str("status", res.Status.String()).
			Str("version", res.From.String()+" -> "+res.To.String()).
			Dur("took", res.Duration).
			Msg("summary")
	}
	log.Info().
		Int("published", published).
		Int("failed", failed).
		Int("skipped", skipped).
		Int("unchanged", len(pl.Order)-len(results)).
		Dur("took", took).
		Msg("done")
	if failed > 0 {
		return 1
	}
	return 0
}
