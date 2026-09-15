package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// repositoryRecord owns one repository's mutable release state. The mutex
// protects complete commit/tag transactions, not the package dependency graph.
type repositoryRecord struct {
	repo   *config.Repository
	git    *gitx.CLI
	hooks  *runHooks
	branch string
	mu     sync.Mutex
	// expectedHead advances only after an owned commit or an explicit nested
	// step export. Unrelated Git mutations must not change a planned release.
	expectedHead string
	snapshot     *repositoryRefGuard
	publishGate  chan struct{}

	includeReady    bool
	includeInput    []string
	includeResolved []string
	includeRoots    []string
}

func (r *repositoryRecord) remote() string {
	if r.repo.Commit != nil && r.repo.Commit.Remote != "" {
		return r.repo.Commit.Remote
	}
	return "origin"
}

// workspaceRecorder records each package before its consumers may publish.
// A source's native records precede its optional control gitlink checkpoint.
type workspaceRecorder struct {
	app      *App
	byName   map[string]*repositoryRecord
	ordered  []*repositoryRecord
	gh       *ghDispatch
	snapshot *workspaceSnapshotGuard
	pins     *workspacePins
}

func (a *App) newWorkspaceRecorder() *workspaceRecorder {
	w := &workspaceRecorder{app: a, byName: make(map[string]*repositoryRecord), pins: newWorkspacePins(a)}
	for i := range a.workspace.Repositories {
		repo := &a.workspace.Repositories[i]
		log := a.log.With().Str("repository", repo.Name).Logger()
		g := &gitx.CLI{Dir: repo.Root, Log: log}
		if repo.Commit != nil {
			g.Name, g.Email = repo.Commit.Name, repo.Commit.Email
		}
		r := &repositoryRecord{repo: repo, git: g, publishGate: make(chan struct{}, 1),
			hooks: &runHooks{cfg: repo.Config, root: repo.Root, log: log,
				runner: w.pins.runner(a.packageRunner())}}
		w.byName[repo.Name] = r
		w.ordered = append(w.ordered, r)
	}
	sort.Slice(w.ordered, func(i, j int) bool { return w.ordered[i].repo.Name < w.ordered[j].repo.Name })
	return w
}

func (w *workspaceRecorder) acquire(ctx context.Context) (func(), error) {
	type heldLock struct {
		repository *repositoryRecord
		lock       *release.Lock
	}
	var held []heldLock
	unlock := func() {
		for i := len(held) - 1; i >= 0; i-- {
			owned := held[i]
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			releaseMutation, err := owned.repository.git.AcquireMutation(cleanupCtx)
			if err != nil {
				owned.repository.git.Log.Error().Err(err).Str("code", "E336").Str("remedy", release.LockRemedy).
					Msg("unable to acquire local mutation lock for fleet lock cleanup")
			} else {
				owned.lock.Release(cleanupCtx)
				releaseMutation()
			}
			cancel()
		}
	}
	for _, r := range w.ordered {
		if w.app.lockDisabled() || r.repo.Config.UnsafeDisableLock {
			r.git.Log.Debug().Msg("fleet release lock disabled by configured policy")
			continue
		}
		r.git.Log.Debug().Msg("acquiring repository release lock")
		lock := &release.Lock{Git: r.git, Remote: r.remote(), Log: r.git.Log}
		releaseMutation, err := r.git.AcquireMutation(ctx)
		if err == nil {
			err = lock.Acquire(ctx)
			releaseMutation()
		}
		if err != nil {
			unlock()
			return nil, config.WithDiagnostic("E336", fmt.Errorf("E336: repository %s: acquiring fleet release lock: %w", r.repo.Name, err))
		}
		held = append(held, heldLock{repository: r, lock: lock})
	}
	w.app.log.Debug().Int("repositories", len(w.ordered)).Int("locks", len(held)).Msg("fleet release locks acquired")
	return unlock, nil
}

func (w *workspaceRecorder) selectedRepositories(pl *plan.Plan) map[string]bool {
	selected := make(map[string]bool)
	for _, rel := range pl.Releasing() {
		selected[rel.Pkg.Repository] = true
		if rel.Pkg.Repository != config.ControlRepository {
			if control := w.byName[config.ControlRepository]; control != nil && control.repo.Commit.IsEnabled() {
				selected[config.ControlRepository] = true
			}
		}
	}
	return selected
}

func (w *workspaceRecorder) verify(ctx context.Context, pl *plan.Plan) error {
	return w.verifySelected(ctx, w.selectedRepositories(pl))
}

// verifyPlannedHeads proves that the history planner read the same repository
// snapshot composition accepted. This closes the interval between CLI config
// composition and Release acquiring its fleet locks. Empty composition heads
// retain compatibility with manually assembled Workspace values in tests and
// direct internal callers.
func (w *workspaceRecorder) verifyPlannedHeads(pl *plan.Plan) error {
	if pl == nil {
		return config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: release plan has no repository head snapshot"))
	}
	for _, record := range w.ordered {
		planned := pl.RepositoryHeads[record.repo.Name]
		accepted := record.repo.CompositionHead
		if accepted != "" && planned != accepted {
			return config.WithDiagnostic(config.DiagnosticRepositoryInvalid, fmt.Errorf(
				"E330: repository %s moved after workspace composition: accepted %s, planner read %s",
				record.repo.Name, accepted, planned))
		}
		if accepted == "" {
			accepted = planned
		}
		record.expectedHead = accepted
	}
	return nil
}

func (w *workspaceRecorder) verifySelected(ctx context.Context, selected map[string]bool) error {
	for _, r := range w.ordered {
		if !selected[r.repo.Name] || !r.repo.Commit.PushEnabled() {
			continue
		}
		branch, err := r.git.CurrentBranch(ctx)
		if err != nil {
			return err
		}
		if r.repo.Commit.Branch != "" {
			branch = r.repo.Commit.Branch
		}
		if branch != "" {
			if err := gitx.ValidRefName("refs/heads/" + branch); err != nil {
				return config.WithDiagnostic("E337", fmt.Errorf("E337: repository %s: %w", r.repo.Name, err))
			}
		}
		r.branch = branch
		if r.repo.Commit.VerifyEnabled() {
			if err := r.git.VerifyRemote(ctx, r.remote()); err != nil {
				return fmt.Errorf("repository %s: %w", r.repo.Name, err)
			}
			// A detached repository may still push an immutable tag. The branch
			// check becomes mandatory only if this run actually creates a commit.
			if branch == "" {
				continue
			}
			behind, err := r.git.BehindRemote(ctx, r.remote(), branch)
			if err != nil {
				return fmt.Errorf("repository %s: %w", r.repo.Name, err)
			}
			if behind {
				return fmt.Errorf("repository %s is behind remote branch %s; update the checkout before releasing", r.repo.Name, branch)
			}
		}
	}
	return nil
}

func requireReleaseCommitBranch(ctx context.Context, r *repositoryRecord, dirs []string) error {
	if !r.repo.Commit.PushEnabled() || r.branch != "" {
		return nil
	}
	dirty, err := r.git.DirtyPaths(ctx, dirs)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return config.WithDiagnostic("E337", fmt.Errorf("E337: repository %s is detached; set commit.branch before creating and pushing a release commit", r.repo.Name))
	}
	// Keep the empty branch for a clean checkout so PushRelease pushes only
	// the immutable release tag from detached HEAD.
	return nil
}

// verifyPublishBranch runs after the package's beforePublish hook and before
// its publish command. A detached checkout may push a tag at its existing
// commit, but publication must not begin when recording the result would need
// a new branch commit with no explicit destination.
func (w *workspaceRecorder) verifyPublishBranch(ctx context.Context, rel *plan.Release) error {
	source := w.byName[rel.Pkg.Repository]
	if source == nil {
		return fmt.Errorf("missing repository owner for %s", rel.Pkg.Name)
	}
	source.mu.Lock()
	defer source.mu.Unlock()

	sourceCommit, err := w.releaseCommitNeeded(ctx, source, rel)
	if err != nil {
		return err
	}
	if sourceCommit && source.repo.Commit.PushEnabled() && source.branch == "" {
		return config.WithDiagnostic("E337", fmt.Errorf("E337: repository %s is detached; set commit.branch before publishing a release that needs a source commit", source.repo.Name))
	}
	if source.repo.Control {
		return nil
	}
	control := w.byName[config.ControlRepository]
	if control == nil || !control.repo.Commit.PushEnabled() || control.branch != "" {
		return nil
	}
	control.mu.Lock()
	defer control.mu.Unlock()

	checkpoint := sourceCommit
	if !checkpoint {
		pin := rel.ExportedCommit()
		if pin == "" {
			pin, err = source.git.HeadSHA(ctx)
			if err != nil {
				return err
			}
		}
		checkpoint, err = controlSourceDiffers(ctx, control, source, pin)
		if err != nil {
			return err
		}
	}
	if checkpoint {
		return config.WithDiagnostic("E337", fmt.Errorf("E337: repository %s is detached; set commit.branch before publishing a release that needs a control checkpoint", control.repo.Name))
	}
	return nil
}

func (w *workspaceRecorder) releaseCommitNeeded(ctx context.Context, r *repositoryRecord, rel *plan.Release) (bool, error) {
	if !r.repo.Commit.IsEnabled() || rel.ExportedCommit() != "" {
		return false, nil
	}
	dirs, err := w.includeDirs(r, []string{rel.Pkg.Dir})
	if err != nil {
		return false, err
	}
	if rel.Pkg.Changelog.Enabled {
		return true, nil
	}
	dirty, err := r.git.DirtyPaths(ctx, dirs)
	return len(dirty) > 0, err
}

func (w *workspaceRecorder) prepare(ctx context.Context, pl *plan.Plan) error {
	protected := make(map[string][]string)
	selected := w.selectedRepositories(pl)
	env := release.WorkspaceEnv(pl, w.app.log)
	for _, rel := range pl.Releasing() {
		r := w.byName[rel.Pkg.Repository]
		if r == nil {
			return fmt.Errorf("missing repository owner for %s", rel.Pkg.Name)
		}
		if err := validateRecordPath(r, rel); err != nil {
			return err
		}
		if r.repo.Commit.IsEnabled() || rel.Pkg.Space.RevertOnFail {
			protected[r.repo.Name] = append(protected[r.repo.Name], rel.Pkg.Dir)
		}
	}
	for _, r := range w.ordered {
		if r.expectedHead == "" {
			r.expectedHead = pl.RepositoryHeads[r.repo.Name]
		}
		if err := r.verifyExpectedHead(ctx); err != nil {
			return err
		}
		if !selected[r.repo.Name] {
			continue
		}
		r.git.Log.Debug().Str("revision", r.expectedHead).
			Bool("commit", r.repo.Commit.IsEnabled()).Bool("push", r.repo.Commit.PushEnabled()).
			Msg("preparing repository release records")
		r.hooks.env = env
		owner := &App{root: r.repo.Root, cfg: r.repo.Config, git: r.git, log: r.git.Log}
		if err := owner.checkBranchAllowed(ctx); err != nil {
			return fmt.Errorf("repository %s: %w", r.repo.Name, err)
		}
		dirs := protected[r.repo.Name]
		if r.repo.Commit.IsEnabled() && len(dirs) > 0 {
			var err error
			dirs, err = w.includeDirs(r, dirs)
			if err != nil {
				return err
			}
		}
		if len(dirs) == 0 {
			continue
		}
		dirty, err := r.git.DirtyPaths(ctx, dirs)
		if err != nil {
			return err
		}
		if len(dirty) > 0 {
			return fmt.Errorf("repository %s release paths have pre-existing local changes (%s); commit, stash, or move them before releasing", r.repo.Name, strings.Join(dirty, ", "))
		}
	}
	return nil
}

func (w *workspaceRecorder) includeDirs(r *repositoryRecord, dirs []string) ([]string, error) {
	if r.repo.Commit == nil || len(r.repo.Commit.Include) == 0 {
		return dirs, nil
	}
	if !r.includeReady || !slices.Equal(r.includeInput, r.repo.Commit.Include) {
		r.includeReady = false
		r.includeInput = append(r.includeInput[:0], r.repo.Commit.Include...)
		r.includeResolved = r.includeResolved[:0]
		r.includeRoots = r.includeRoots[:0]
		for _, repository := range w.ordered {
			root, err := resolveWritePath(repository.repo.Root)
			if err != nil {
				return nil, err
			}
			r.includeRoots = append(r.includeRoots, root)
		}
		for _, path := range r.repo.Commit.Include {
			resolved, err := w.validateInclude(r, path)
			if err != nil {
				return nil, err
			}
			r.includeResolved = append(r.includeResolved, resolved)
		}
		r.includeReady = true
	}
	// The repository-overlap scan above is cached, but a symlink can be
	// replaced after preflight. Resolve every configured path again immediately
	// before staging and refuse if its ownership changed.
	for i, repository := range w.ordered {
		root, err := resolveWritePath(repository.repo.Root)
		if err != nil {
			return nil, err
		}
		if root != r.includeRoots[i] {
			return nil, fmt.Errorf("repository %s root changed its resolved location after preflight", repository.repo.Name)
		}
	}
	for i, path := range r.repo.Commit.Include {
		abs := filepath.Join(r.repo.Root, filepath.FromSlash(path))
		resolved, err := resolveWritePath(abs)
		if err != nil {
			return nil, err
		}
		if resolved != r.includeResolved[i] {
			return nil, fmt.Errorf("repository %s commit.include path %q changed its resolved owner after preflight", r.repo.Name, path)
		}
	}
	owner := &App{root: r.repo.Root, log: r.git.Log}
	return owner.appendIncludeDirs(dirs, r.repo.Commit.Include), nil
}

func (w *workspaceRecorder) validateInclude(r *repositoryRecord, path string) (string, error) {
	abs := filepath.Join(r.repo.Root, filepath.FromSlash(path))
	resolved, err := resolveWritePath(abs)
	if err != nil {
		return "", err
	}
	ownerRoot, err := resolveWritePath(r.repo.Root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(path) || !pathWithin(ownerRoot, resolved) {
		return "", fmt.Errorf("repository %s commit.include path %q escapes its owner", r.repo.Name, path)
	}
	for _, other := range w.ordered {
		// The control root contains every source checkout. Its containment
		// does not change ownership of a path already inside this source.
		if other == r || (other.repo.Control && !r.repo.Control) {
			continue
		}
		otherRoot, err := resolveWritePath(other.repo.Root)
		if err != nil {
			return "", err
		}
		if pathWithin(resolved, otherRoot) || pathWithin(otherRoot, resolved) {
			return "", fmt.Errorf("repository %s commit.include path %q spans repository %s", r.repo.Name, path, other.repo.Name)
		}
	}
	return resolved, nil
}

// Resolve existing ancestors as well as existing files: generated output may
// not exist yet, but a symlink in its parent path still defines its owner.
func resolveWritePath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func validateRecordPath(r *repositoryRecord, rel *plan.Release) error {
	if !rel.Pkg.Changelog.Enabled {
		return nil
	}
	file := rel.Pkg.Changelog.File
	if file == "" {
		file = "CHANGELOG.md"
	}
	path, err := resolveWritePath(filepath.Join(rel.Pkg.Dir, filepath.FromSlash(file)))
	if err != nil {
		return err
	}
	ownerRoot, err := resolveWritePath(r.repo.Root)
	if err != nil {
		return err
	}
	if filepath.IsAbs(file) || !pathWithin(ownerRoot, path) {
		return fmt.Errorf("repository %s package %s changelog path %q escapes its owner", r.repo.Name, rel.Pkg.Name, file)
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *workspaceRecorder) Record(ctx context.Context, rel *plan.Release) error {
	r := w.byName[rel.Pkg.Repository]
	if r == nil {
		return fmt.Errorf("no repository owner for package %s", rel.Pkg.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.git.Log.Debug().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Msg("recording source release")
	if err := validateRecordPath(r, rel); err != nil {
		return err
	}
	var failures []error
	if err := (&changelog.Dispatcher{Log: r.git.Log}).Record(ctx, rel); err != nil {
		failures = append(failures, err)
	}
	var pin string
	if r.repo.Commit.IsEnabled() && rel.ExportedCommit() == "" {
		var err error
		pin, err = w.commit(ctx, r, []string{rel.Pkg.Dir}, rel, rel.TagName())
		if err != nil {
			return fmt.Errorf("repository %s source release commit failed; no release tag or control checkpoint was written: %w", r.repo.Name, errors.Join(append(failures, err)...))
		}
	}
	// A changelog failure remains reportable after the source record. A
	// failed required source commit above cannot supply a valid release tag.
	pinned, err := w.tagSource(ctx, r, rel, pin)
	if err != nil {
		failures = append(failures, err)
		return fmt.Errorf("repository %s tag %s: %w", r.repo.Name, rel.TagName(), errors.Join(failures...))
	}
	r.admitRecordedRelease(pinned)
	if r.repo.Commit.PushEnabled() {
		r.hooks.run(ctx, "beforePush", r.repo.Config.Run.BeforePush)
		tags := []string{rel.TagName()}
		var moving []string
		for _, alias := range rel.AliasTags() {
			if alias.Force {
				moving = append(moving, alias.Name)
			} else {
				tags = append(tags, alias.Name)
			}
		}
		unlock, err := gitx.AcquireMutations(ctx, r.git)
		if err == nil {
			err = verifyPinnedSource(ctx, r, pinned, rel.TagName())
		}
		if err == nil {
			err = r.git.PushRelease(ctx, r.remote(), r.branch, tags, moving)
		}
		if unlock != nil {
			unlock()
		}
		if err != nil {
			return fmt.Errorf("repository %s tag %s: source push failed; repair source records before advancing the control gitlink: %w", r.repo.Name, rel.TagName(), errors.Join(append(failures, err)...))
		}
		r.hooks.run(ctx, "afterPush", r.repo.Config.Run.AfterPush)
		r.git.Log.Info().Str("tag", rel.TagName()).Msg("pushed source release records")
	}
	if !r.repo.Control && len(failures) == 0 {
		if err := w.checkpoint(ctx, r, pinned, rel.TagName()); err != nil {
			failures = append(failures, err)
		}
	}
	if w.gh != nil {
		if err := w.gh.Record(ctx, pinned); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("repository %s tag %s: %w", r.repo.Name, rel.TagName(), errors.Join(failures...))
	}
	r.git.Log.Info().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Msg("source release recorded")
	return nil
}

func (w *workspaceRecorder) commit(ctx context.Context, r *repositoryRecord, dirs []string, rel *plan.Release, tag string) (string, error) {
	r.hooks.run(ctx, "beforeCommit", r.repo.Config.Run.BeforeCommit)
	unlock, err := gitx.AcquireMutations(ctx, r.git)
	if err != nil {
		return "", err
	}
	if err := r.verifyExpectedHead(ctx); err != nil {
		unlock()
		return "", err
	}
	dirs, err = w.includeDirs(r, dirs)
	msg := renderCommitMessage(r.repo.Commit.MessageFormat, []string{rel.Pkg.Name}, []string{tag})
	var committed bool
	if err == nil {
		err = requireReleaseCommitBranch(ctx, r, dirs)
	}
	if err == nil {
		committed, err = r.git.CommitDirs(ctx, dirs, msg)
	}
	var pin string
	if err == nil {
		pin, err = r.git.HeadSHA(ctx)
		if err == nil {
			r.expectedHead = pin
		}
	}
	if err == nil {
		err = w.pins.remember(r.repo.Name, rel, pin)
	}
	unlock()
	if err != nil {
		return "", err
	}
	if committed {
		r.git.Log.Info().Str("message", msg).Msg("created release commit")
	}
	r.hooks.run(ctx, "afterCommit", r.repo.Config.Run.AfterCommit)
	r.hooks.run(ctx, "postCommit", r.repo.Config.Run.PostCommit)
	return pin, nil
}

func (w *workspaceRecorder) tagSource(ctx context.Context, source *repositoryRecord, rel *plan.Release, pin string) (*plan.Release, error) {
	unlock, err := gitx.AcquireMutations(ctx, source.git)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if pin == "" {
		target := rel.ExportedCommit()
		if target == "" {
			if err := source.verifyExpectedHead(ctx); err != nil {
				return nil, err
			}
			target = "HEAD"
		}
		pin, err = source.git.ResolveCommit(ctx, target)
		if err != nil {
			return nil, err
		}
	}
	pinned := pinnedRelease(rel, pin)
	if err := verifyRepositoryPin(ctx, source, pin); err != nil {
		return nil, err
	}
	source.expectedHead = pin
	if err := w.pins.remember(source.repo.Name, rel, pin); err != nil {
		return nil, err
	}
	source.git.Log.Trace().Str("revision", pin).Str("tag", rel.TagName()).Msg("source release revision verified")
	if err := release.CreateReleaseTag(ctx, source.git, pinned, false, source.git.Log); err != nil {
		return nil, err
	}
	if err := verifyPinnedSource(ctx, source, pinned, rel.TagName()); err != nil {
		return nil, err
	}
	return pinned, nil
}

func pinnedRelease(rel *plan.Release, pin string) *plan.Release {
	pinned := *rel
	pinned.Outputs = append([]plan.Output(nil), rel.Outputs...)
	release.MergeOutputs(&pinned, []plan.Output{{
		Name: plan.PackageCommitExportPrefix + plan.EnvKey(rel.Pkg.Name), Value: pin, Source: "dispat:source-record",
	}})
	return &pinned
}

func verifyRepositoryPin(ctx context.Context, repository *repositoryRecord, pin string) error {
	head, err := repository.git.HeadSHA(ctx)
	if err != nil {
		return err
	}
	if head != pin {
		return fmt.Errorf("repository %s HEAD moved from recorded source revision %s to %s", repository.repo.Name, pin, head)
	}
	return nil
}

func (r *repositoryRecord) verifyExpectedHead(ctx context.Context) error {
	if r.expectedHead == "" {
		return nil
	}
	if err := verifyRepositoryPin(ctx, r, r.expectedHead); err != nil {
		return config.WithDiagnostic(config.DiagnosticRepositoryInvalid, fmt.Errorf("E330: repository changed after planning; re-plan before writing release records: %w", err))
	}
	return nil
}

func verifyPinnedSource(ctx context.Context, source *repositoryRecord, rel *plan.Release, tag string) error {
	pin := rel.ExportedCommit()
	if err := verifyRepositoryPin(ctx, source, pin); err != nil {
		return err
	}
	if tag == "" {
		return nil
	}
	tagPin, err := source.git.ResolveCommit(ctx, "refs/tags/"+tag)
	if err != nil {
		return err
	}
	if tagPin != pin {
		return fmt.Errorf("repository %s tag %s moved from recorded source revision %s to %s", source.repo.Name, tag, pin, tagPin)
	}
	return nil
}

func (w *workspaceRecorder) checkpoint(ctx context.Context, source *repositoryRecord, rel *plan.Release, tag string) error {
	control := w.byName[config.ControlRepository]
	if control == nil || !control.repo.Commit.IsEnabled() {
		return nil
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	pin := rel.ExportedCommit()
	controlPin, err := w.commitCheckpoint(ctx, control, source, rel, tag)
	if err == nil && controlPin != "" && control.repo.Commit.PushEnabled() {
		control.hooks.run(ctx, "beforePush", control.repo.Config.Run.BeforePush)
		var unlock func()
		unlock, err = gitx.AcquireMutations(ctx, source.git, control.git)
		if err == nil {
			err = verifyPinnedSource(ctx, source, rel, tag)
		}
		if err == nil {
			err = verifyRemoteSource(ctx, source, tag, pin)
		}
		if err == nil {
			err = verifyRepositoryPin(ctx, control, controlPin)
		}
		if err == nil {
			err = control.git.PushRelease(ctx, control.remote(), control.branch, nil, nil)
		}
		if unlock != nil {
			unlock()
		}
		if err == nil {
			control.hooks.run(ctx, "afterPush", control.repo.Config.Run.AfterPush)
		}
	}
	if err != nil {
		record := "revision " + pin
		if tag != "" {
			record = "tag " + tag + " at " + pin
		}
		return fmt.Errorf("control checkpoint failed after source %s recorded %s; preserve the source record and explicitly repair its control gitlink before retrying: %w", source.repo.Name, record, err)
	}
	return nil
}

func (w *workspaceRecorder) commitCheckpoint(ctx context.Context, control, source *repositoryRecord, rel *plan.Release, tag string) (string, error) {
	control.hooks.run(ctx, "beforeCommit", control.repo.Config.Run.BeforeCommit)
	unlock, err := gitx.AcquireMutations(ctx, source.git, control.git)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := control.verifyExpectedHead(ctx); err != nil {
		return "", err
	}
	if err := verifyPinnedSource(ctx, source, rel, tag); err != nil {
		return "", err
	}
	needed, err := controlSourceDiffers(ctx, control, source, rel.ExportedCommit())
	if err != nil {
		return "", err
	}
	if !needed {
		unlock()
		control.hooks.run(ctx, "afterCommit", control.repo.Config.Run.AfterCommit)
		control.hooks.run(ctx, "postCommit", control.repo.Config.Run.PostCommit)
		return "", nil
	}
	if control.repo.Commit.PushEnabled() && control.branch == "" {
		return "", config.WithDiagnostic("E337", fmt.Errorf("E337: repository %s is detached; set commit.branch before creating and pushing a control checkpoint", control.repo.Name))
	}
	if control.repo.Commit.PushEnabled() {
		if err := verifyRemoteSource(ctx, source, tag, rel.ExportedCommit()); err != nil {
			return "", fmt.Errorf("control checkpoint cannot be pushed for source %s: %w", source.repo.Name, err)
		}
	}
	var tags []string
	if tag != "" {
		tags = []string{tag}
	}
	msg := renderCommitMessage(control.repo.Commit.MessageFormat, []string{rel.Pkg.Name}, tags)
	gitlinkPath, err := controlGitlinkPath(control, source)
	if err != nil {
		return "", err
	}
	committed, err := control.git.CommitDirs(ctx, []string{filepath.Join(control.repo.Root, gitlinkPath)}, msg)
	if err != nil {
		return "", err
	}
	controlPin, err := control.git.HeadSHA(ctx)
	if err != nil {
		return "", err
	}
	control.expectedHead = controlPin
	if committed {
		control.git.Log.Info().Str("message", msg).Msg("created release commit")
	}
	// Hooks observe the commit and must never run under the advisory lock.
	unlock()
	control.hooks.run(ctx, "afterCommit", control.repo.Config.Run.AfterCommit)
	control.hooks.run(ctx, "postCommit", control.repo.Config.Run.PostCommit)
	if !committed {
		return "", nil
	}
	return controlPin, nil
}

func verifyRemoteSource(ctx context.Context, source *repositoryRecord, tag, pin string) error {
	if tag != "" {
		return source.git.VerifyRemoteRelease(ctx, source.remote(), tag, pin)
	}
	if source.branch == "" {
		return fmt.Errorf("source revision %s has no verified remote tag or branch", pin)
	}
	return source.git.VerifyRemoteBranch(ctx, source.remote(), source.branch, pin)
}

func controlSourceDiffers(ctx context.Context, control, source *repositoryRecord, pin string) (bool, error) {
	path, err := controlGitlinkPath(control, source)
	if err != nil {
		return false, err
	}
	current, err := control.git.GitlinkCommit(ctx, "HEAD", filepath.ToSlash(path))
	if err != nil {
		return false, err
	}
	return current != pin, nil
}

func controlGitlinkPath(control, source *repositoryRecord) (string, error) {
	path := filepath.Clean(filepath.FromSlash(source.repo.GitlinkPath))
	if path == "." || path == ".." || filepath.IsAbs(path) || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source repository %s has no valid gitlink path in control repository %s", source.repo.Name, control.repo.Root)
	}
	return path, nil
}

func (w *workspaceRecorder) RevertDir(ctx context.Context, dir string) error {
	for _, r := range w.ordered {
		if pathWithin(r.repo.Root, dir) && (r.repo.Control == false || filepath.Clean(dir) == filepath.Clean(r.repo.Root)) {
			return r.revertDir(ctx, dir)
		}
	}
	r := w.byName[config.ControlRepository]
	return r.revertDir(ctx, dir)
}

func (r *repositoryRecord) revertDir(ctx context.Context, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := gitx.AcquireMutations(ctx, r.git)
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.verifyExpectedHead(ctx); err != nil {
		return err
	}
	return r.git.RevertDir(ctx, dir)
}
