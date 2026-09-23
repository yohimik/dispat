// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

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
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/scanner"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// App holds everything one run needs: the monorepo root, its validated
// configuration, the logger the run reports through, the git client and the
// manifest scanner (swappable in tests).
type App struct {
	root string
	cfg  *config.File
	log  zerolog.Logger
	git  *gitx.LocalGitx
	scan scanner.Scannerx
	// workspace is nil for legacy single-repository behavior. In polyrepo
	// mode it is the immutable ownership map shared by every command.
	workspace *config.Workspace

	// sender is who this process is when it takes part in distributed
	// execution, and the zero sender when it does not. It is resolved once,
	// here, so that every event this run sends is named by the same decision
	// the run logger was named by.
	sender release.Sender

	// The discovered workspace, remembered for the run. Discovery walks the
	// filesystem and is repeatable rather than cheap, and one exec invocation
	// can now ask for it three times over — a cwd subject, a cwd script-from
	// and an --in naming a package. See packages.
	pkgsOnce sync.Once
	pkgs     []*model.Package
	pkgsErr  error

	// The spaces as they effectively are — the root file's entries with each
	// space folder's own config file merged over them — remembered for the
	// same reason as pkgs: one exec invocation can ask for a space's scripts
	// and its env separately. See spaces.
	spacesOnce sync.Once
	spaceCfgs  []spaceOwner
	spacesErr  error

	// ignoreTags are tag names masked from baseline resolution when this
	// invocation is a step command wired to a running release; see stepenv.go.
	ignoreTags             []string
	ignoreTagsByRepository map[string][]string

	// releaseLock is the remote release lock this run acquired for a single
	// history, nil in a composed workspace (where the recorder holds one per
	// repository) and nil under a bypass. It is kept because ownership is a
	// question a distributed run asks again later, not only once.
	releaseLock *release.Lock
	// retention is the distributed run's answer to the one question the unlock
	// path has to ask it: which repositories' exclusion must survive this run
	// because a publication it authorized cannot be accounted for. It is nil
	// for every run that delegates nothing, and the question is then never
	// asked.
	retention lockRetentionx
	// coordinator is this run's distributed execution, kept so that the
	// closing summary can ask it what every task came to. It is nil for every
	// run that delegates nothing, and the summary then prints nothing.
	coordinator *execution.Coordinator

	// runID names this run when it spreads over several machines, and is
	// empty for every run that does not: it is generated once, before the
	// plan is fixed, so that the line naming the plan can also name the run
	// every assignment of it will carry.
	runID string
	// planDigest is the name of the plan this run fixed, remembered by the
	// one place that computes it so that the coordinator can state it in
	// every assignment without digesting the plan a second time.
	planDigest string
	// plannedHeads are the repository heads the fixed plan was computed
	// against, keyed by repository identity and by the empty name for a single
	// history. Every prepared input state a node builds from descends from
	// exactly these commits (§28.3), so they are read once, where the digest
	// reads them, rather than asked of git again later.
	plannedHeads map[string]string

	// owedAtHead are the pairs E201 refuses in the plan this invocation
	// selected: a provider it would release at the baseline commit of a
	// consumer it still owes. Written by selectedPlan, read by releaseBlocked.
	owedAtHead []plan.OwedPair

	// plannedOptions are the planner inputs the last plan was computed from,
	// kept for the one consumer that needs the input rather than the result:
	// a distributed run digests the plan together with what it was planned
	// from (§28.3), and assembling those inputs a second time would walk the
	// workspace again. Written by plan alone, which is the only place a plan
	// is computed.
	plannedOptions plan.Options
}

// packages is the discovered workspace, walked once per App.
//
// Discovery is deterministic and side-effect free, so remembering its answer
// changes nothing about what a caller sees; it only stops the same walk being
// paid for two and three times in one invocation. Callers that need the
// declared dependencies alongside it still call config.DiscoverPackages
// themselves, since that is a different question.
func (a *App) packages() ([]*model.Package, error) {
	a.pkgsOnce.Do(func() {
		if a.workspace == nil {
			a.pkgs, _, _, a.pkgsErr = config.DiscoverPackages(a.cfg, a.root)
		} else {
			a.pkgs, _, _, a.pkgsErr = config.DiscoverWorkspace(a.cfg, a.root, a.workspace)
		}
	})
	return a.pkgs, a.pkgsErr
}

// New assembles an App for one monorepo.
func New(root string, cfg *config.File, log zerolog.Logger) *App {
	return NewWorkspace(root, cfg, nil, log)
}

// NewWorkspace is New with a composed repository ownership map.
func NewWorkspace(root string, cfg *config.File, workspace *config.Workspace, log zerolog.Logger) *App {
	git := &gitx.LocalGitx{Dir: root, Log: log}
	if cfg.Commit != nil {
		// The configured identity covers every commit and annotated tag the
		// run creates, so CI needs no `git config` step.
		git.Name, git.Email = cfg.Commit.Name, cfg.Commit.Email
	}
	return &App{root: root, cfg: cfg, workspace: workspace, log: log, git: git, scan: scanner.New(),
		sender: execution.ResolveSender(cfg.Execution, false, os.Environ())}
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
		if pl.IsFatal() {
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

// ErrNothingToRelease is the --require-release refusal: the plan is correct
// and it releases nothing. It is exported so the cli can give it an exit code
// of its own — a pipeline gating on `dispat status --require-release` needs
// "nothing to do" told apart from "something is wrong".
var ErrNothingToRelease = errors.New("the plan releases nothing and --require-release is set")

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
	// The pairs §19.3 refuses are read off the narrowed plan, because the
	// selection is what decides whether a consumer releases after its provider.
	if err := a.reportOwedAtHead(ctx, pl); err != nil {
		return nil, err
	}
	if opts.Strict && !narrowing.IsClean() {
		err := errors.New("the selection cannot be released as it stands and --strict is set")
		a.log.Error().Err(err).Msg("refusing to release")
		return nil, err
	}
	// The same placement, for the same reason: --require-release is a refusal
	// about the plan, and it belongs after the plan that explains it. A fatal
	// plan is left alone so releaseBlocked keeps the truer message — "no correct
	// plan exists" outranks "nothing to release", and a fatal plan exits 1
	// where this refusal exits 3.
	if opts.RequireRelease && !pl.IsFatal() && len(pl.Releasing()) == 0 {
		a.log.Error().Err(ErrNothingToRelease).Msg("refusing to release")
		return nil, ErrNothingToRelease
	}
	// The plan both commands work from is settled here, selection included, so
	// this is where a distributed run fixes it and says which one it is. With
	// no worker links nothing is computed and nothing is written.
	if err := a.recordFixedPlan(ctx, pl, opts); err != nil {
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
func (a *App) planOptions(ctx context.Context) (plan.Options, error) {
	if err := ctx.Err(); err != nil {
		return plan.Options{}, err
	}
	pkgs, deps, inactiveExternal, excluded, err := config.DiscoverWorkspacePlan(a.cfg, a.root, a.workspace)
	if err != nil {
		a.logError(err).Msg("package discovery failed")
		return plan.Options{}, err
	}
	if a.workspace != nil {
		if err := a.resolveRepositoryRecords(ctx, pkgs); err != nil {
			a.log.Error().Err(err).Msg("repository record targets could not be resolved")
			return plan.Options{}, err
		}
	}
	a.logWorkspace(pkgs, deps, excluded)
	opts := plan.Options{
		Packages:                     pkgs,
		Dependencies:                 deps,
		InactiveExternalDependencies: inactiveExternal,
		Initials:                     a.initialVersions(pkgs),
		Root:                         a.root,
		NonPackageScopes:             a.cfg.NonPackageScopes,
		ParserConfig:                 a.cfg.ResolvedParser,
		Log:                          a.log,
		IgnoredTags:                  a.ignoreTags,
		IgnoredTagsByRepository:      a.ignoreTagsByRepository,
	}
	if a.workspace != nil {
		opts.LinkEvidence = a.workspace.IsLinked()
		opts.Repositories = make(map[string]plan.RepositoryHistory, len(a.workspace.Repositories))
		for _, repository := range a.workspace.Repositories {
			repositoryGit := &gitx.LocalGitx{Dir: repository.Root, Name: a.git.Name, Email: a.git.Email, Log: a.log}
			opts.Repositories[repository.Name] = plan.RepositoryHistory{
				Name: repository.Name, Root: repository.Root, Path: repository.GitlinkPath, Git: repositoryGit,
				ParserConfig: repository.Config.ResolvedParser, NonPackageScopes: repository.Config.NonPackageScopes,
				Control: repository.Control, Linker: repository.Linker, Links: repository.Links,
			}
		}
		for _, baseline := range a.cfg.RepositoryBaselines {
			opts.RepositoryBaselines = append(opts.RepositoryBaselines, plan.RepositoryBaseline{
				Consumer: baseline.Consumer, ReleaseTag: baseline.ReleaseTag,
				Repository: baseline.Repository, Revision: baseline.Revision,
			})
		}
	}
	return opts, nil
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
func (a *App) logWorkspace(pkgs []*model.Package, deps []model.Dependency, excluded []config.ExcludedDir) {
	if !a.log.Debug().Enabled() {
		return
	}
	// The folders .dispatexclude dropped, said out loud: an excluded folder
	// is otherwise mentioned nowhere, and "why is my package missing" is the
	// question this line exists for.
	for _, e := range excluded {
		a.log.Debug().Str("space", e.Space).Str("folder", e.Name).
			Msg("package folder excluded by " + config.DispatexcludeName)
	}
	names := make([]string, 0, len(a.cfg.Spaces))
	for name := range a.cfg.Spaces {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := a.cfg.Spaces[name]
		ev := a.log.Debug().Str("space", name).Strs("paths", sc.Path)
		if sc.Versioning != "" {
			ev = ev.Str("versioning", sc.Versioning)
		}
		ev.Msg("space resolved")
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
			// The other two axes, named only where they are not the default,
			// so the ordinary group's line stays the line it always was.
			if s.CounterSharing.IsIndependent() {
				ev = ev.Str("counter", string(s.CounterSharing))
			}
			if s.ChannelSharing.IsIndependent() {
				ev = ev.Str("channels", string(s.ChannelSharing))
			}
			// What the package's build is declared to produce and where it may
			// run, named only when something declared them, so the line a
			// workspace without them writes is the line it always wrote. It is
			// the one place a reader can check what the ladder resolved to
			// without starting a release.
			if len(s.BuildOutputs) > 0 {
				ev = ev.Strs("buildOutputs", s.BuildOutputs)
			}
			if len(s.BuildPlatforms) > 0 {
				ev = ev.Strs("buildPlatforms", s.BuildPlatforms)
			}
			// What the package's space imposes on the consumers of its
			// packages, named only where it is not the relation every space
			// has by default, so the line a workspace that never states the
			// key writes is the line it always wrote.
			if relation := s.ProviderRelation; !relation.IsDefault() {
				ev = ev.Str("providerRelation", string(relation.Build)).
					Bool("providerBlocking", relation.IsBlocking)
			}
			// Where the package's two delegable stages may be placed, on the
			// same terms: named only when a level stated it, and as the pair
			// it resolved to rather than as the shape the file wrote, since
			// what a reader is checking is what the ladder came to.
			if s.RunOnly != nil {
				ev = ev.Strs("runOnly", []string{s.RunOnly.ResolveBuild(), s.RunOnly.ResolvePublish()})
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
	opts, err := a.planOptions(ctx)
	if err != nil {
		return nil, err
	}
	a.plannedOptions = opts
	// Capture workload counts only when they will be logged. Large fleets can
	// then distinguish repeated history reads from graph work without paying
	// for diagnostic counters during ordinary releases.
	work := a.log.Debug()
	var started time.Time
	if work.Enabled() {
		opts.HistoryStats = &plan.HistoryStats{}
		started = time.Now()
	}
	pl, err := plan.Compute(ctx, a.git, opts)
	if s := opts.HistoryStats; s != nil {
		work.Dur("elapsed", time.Since(started)).Err(err).
			Int64("tagInventories", s.TagInventories.Load()).
			Int64("commitWindows", s.CommitWindows.Load()).
			Int64("uniqueCommits", s.UniqueCommits.Load()).
			Int64("canonicalBytes", s.CanonicalBytes.Load()).
			Int64("windowCommitRefs", s.WindowCommitRefs.Load()).
			Int64("ancestryLookups", s.AncestryLookups.Load()).
			Int64("controlSnapshotRuns", s.ControlSnapshotRuns.Load()).
			Int64("persistentLinkNodes", s.PersistentLinkNodes.Load()).
			Int64("reachabilityEdges", s.ReachabilityEdges.Load()).
			Int64("channelFrontierEntries", s.ChannelFrontierEntries.Load()).
			Int64("linkReads", s.LinkReads.Load()).Msg("planning workload")
	}
	if err != nil {
		a.log.Error().Err(err).Msg("planning failed")
		return nil, err
	}
	return pl, nil
}

// releaseBlocked reports why the run must not release, or "" when it may.
//
// Three rules. A *repository-scoped* error (a tag that cannot be read, a
// version that goes backwards, a dependency cycle) means no correct plan
// exists, so no partial release may be emitted and no configuration may say
// otherwise (§16). A provider released at the baseline commit of a consumer it
// still owes, without that consumer after it, would leave two releases on one
// commit that nothing can order afterwards, so the run is refused whatever
// the configuration says (E201, §19.3). Everything else is an authoring
// mistake whose blast radius is the offending unit, and whether that stops the
// run is a judgement about which failure is worse: releasing without a
// package whose scope was mistyped, or not releasing at all. `commitErrors` is
// where a repository states its answer.
func (a *App) releaseBlocked(pl *plan.Plan) string {
	if pl.IsFatal() {
		return "the repository cannot produce a correct plan (§16 repository-scoped error)"
	}
	if len(a.owedAtHead) > 0 {
		return "a provider would be released at the baseline commit of a consumer it still owes (E201)"
	}
	if a.cfg.CommitErrors == config.CommitErrorsError && pl.IsInvalid() {
		return `a commit message has errors and commitErrors is "error"`
	}
	return ""
}

// initialVersions maps the configured initials onto discovered package names.
// Matching is case-insensitive, like every other name in the configuration;
// keys that match no discovered package are warned about and ignored. Two
// packages differing only in case would leave an entry with no single package
// to route to, so their entries are refused with the candidates named —
// discovery refuses such a pair outright, and this stays as the reading that
// cannot be surprised by one.
func (a *App) initialVersions(pkgs []*model.Package) map[string]ccme.Version {
	if a.workspace != nil {
		return a.workspaceInitialVersions(pkgs)
	}
	if len(a.cfg.InitialVersions) == 0 {
		return nil
	}
	byLower := make(map[string]string, len(pkgs)) // lowercase -> real name
	collided := make(map[string][]string)
	for _, p := range pkgs {
		low := globx.Fold(p.Name)
		if prev, dup := byLower[low]; dup {
			collided[low] = append(collided[low], prev, p.Name)
		}
		byLower[low] = p.Name
	}
	out := make(map[string]ccme.Version, len(a.cfg.InitialVersions))
	for key, v := range a.cfg.InitialVersions {
		low := globx.Fold(key)
		if names := collided[low]; len(names) > 0 {
			a.log.Warn().Str("initial", key).Strs("candidates", names).
				Msg("initials entry is ambiguous between case-colliding packages, ignoring")
			continue
		}
		if real, ok := byLower[low]; ok {
			out[real] = v
		} else {
			a.log.Warn().Str("package", key).Msg("initials entry matches no discovered package, ignoring")
		}
	}
	return out
}
