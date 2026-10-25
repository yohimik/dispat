// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// LatestRelease is the --version value naming the newest release of the
// selected package, and what a bare --version means.
const LatestRelease = "latest"

// historyCleanupTimeout bounds removing the history worktree after the
// caller's context is done.
const historyCleanupTimeout = 30 * time.Second

// previewedRelease is one release a --version value resolved to: the package
// and its release tag, with the tags the replan must not see.
type previewedRelease struct {
	pkg *model.Package
	tag gitx.Tag
	// masked are the tags left out of the replan's inventory: this release,
	// every later release of the package, and every other package's release
	// tag the same run wrote.
	masked []string
}

// releaseInventory is what a --version value is resolved against: every
// package of the workspace with its release tags, and the packages the
// selection covers.
type releaseInventory struct {
	packages []*model.Package
	tags     map[string]gitx.Tags
	selected []string
	// findTag looks one tag up by its exact name, for a value that is an
	// alias rather than a release tag.
	findTag func(name string) (gitx.Tag, bool, error)
	// firstParents lists a commit and its first-parent ancestors, newest
	// first, at most limit of them.
	firstParents func(commit string, limit int) ([]gitx.CommitMessage, error)
	// commitFormat is commit.messageFormat, the template of every release
	// commit's message.
	commitFormat string
}

// previewRelease renders the notes a past release rendered (preview
// --version).
//
// The plan is a pure function of the history, the configuration and the
// record inventory (CCME §17.2), so the release's plan is computed again with
// the tag's commit as HEAD and the release, the rest of its run and
// everything after it removed from the inventory. The configuration is the
// current checkout's: notes rendered under a configuration that has changed
// since may differ from what the release wrote.
//
// The entry's date is the tag's. The one the release wrote is the run's clock
// when it recorded the entry, which nothing durable keeps; the tag was written
// by the same run moments later.
func (a *App) previewRelease(ctx context.Context, opts PreviewOptions) (PreviewResult, error) {
	released, err := a.resolvePreviewedRelease(ctx, opts)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot preview")
		return PreviewResult{}, err
	}
	pl, err := a.planAtRelease(ctx, released)
	if err != nil {
		return PreviewResult{}, err
	}
	a.printDiagnostics(pl)
	if pl.IsFatal() {
		a.log.Error().Str("tag", released.tag.Name).
			Msg("refusing to preview: the repository cannot produce a correct plan at the release's commit")
		return PreviewResult{}, errors.New("no correct plan exists")
	}
	rel, err := a.alignPreviewedRelease(pl, released)
	if err != nil {
		a.log.Error().Err(err).Str("package", released.pkg.Name).Str("tag", released.tag.Name).Msg("cannot preview")
		return PreviewResult{}, err
	}
	date, err := a.git.TagDate(ctx, released.tag.Name)
	if err != nil {
		a.log.Error().Err(err).Str("tag", released.tag.Name).Msg("cannot preview")
		return PreviewResult{}, err
	}
	header := changelog.RenderEntryHeader(rel, date.UTC(), changelog.SpecFormat(rel.Pkg.Changelog.Format))
	notes := a.renderPreviewNotes(rel, opts, previewedNotes{
		header: header,
		githubBody: func(rel *plan.Release) string {
			return a.previewReleasedGitHubBody(rel, a.releaseSectionCommit(released))
		},
	})
	return PreviewResult{Notes: notes, Scope: released.pkg.Name}, nil
}

// resolvePreviewedRelease reads the workspace's release tags and resolves the
// --version value against them and the selection.
func (a *App) resolvePreviewedRelease(ctx context.Context, opts PreviewOptions) (previewedRelease, error) {
	if a.workspace != nil {
		return previewedRelease{}, errors.New("--version previews a release of a single repository, " +
			"and a composed workspace plans several histories at once")
	}
	if err := a.checkGit(); err != nil {
		return previewedRelease{}, err
	}
	pkgs, err := a.packages()
	if err != nil {
		return previewedRelease{}, err
	}
	sel, err := a.selectPackages(a.discoveredWorkspace(pkgs), opts.Filter)
	if err != nil {
		return previewedRelease{}, err
	}
	formats := make(map[string]gitx.TagFormat, len(pkgs))
	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		formats[p.Name] = plan.TagFormatFor(p)
		names = append(names, p.Name)
	}
	tags, err := a.git.TagsForPackages(ctx, formats)
	if err != nil {
		return previewedRelease{}, fmt.Errorf("reading the release tags: %w", err)
	}
	inventory := releaseInventory{
		packages: pkgs,
		tags:     tags,
		selected: sel.Keep(names),
		findTag:  func(name string) (gitx.Tag, bool, error) { return a.git.FindTag(ctx, name) },
		firstParents: func(commit string, limit int) ([]gitx.CommitMessage, error) {
			return a.git.FirstParentMessages(ctx, commit, limit)
		},
	}
	if a.cfg.Commit != nil {
		inventory.commitFormat = a.cfg.Commit.MessageFormat
	}
	released, err := inventory.resolve(opts.Version)
	if err != nil {
		return previewedRelease{}, err
	}
	a.log.Debug().Str("package", released.pkg.Name).Str("tag", released.tag.Name).
		Str("commit", released.tag.Commit).Strs("masked", released.masked).
		Msg("previewing a past release")
	return released, nil
}

// resolve finds the release a --version value names, with the tags its replan
// must not see.
func (inv releaseInventory) resolve(value string) (previewedRelease, error) {
	released, err := inv.find(value)
	if err != nil {
		return previewedRelease{}, err
	}
	run, err := inv.runCommits(released.tag)
	if err != nil {
		return previewedRelease{}, err
	}
	released.masked = inv.maskedFor(released, run)
	return released, nil
}

// find resolves a --version value: latest, a version of the one selected
// package, or a tag that identifies one package of the selection.
func (inv releaseInventory) find(value string) (previewedRelease, error) {
	if value == LatestRelease {
		pkg, err := inv.singleSelected(value)
		if err != nil {
			return previewedRelease{}, err
		}
		tag, ok := inv.latestRelease(pkg)
		if !ok {
			return previewedRelease{}, fmt.Errorf("%s has no release to preview", pkg.Name)
		}
		return previewedRelease{pkg: pkg, tag: tag}, nil
	}
	if version, err := ccme.ParseVersion(value); err == nil {
		pkg, err := inv.singleSelected(value)
		if err != nil {
			return previewedRelease{}, err
		}
		for _, tag := range inv.tags[pkg.Name] {
			if tag.Parsed && tag.Version.Compare(version) == 0 {
				return previewedRelease{pkg: pkg, tag: tag}, nil
			}
		}
		return previewedRelease{}, fmt.Errorf("%s has no release %s", pkg.Name, value)
	}
	return inv.resolveTag(value)
}

// singleSelected is the one package a version or latest can belong to. A
// version names no package, so the selection has to.
func (inv releaseInventory) singleSelected(value string) (*model.Package, error) {
	switch len(inv.selected) {
	case 0:
		return nil, errors.New("no package to preview")
	case 1:
		return inv.packageNamed(inv.selected[0]), nil
	default:
		return nil, fmt.Errorf("--version %s needs one package, and the selection covers %s; "+
			"select one with --package or pass its full tag", value, strings.Join(inv.selected, ", "))
	}
}

// resolveTag finds the package a tag is a release of, by its tag format or,
// for an alias, by the release tag on the alias's commit.
func (inv releaseInventory) resolveTag(value string) (previewedRelease, error) {
	owners, err := inv.tagOwners(value)
	if err != nil {
		return previewedRelease{}, err
	}
	if len(owners) == 0 {
		return previewedRelease{}, fmt.Errorf("%s is not a version, latest, or a release tag of any package", value)
	}
	var selected []previewedRelease
	for _, owner := range owners {
		if inv.isSelected(owner.pkg.Name) {
			selected = append(selected, owner)
		}
	}
	switch len(selected) {
	case 1:
		return selected[0], nil
	case 0:
		return previewedRelease{}, fmt.Errorf("%s is a release of %s, which the selection (%s) does not cover",
			value, owners[0].pkg.Name, strings.Join(inv.selected, ", "))
	default:
		names := make([]string, 0, len(selected))
		for _, owner := range selected {
			names = append(names, owner.pkg.Name)
		}
		return previewedRelease{}, fmt.Errorf("%s reads as a release of several packages (%s); select one with --package",
			value, strings.Join(names, ", "))
	}
}

// tagOwners lists every package the tag is a release of. A release tag is
// matched by name; an alias is matched by the release tag its package wrote
// on the same commit, because an alias is only ever written and carries no
// version a moving one could be read back from.
func (inv releaseInventory) tagOwners(value string) ([]previewedRelease, error) {
	var owners []previewedRelease
	for _, pkg := range inv.packages {
		for _, tag := range inv.tags[pkg.Name] {
			if tag.Parsed && tag.Name == value {
				owners = append(owners, previewedRelease{pkg: pkg, tag: tag})
				break
			}
		}
	}
	if len(owners) > 0 {
		return owners, nil
	}
	var alias gitx.Tag
	var isAliasFound bool
	for _, pkg := range inv.packages {
		if !isAliasOf(pkg, value) {
			continue
		}
		if !isAliasFound {
			found, ok, err := inv.findTag(value)
			if err != nil {
				return nil, fmt.Errorf("looking up %s: %w", value, err)
			}
			if !ok {
				return nil, nil
			}
			alias, isAliasFound = found, true
		}
		if tag, ok := inv.releaseAt(pkg, alias.Commit); ok {
			owners = append(owners, previewedRelease{pkg: pkg, tag: tag})
		}
	}
	return owners, nil
}

// isAliasOf reports whether one of the package's alias formats could have
// written the name.
func isAliasOf(pkg *model.Package, name string) bool {
	if pkg.Space == nil {
		return false
	}
	for _, alias := range pkg.Space.AliasTags {
		if gitx.AliasFormat(alias.Format).Matcher(pkg.Name).IsMatch(name) {
			return true
		}
	}
	return false
}

// latestRelease is the package's highest release by SemVer precedence.
func (inv releaseInventory) latestRelease(pkg *model.Package) (gitx.Tag, bool) {
	return highestRelease(inv.tags[pkg.Name], func(gitx.Tag) bool { return true })
}

// releaseAt is the package's highest release on one commit.
func (inv releaseInventory) releaseAt(pkg *model.Package, commit string) (gitx.Tag, bool) {
	return highestRelease(inv.tags[pkg.Name], func(tag gitx.Tag) bool { return tag.Commit == commit })
}

// highestRelease is the highest parsed tag that keep admits. Tags.Baseline is
// not used because an unparsed newest tag is its answer, which names no
// release.
func highestRelease(tags gitx.Tags, keep func(gitx.Tag) bool) (gitx.Tag, bool) {
	var best gitx.Tag
	found := false
	for _, tag := range tags {
		if !tag.Parsed || !keep(tag) {
			continue
		}
		if !found || best.Version.Compare(tag.Version) < 0 {
			best, found = tag, true
		}
	}
	return best, found
}

// runCommits are the commits the run that wrote release made, and so the
// commits whose other release tags did not exist yet when it planned.
//
// The tag's own commit is always one. Where it is a release commit, the run
// may have written more below it: a package whose flow commits for itself
// gets a release commit of its own, one after the other on the first-parent
// line, on top of the commit the run planned at. Walking down while the
// commit is a release commit finds them. A run writes at most one release
// commit per package and one of its own, which bounds the walk.
func (inv releaseInventory) runCommits(release gitx.Tag) (map[string]bool, error) {
	run := map[string]bool{release.Commit: true}
	commits, err := inv.firstParents(release.Commit, len(inv.packages)+1)
	if err != nil {
		return nil, fmt.Errorf("reading the commits below %s: %w", release.Name, err)
	}
	for _, commit := range commits {
		if !inv.isReleaseCommit(commit) {
			break
		}
		run[commit.SHA] = true
	}
	return run, nil
}

// isReleaseCommit reports whether dispat's release-commit phase wrote the
// commit: its message is commit.messageFormat rendered with the release tags
// the commit carries, in the order the message names them. A commit that
// carries a tag its message does not name was tagged by another run as well,
// and is not one run's release commit.
func (inv releaseInventory) isReleaseCommit(commit gitx.CommitMessage) bool {
	type named struct {
		pkg, tag string
		at       int
	}
	var carried []named
	seen := make(map[string]bool)
	for _, pkg := range inv.packages {
		for _, tag := range inv.tags[pkg.Name] {
			if !tag.Parsed || tag.Commit != commit.SHA || seen[tag.Name] {
				continue
			}
			seen[tag.Name] = true
			carried = append(carried, named{pkg: pkg.Name, tag: tag.Name, at: strings.Index(commit.Message, tag.Name)})
		}
	}
	if len(carried) == 0 {
		return false
	}
	sort.SliceStable(carried, func(i, j int) bool { return carried[i].at < carried[j].at })
	pkgs := make([]string, 0, len(carried))
	tags := make([]string, 0, len(carried))
	for _, entry := range carried {
		pkgs = append(pkgs, entry.pkg)
		tags = append(tags, entry.tag)
	}
	return strings.TrimSpace(renderCommitMessage(inv.commitFormat, pkgs, tags)) == strings.TrimSpace(commit.Message)
}

// maskedFor lists the tags the replan of released must not see.
//
// Every other package's release tags on the run's commits are masked because
// the run wrote those beside this one, and to it they did not exist yet: with
// them gone, those packages plan again exactly as the run planned them, and
// what this release picked up from them comes back. The package's own earlier
// releases on its commit stay: they were its history when it released.
func (inv releaseInventory) maskedFor(released previewedRelease, run map[string]bool) []string {
	var masked []string
	for _, owner := range inv.packages {
		for _, tag := range inv.tags[owner.Name] {
			if released.isMaskedTag(owner.Name, tag, run) {
				masked = append(masked, tag.Name)
			}
		}
	}
	return masked
}

// isMaskedTag reports whether a tag of the owner package is hidden from the
// replan of the release, whose run wrote the commits in run.
func (released previewedRelease) isMaskedTag(owner string, tag gitx.Tag, run map[string]bool) bool {
	if owner != released.pkg.Name {
		return run[tag.Commit]
	}
	return tag.Name == released.tag.Name || (tag.Parsed && tag.Version.Compare(released.tag.Version) > 0)
}

// isSelected reports whether the selection covers the named package.
func (inv releaseInventory) isSelected(name string) bool {
	for _, selected := range inv.selected {
		if selected == name {
			return true
		}
	}
	return false
}

// packageNamed is the workspace package of that name.
func (inv releaseInventory) packageNamed(name string) *model.Package {
	for _, pkg := range inv.packages {
		if pkg.Name == name {
			return pkg
		}
	}
	return nil
}

// planAtRelease computes the plan the release was cut from: the tag's commit
// as HEAD, with the release's masked tags out of the inventory.
func (a *App) planAtRelease(ctx context.Context, released previewedRelease) (*plan.Plan, error) {
	opts, err := a.planOptions(ctx)
	if err != nil {
		return nil, err
	}
	opts.IgnoredTags = append(opts.IgnoredTags, released.masked...)
	scratch, err := os.MkdirTemp("", "dispat-preview-")
	if err != nil {
		a.log.Error().Err(err).Msg("cannot create the folder the release's history is read from")
		return nil, err
	}
	head := filepath.Join(scratch, "head")
	defer a.removeHistoryWorktree(ctx, scratch, head)
	if err := a.git.AddHistoryWorktree(ctx, head, released.tag.Commit); err != nil {
		a.log.Error().Err(err).Str("tag", released.tag.Name).Msg("cannot read the history at the release's commit")
		return nil, err
	}
	history := &gitx.LocalGitx{Dir: head, Log: a.log}
	pl, err := plan.Compute(ctx, history, opts)
	if err != nil {
		a.log.Error().Err(err).Str("tag", released.tag.Name).Msg("planning failed")
		return nil, err
	}
	return pl, nil
}

// removeHistoryWorktree takes the history worktree away again. It runs under
// a bounded context detached from the caller's, so an interrupted preview
// still leaves no worktree behind in the repository.
func (a *App) removeHistoryWorktree(ctx context.Context, scratch, head string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), historyCleanupTimeout)
	defer cancel()
	if _, err := os.Stat(head); err == nil {
		if err := a.git.RemoveWorktree(cleanupCtx, head); err != nil {
			a.log.Warn().Err(err).Str("path", head).
				Msg("the preview's history worktree was not removed; git worktree prune forgets it")
		}
	}
	if err := os.RemoveAll(scratch); err != nil {
		a.log.Warn().Err(err).Str("path", scratch).Msg("the preview's scratch folder was not removed")
	}
}

// alignPreviewedRelease finds the release in the replan and holds it to the
// version that was released. A replan that computes another version is
// rendered under the released one and said out loud, because the difference
// can only come from a configuration that has changed since.
func (a *App) alignPreviewedRelease(pl *plan.Plan, released previewedRelease) (*plan.Release, error) {
	rel := pl.Releases[released.pkg.Name]
	if rel == nil || !rel.IsReleasing() {
		return nil, fmt.Errorf("the plan at %s's commit releases nothing for %s; "+
			"the configuration may have changed since that release", released.tag.Name, released.pkg.Name)
	}
	if rel.Next.Compare(released.tag.Version) != 0 {
		a.log.Warn().Str("package", rel.Pkg.Name).Str("tag", released.tag.Name).
			Str("planned", rel.Next.String()).Str("released", released.tag.Version.String()).
			Msg("the plan at the release's commit computes another version; " +
				"the notes are rendered under the released one and may differ from what the release wrote")
		rel.Next = released.tag.Version
		rel.Channel = channelOfVersion(released.tag.Version)
	}
	return rel, nil
}

// releaseSectionCommit is the commit the GitHub body's release section names,
// empty when the body has none. Only the release-commit phase stamps the
// section, and it tags the commit it stamps, so the tag's commit is the one a
// release with a release commit named.
func (a *App) releaseSectionCommit(released previewedRelease) string {
	if !a.cfg.Commit.IsEnabled() {
		return ""
	}
	return released.tag.Commit
}

// previewReleasedGitHubBody renders the GitHub release body a past release
// sent. A note the mid-release merge recovery added and a commit a script
// exported in place of the release commit are absent, because nothing in the
// history records them.
func (a *App) previewReleasedGitHubBody(rel *plan.Release, commit string) string {
	spec := rel.Pkg.GitHub
	if reason := withheldReason(spec.Enabled, spec.Channels, rel.Channel); reason != "" {
		return "github release withheld: " + reason + "\n"
	}
	return github.RenderReleaseBody(rel, changelog.SpecFormat(spec.Format).WithRepoEnv(), nil, commit, "")
}
