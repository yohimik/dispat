package app

import (
	"context"

	"fmt"
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// CommitOptions selects what Commit covers and does. The override fields,
// when set, replace the corresponding commit.* config values for this
// invocation.
type CommitOptions struct {
	Window  WindowOptions // which packages the command covers
	OnError string        // what a failure does to the failed package's dependents
	Tag     bool          // also create the annotated release tag
	Push    bool          // push the branch, and with Tag the tags
	Name    string        // overrides commit.name (committer identity)
	Email   string        // overrides commit.email
	Remote  string        // overrides commit.remote
	Message string        // overrides commit.messageFormat
	Include []string
}

// Commit creates each covered package's release commit: the package folder
// staged (plus the commit.include paths), the message rendered per
// commit.messageFormat with that one package's name and tag. A package with
// nothing staged is a clean no-op. With Tag, the annotated release tag is
// created at the resulting HEAD; a tag that already exists at that commit is
// a skip (W223). With Push, the branch is pushed once after the loop, and
// with Tag the tags too, skipping any already on the remote. When the
// process environment carries DISPAT_OUTPUT (the command runs inside a
// release stage script), each package's commit is exported as
// PACKAGE_<KEY>=<sha>, pinning the outer run's tag and GitHub release to it.
func (a *App) Commit(ctx context.Context, opts CommitOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot commit")
		return err
	}

	// A fresh CLI, not a struct copy: the CLI carries lazily built cache
	// state (a sync.Once) that must not be copied.
	git := &gitx.CLI{Dir: a.git.Dir, Name: a.git.Name, Email: a.git.Email}
	if opts.Name != "" {
		git.Name = opts.Name
	}
	if opts.Email != "" {
		git.Email = opts.Email
	}
	include := opts.Include
	format := opts.Message
	remote := opts.Remote
	if a.cfg.Commit != nil {
		if len(include) == 0 {
			include = a.cfg.Commit.Include
		}
		if format == "" {
			format = a.cfg.Commit.MessageFormat
		}
		if remote == "" {
			remote = a.cfg.Commit.Remote
		}
	}
	if remote == "" {
		remote = "origin"
	}

	work := &commitWork{app: a, git: git, tag: opts.Tag, format: format, include: include}
	rep, err := a.sweepStep(ctx, pl, covered, work, opts.OnError, "commit")
	if err != nil {
		return err
	}
	if !opts.Push || rep.Ran == 0 {
		return nil
	}
	skipped, err := git.Push(ctx, remote, work.tags)
	if err != nil {
		a.log.Error().Err(err).Str("remote", remote).Msg("push failed")
		return err
	}
	for _, tag := range skipped {
		a.log.Warn().Str("tag", tag).Str("remote", remote).
			Msg("tag already exists on the remote, skipped")
	}
	a.log.Info().Str("remote", remote).Strs("tags", work.tags).Msg("pushed")
	return nil
}

// commitWork is `dispat commit`'s share of a sweep: one package's release
// commit, its tag, and the commit pin it exports.
//
// It is serial, and not as a precaution: a repository has one index and one
// HEAD, so two packages committing at once would stage each other's files.
// That also makes tags a plain slice — only one package is ever inside Do.
type commitWork struct {
	app     *App
	git     *gitx.CLI
	tag     bool
	format  string
	include []string
	tags    []string
}

func (w *commitWork) stage() string { return "commit" }
func (w *commitWork) serial() bool  { return true }

func (w *commitWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	if !w.app.releasing(rel) {
		return nil, nil
	}
	// The failures return rather than log: the sweep reports a failed package
	// once, with the error, and an error that names what it was doing reads
	// the same as the line this used to print itself.
	return func(ctx context.Context) error {
		name := rel.Pkg.Name
		log := w.app.log.With().Str("package", name).Str("tag", rel.TagName()).Logger()
		dirs := w.app.appendIncludeDirs([]string{rel.Pkg.Dir}, w.include)
		msg := renderCommitMessage(w.format, []string{name}, []string{rel.TagName()})
		committed, err := w.git.CommitDirs(ctx, dirs, msg)
		if err != nil {
			return fmt.Errorf("release commit failed: %w", err)
		}
		if committed {
			log.Info().Str("message", msg).Msg("created release commit")
		} else {
			log.Debug().Msg("nothing to commit")
		}
		if w.tag {
			if err := release.CreateReleaseTag(ctx, w.git, rel, log); err != nil {
				return fmt.Errorf("tagging failed: %w", err)
			}
			w.tags = append(w.tags, rel.TagName())
		}
		if out := os.Getenv(release.OutputEnvVar); out != "" {
			if err := exportPackageCommit(ctx, w.git, out, name); err != nil {
				return fmt.Errorf("exporting the commit pin failed: %w", err)
			}
		}
		return nil
	}, nil
}

// exportPackageCommit appends the package's HEAD as PACKAGE_<KEY>=<sha> to
// the DISPAT_OUTPUT file at path, so the outer run pins the package's tag
// and GitHub release to the commit this command created.
func exportPackageCommit(ctx context.Context, git interface {
	HeadSHA(context.Context) (string, error)
}, path, pkg string) error {
	sha, err := git.HeadSHA(ctx)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s%s=%s\n", plan.PackageCommitExportPrefix, plan.EnvKey(pkg), sha)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
