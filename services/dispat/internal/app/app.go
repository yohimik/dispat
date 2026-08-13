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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// App holds everything one run needs: the monorepo root, its validated
// configuration, the logger the run reports through, the git client and the
// manifest scanner (swappable in tests).
type App struct {
	root string
	cfg  *config.File
	log  zerolog.Logger
	git  *gitx.CLI
	scan scanner.Scanner
}

// New assembles an App for one monorepo.
func New(root string, cfg *config.File, log zerolog.Logger) *App {
	git := &gitx.CLI{Dir: root, Log: log}
	if cfg.Commit != nil {
		// The configured identity covers every commit and annotated tag the
		// run creates, so CI needs no `git config` step.
		git.Name, git.Email = cfg.Commit.Name, cfg.Commit.Email
	}
	return &App{root: root, cfg: cfg, log: log, git: git, scan: scanner.New()}
}

// Status computes the plan and reports it — diagnostics, then the full graph
// with versions and channel transitions — without executing, tagging or
// writing anything. It takes the release's own options because it is that
// release seen in advance: the same selection, narrowed the same way, so what
// the graph shows is what `dispat release` with those flags would do.
//
// It returns an error only when no correct plan exists (a repository-scoped
// failure), when --strict refuses the selection, or when --require-release
// finds nothing to release. A unit-scoped finding is reported and tolerated:
// seeing the plan is the point of the operation, and the plan it printed is the
// one a release would use.
func (a *App) Status(ctx context.Context, opts ReleaseOptions) error {
	pl, err := a.selectedPlan(ctx, opts)
	if err != nil {
		return err
	}
	if blocked := a.releaseBlocked(pl); blocked != "" {
		if pl.Fatal() {
			// No correct plan exists: the one case status itself fails on.
			a.log.Error().Str("reason", blocked).Msg("refusing to release")
			return errors.New(blocked)
		}
		// A release would refuse, but showing the plan is this command's job
		// and the plan shown is correct — so status reports the refusal at
		// warning level and still exits 0.
		a.log.Warn().Str("reason", blocked).Msg("a release would be refused")
	}
	return nil
}

// computePlan discovers the workspace, computes the release plan and reports
// it (diagnostics, then the graph). It is the whole plan, unnarrowed: `dispat
// run` selects its own packages afterwards and never releases any of them, so
// the graph it prints is the repository's. The commands that do release one
// take selectedPlan below.
func (a *App) computePlan(ctx context.Context) (*plan.Plan, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return nil, err
	}
	a.printDiagnostics(pl)
	a.printGraph(pl)
	return pl, nil
}

// selectedPlan is computePlan for the two commands that release the plan
// rather than read it: the plan is narrowed to the invocation's selection
// between the diagnostics and the graph, so the graph printed is the run the
// operator is about to get.
//
// The order matters at the end too. A --strict refusal comes after the graph,
// never instead of it: "this selection cannot be released cleanly" is only
// actionable next to the plan that says why.
func (a *App) selectedPlan(ctx context.Context, opts ReleaseOptions) (*plan.Plan, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return nil, err
	}
	a.printDiagnostics(pl)
	narrowing, err := a.narrow(pl, opts.Filter)
	if err != nil {
		return nil, err
	}
	a.printGraph(pl)
	if opts.Strict && !narrowing.Clean() {
		err := errors.New("the selection cannot be released as it stands and --strict is set")
		a.log.Error().Err(err).Msg("refusing to release")
		return nil, err
	}
	// The same placement, for the same reason: --require-release is a refusal
	// about the plan, and it belongs after the plan that explains it. A fatal
	// plan is left alone so releaseBlocked keeps the truer message — "no correct
	// plan exists" outranks "nothing to release", and both exit 1 anyway.
	if opts.RequireRelease && !pl.Fatal() && len(pl.Releasing()) == 0 {
		err := errors.New("the plan releases nothing and --require-release is set")
		a.log.Error().Err(err).Msg("refusing to release")
		return nil, err
	}
	return pl, nil
}

// checkGit verifies the two prerequisites every command dies without — the
// git executable and the repository — before any real work, so a missing
// prerequisite reads as one clear error instead of a raw git failure halfway
// through planning. The .git entry may be a directory or, for worktrees and
// submodules, a file; existing is all that is checked.
func (a *App) checkGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git executable not found in PATH (dispat shells out to git)")
	}
	if _, err := os.Stat(filepath.Join(a.root, ".git")); err != nil {
		return fmt.Errorf("%s is not a git repository root (no .git); the dispat config belongs at the repository root", a.root)
	}
	return nil
}

// planOptions discovers the workspace and assembles the planner inputs — for
// Compute, and for every other plan-package entry point that needs the same
// workspace view (PackagesChangedSince).
func (a *App) planOptions() (plan.Options, error) {
	pkgs, deps, err := config.Discover(a.cfg, a.root)
	if err != nil {
		a.log.Error().Err(err).Msg("package discovery failed")
		return plan.Options{}, err
	}
	a.logWorkspace(pkgs, deps)
	return plan.Options{
		Packages:         pkgs,
		Dependencies:     deps,
		Initials:         a.initialVersions(pkgs),
		Root:             a.root,
		NonPackageScopes: a.cfg.NonPackageScopes,
		ParserConfig:     a.cfg.ResolvedParser,
		Log:              a.log,
	}, nil
}

// logWorkspace records what discovery resolved, for the questions a layered
// configuration makes hard to answer from the file alone: which space each
// package landed in and how it versions there, which folder it is scoped to,
// and where its dependency edges came from.
//
// None of it is an event the user asked about, so nothing here is louder than
// debug. The per-edge lines are trace, because a large workspace has many
// more edges than packages and the interesting one is usually a single edge
// somebody is looking for.
func (a *App) logWorkspace(pkgs []*model.Package, deps []model.Dependency) {
	if !a.log.Debug().Enabled() {
		return
	}
	for _, p := range pkgs {
		ev := a.log.Debug().Str("package", p.Name).Str("scope", p.ScopeDir())
		if s := p.Space; s != nil {
			// The versioning group rather than the space name: they are the
			// same thing until configuration joins spaces together, and when
			// they differ the group is the one that decides the version.
			ev = ev.Str("space", s.Name).Str("versioning", string(s.Versioning))
			if g := p.VersionGroupName(); g != "" && g != s.Name {
				ev = ev.Str("versionGroup", g)
			}
		}
		if len(p.Ignore) > 0 {
			ev = ev.Int("ignoreLevels", len(p.Ignore))
		}
		ev.Msg("package resolved")
	}
	for _, d := range deps {
		a.log.Trace().
			Str("consumer", d.Consumer).
			Str("provider", d.Provider).
			Str("kind", string(d.Kind)).
			Msg("dependency edge")
	}
}

// plan discovers the workspace and computes the release plan without
// reporting it — for the commands whose output is not the graph (`test`,
// `preview`), which print the diagnostics alone.
func (a *App) plan(ctx context.Context) (*plan.Plan, error) {
	if err := a.checkGit(); err != nil {
		a.log.Error().Err(err).Msg("git prerequisites missing")
		return nil, err
	}
	opts, err := a.planOptions()
	if err != nil {
		return nil, err
	}
	pl, err := plan.Compute(ctx, a.git, opts)
	if err != nil {
		a.log.Error().Err(err).Msg("planning failed")
		return nil, err
	}
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
