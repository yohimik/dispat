// Package app is the application: it plans and releases the monorepo. The two
// operations a user can ask for — see the plan, run the plan — are methods on
// App, callable with nothing but a configuration and a logger, so the cli
// package stays a thin command-line controller (flags in, exit code out) and
// any other front end could drive the same struct.
package app

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
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

// computePlan discovers the workspace, computes the release plan and reports
// it (diagnostics, then the graph).
func (a *App) computePlan(ctx context.Context) (*plan.Plan, error) {
	pkgs, deps, err := config.Discover(a.cfg, a.root)
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
		ParserConfig:     a.cfg.ParserConfig,
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
