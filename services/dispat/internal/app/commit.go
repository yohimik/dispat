package app

import (
	"context"
	"fmt"
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// CommitOptions selects what Commit covers and does. The override fields,
// when set, replace the corresponding commit.* config values for this
// invocation.
type CommitOptions struct {
	Package string // explicit target, or ""
	Dir     string // where the command was invoked; narrows inside a package folder
	Tag     bool   // also create the annotated release tag
	Push    bool   // push the branch, and with Tag the tags
	Name    string // overrides commit.name (committer identity)
	Email   string // overrides commit.email
	Remote  string // overrides commit.remote
	Message string // overrides commit.messageFormat
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
	targets, err := a.stepTargets(pl, opts.Package, opts.Dir)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot commit")
		return err
	}

	git := *a.git
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

	var tags []string
	for _, name := range targets {
		rel := pl.Releases[name]
		log := a.log.With().Str("package", name).Str("tag", rel.TagName()).Logger()
		dirs := a.appendIncludeDirs([]string{rel.Pkg.Dir}, include)
		msg := renderCommitMessage(format, []string{name}, []string{rel.TagName()})
		committed, err := git.CommitDirs(ctx, dirs, msg)
		if err != nil {
			log.Error().Err(err).Msg("release commit failed")
			return err
		}
		if committed {
			log.Info().Str("message", msg).Msg("created release commit")
		} else {
			log.Debug().Msg("nothing to commit")
		}
		if opts.Tag {
			if err := release.CreateReleaseTag(ctx, &git, rel, log); err != nil {
				log.Error().Err(err).Msg("tagging failed")
				return err
			}
			tags = append(tags, rel.TagName())
		}
		if out := os.Getenv(release.OutputEnvVar); out != "" {
			if err := exportPackageCommit(ctx, &git, out, name); err != nil {
				log.Error().Err(err).Msg("exporting the commit pin failed")
				return err
			}
		}
	}
	if opts.Push && len(targets) > 0 {
		skipped, err := git.Push(ctx, remote, tags)
		if err != nil {
			a.log.Error().Err(err).Str("remote", remote).Msg("push failed")
			return err
		}
		for _, tag := range skipped {
			a.log.Warn().Str("tag", tag).Str("remote", remote).
				Msg("tag already exists on the remote, skipped")
		}
		a.log.Info().Str("remote", remote).Strs("tags", tags).Msg("pushed")
	}
	return nil
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
