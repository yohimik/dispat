package app

import (
	"context"
	"fmt"
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func (a *App) commitWorkspace(ctx context.Context, pl *plan.Plan, covered []string, opts CommitOptions) error {
	w := a.newWorkspaceRecorder()
	w.setLinkPlan(pl)
	for _, r := range w.ordered {
		r.expectedHead = pl.RepositoryHeads[r.repo.Name]
	}
	selected := make(map[string]bool)
	for _, name := range covered {
		if rel := pl.Releases[name]; rel != nil && rel.IsReleasing() {
			selected[rel.Pkg.Repository] = true
		}
	}
	// Step overrides are invocation-local; never mutate the shared workspace.
	for _, r := range w.ordered {
		packageSelected := selected[r.repo.Name]
		if !packageSelected && !r.repo.Control {
			continue
		}
		repo := *r.repo
		policy := config.CommitConfig{}
		if repo.Commit != nil {
			policy = *repo.Commit
		}
		if packageSelected {
			enabled := true
			policy.Enabled = &enabled
		}
		// A standalone step pushes only when this invocation asked it to.
		// Keep the control repository's configured enabled state so an optional
		// checkpoint is neither forced on nor disabled by a source-local step.
		policy.Push = opts.Push
		if packageSelected {
			if opts.Name != "" {
				policy.Name = opts.Name
			}
			if opts.Email != "" {
				policy.Email = opts.Email
			}
			if opts.Remote != "" {
				policy.Remote = opts.Remote
			}
			if opts.Message != "" {
				policy.MessageFormat = opts.Message
			}
			if len(opts.Include) > 0 {
				policy.Include = opts.Include
			}
		}
		repo.Commit = &policy
		r.repo = &repo
		// A fresh handle rather than a write into the one already in use.
		// LocalGitx.run reads Name and Email on every invocation and the
		// recorder's handles are shared across the goroutines a fleet runs,
		// so writing the identity in place would be a write racing reads.
		// Replacing the pointer is the same move newWorkspaceRecorder makes,
		// and it is what makes the identity of a handle immutable once it
		// exists.
		r.git = &gitx.LocalGitx{Dir: r.git.Dir, Name: policy.Name, Email: policy.Email, Log: r.git.Log}
		r.hooks.env = release.WorkspaceEnv(pl, a.log)
	}
	if opts.Push {
		verified := make(map[string]bool, len(selected)+1)
		checkpoint := false
		for name := range selected {
			verified[name] = true
			checkpoint = checkpoint || name != config.ControlRepository
		}
		if control := w.byName[config.ControlRepository]; checkpoint && control != nil && control.repo.Commit.IsEnabled() {
			verified[config.ControlRepository] = true
		}
		if err := w.verifySelected(ctx, verified); err != nil {
			return err
		}
	}
	work := &workspaceCommitWork{records: w, opts: opts}
	_, err := a.sweepStep(ctx, pl, covered, work, opts.OnError, "commit")
	return err
}

type workspaceCommitWork struct {
	records *workspaceRecorder
	opts    CommitOptions
}

func (*workspaceCommitWork) stage() string { return "commit" }
func (*workspaceCommitWork) serial() bool  { return true }
func (w *workspaceCommitWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	if !w.records.app.releasing(rel) {
		return nil, nil
	}
	r := w.records.byName[rel.Pkg.Repository]
	if r == nil {
		return nil, fmt.Errorf("missing repository owner for %s", rel.Pkg.Name)
	}
	return func(ctx context.Context) error {
		// The fleet links this release depends on are recorded before the
		// release commit, so the commit's own tree carries the evidence. It
		// happens outside r.mu because settling takes each repository's own
		// lock, this one included.
		if err := w.settle(ctx, rel); err != nil {
			return err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		tag := w.opts.TagName
		if tag == "" {
			tag = rel.TagName()
		}
		pin, err := w.records.commit(ctx, r, []string{rel.Pkg.Dir}, rel, tag)
		if err != nil {
			return err
		}
		pinned := pinnedRelease(rel, pin)
		if w.opts.Tag {
			unlock, lockErr := gitx.AcquireMutations(ctx, r.git)
			if lockErr != nil {
				return lockErr
			}
			if err = verifyRepositoryPin(ctx, r, pin); err == nil {
				err = release.CreateReleaseTagAs(ctx, r.git, pinned, tag, false, r.git.Log)
			}
			unlock()
			if err != nil {
				return err
			}
		}
		if w.opts.Push {
			var tags, moving []string
			if w.opts.Tag {
				tags = []string{tag}
				for _, alias := range rel.AliasTags() {
					if alias.Force && !w.opts.NoForce {
						moving = append(moving, alias.Name)
					} else {
						tags = append(tags, alias.Name)
					}
				}
			}
			unlock, lockErr := gitx.AcquireMutations(ctx, r.git)
			if lockErr != nil {
				return lockErr
			}
			if err = verifyRepositoryPin(ctx, r, pin); err == nil && w.opts.Tag {
				err = verifyPinnedSource(ctx, r, pinned, tag)
			}
			if err == nil {
				err = r.git.PushRelease(ctx, r.remote(), r.branch, tags, moving)
			}
			unlock()
			if err != nil {
				return err
			}
		}
		if out := os.Getenv(release.OutputEnvVar); out != "" {
			if err = exportPackageCommit(ctx, pinnedHead(pin), out, rel.Pkg.Name); err != nil {
				return err
			}
		}
		// A nested step exports its source pin; the outer recorder owns the
		// checkpoint after the entire publish stage has succeeded.
		if os.Getenv(release.OutputEnvVar) == "" && !r.repo.Control {
			checkpointTag := ""
			if w.opts.Tag {
				checkpointTag = tag
			}
			return w.records.checkpoint(ctx, r, pinned, checkpointTag)
		}
		return nil
	}, nil
}

// settle records the fleet links this release needs, reserving each
// repository's publish lane for the settlement and giving them all back
// afterwards: a step command publishes nothing itself.
func (w *workspaceCommitWork) settle(ctx context.Context, rel *plan.Release) error {
	lanes, err := w.records.settleLanes(rel)
	if err != nil {
		return err
	}
	held, err := w.records.takeLanes(ctx, lanes)
	if err != nil {
		return err
	}
	defer releaseLanes(held, "")
	return w.records.settleLinks(ctx, rel, held)
}

type pinnedHead string

func (p pinnedHead) HeadSHA(context.Context) (string, error) { return string(p), nil }
