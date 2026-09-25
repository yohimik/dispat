// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// E201 (§19.3): a provider is released at the baseline commit of a consumer it
// still owes only in a run that releases the consumer after it. Two releases
// on one commit have no ancestry order, so afterwards nothing could tell that
// the consumer came first: it would read as served and stay on the provider's
// old version, with no plan left to find the debt.

// reportOwedAtHead finds the pairs E201 refuses before anything is published,
// in the plan this invocation is about to release, and logs each one with both
// remedies. The pairs are kept for releaseBlocked: a release is refused, and
// status reports that it would be. None of it enters the plan's diagnostics,
// so the plan digest and the commitErrors policy read the same plan either way.
func (a *App) reportOwedAtHead(ctx context.Context, pl *plan.Plan) error {
	isHeadReached, readErr := a.resolveHeadReach(ctx, pl)
	pairs := pl.OwedAtHead(isHeadReached)
	if err := readErr(); err != nil {
		a.log.Error().Err(err).Msg("cannot compare the head the plan releases from with a consumer's baseline")
		return err
	}
	for _, pair := range pairs {
		a.log.Error().Str("code", plan.CodeOwedAtBaseline).Str("package", pair.Consumer).
			Str("provider", pair.Provider).Str("commit", pair.Commit).
			Str("remedy", fmt.Sprintf("release %s in the same run (--package %s,%s), or release %s after a new commit",
				pair.Consumer, pair.Provider, pair.Consumer, pair.Provider)).
			Msg("a provider would be released at the baseline commit of a consumer it still owes")
	}
	a.owedAtHead = pairs
	return nil
}

// resolveHeadReach answers, per repository, whether the head the plan releases
// from is an ancestor-or-self of a commit: where a provider's tag written now
// would sit against a consumer's baseline. The head is the snapshot a composed
// plan records, and in one history the checkout's HEAD, read once and only
// when some pair needs it. The second function reports the first failed read,
// after which every question is answered as reached, so a failure can only
// add a refusal the caller then replaces with the error.
func (a *App) resolveHeadReach(ctx context.Context, pl *plan.Plan) (func(repository, commit string) bool, func() error) {
	var readErr error
	heads := make(map[string]string)
	gits := make(map[string]*gitx.LocalGitx)
	isHeadReached := func(repository, commit string) bool {
		if readErr != nil {
			return true
		}
		key := globx.Fold(repository)
		head, isRead := heads[key]
		if !isRead {
			head, readErr = a.readPlanHead(ctx, pl, repository)
			heads[key] = head
		}
		if readErr != nil || head == "" || commit == "" {
			return readErr != nil
		}
		if head == commit {
			return true
		}
		git, isOpen := gits[key]
		if !isOpen {
			git = a.openRepositoryGit(repository)
			gits[key] = git
		}
		var isReached bool
		isReached, readErr = git.IsAncestor(ctx, head, commit)
		return isReached || readErr != nil
	}
	return isHeadReached, func() error { return readErr }
}

// readPlanHead is the head a plan releases one repository from: the snapshot
// a composed plan records, and the checkout's HEAD in one history.
func (a *App) readPlanHead(ctx context.Context, pl *plan.Plan, repository string) (string, error) {
	if a.workspace == nil {
		head, err := a.git.HeadSHA(ctx)
		if err != nil {
			return "", fmt.Errorf("reading HEAD: %w", err)
		}
		return head, nil
	}
	if sha, ok := pl.RepositoryHeads[repository]; ok {
		return sha, nil
	}
	for name, sha := range pl.RepositoryHeads {
		if globx.Fold(name) == globx.Fold(repository) {
			return sha, nil
		}
	}
	return "", nil
}

// openRepositoryGit is a Git client for a repository of the workspace, the
// run's own in one history or where the repository is not a participant.
func (a *App) openRepositoryGit(repository string) *gitx.LocalGitx {
	if a.workspace == nil {
		return a.git
	}
	if repo := a.workspace.RepositoryByName(repository); repo != nil {
		return &gitx.LocalGitx{Dir: repo.Root, Log: a.log}
	}
	return a.git
}

// publicationOutcome is what a run did with its plan: the plan, what became of
// each package, and the fleet recorder whose repositories hold the records, nil
// in one history.
type publicationOutcome struct {
	plan    *plan.Plan
	results map[string]*release.Result
	fleet   *workspaceRecorder
}

// reportOwedAfterPublication is E201 after the fact: a consumer that did not
// publish while a provider it is owed did, where the provider's release commit
// is the consumer's baseline or behind it. Ancestry now reads the consumer as
// served, so no later plan can compute the debt, and the one remedy is an
// explicit Release-As on the consumer at the version this run planned (§8.6).
// Each such pair is a critical, so the run fails naming it, and so is a pair
// the check cannot answer: a debt it could not rule out is as invisible to the
// next plan as one it found.
//
// Commit mode moves a tag onto the release commit, past the consumer's
// baseline, so this can only fire where the provider's tag stayed on the head
// the run started from, or where its scripts pinned it to an older commit.
func (a *App) reportOwedAfterPublication(ctx context.Context, outcome publicationOutcome, crit *criticals) {
	pl, results := outcome.plan, outcome.results
	for _, consumerName := range pl.Order {
		consumer := pl.Releases[consumerName]
		if consumer == nil || isPublished(results[consumerName]) {
			continue
		}
		for _, providerName := range consumer.DueTo {
			boundary := consumer.OwedBoundary(providerName)
			provider := pl.Releases[providerName]
			if boundary == "" || provider == nil || !isPublished(results[providerName]) {
				continue
			}
			git := a.selectRepositoryGit(provider.Pkg.Repository, outcome.fleet)
			pair := owedPairCheck{consumer: consumer, provider: provider, boundary: boundary}
			released, isBehind, err := locateProviderRelease(ctx, git, provider.TagName(), boundary)
			if err != nil {
				pair.recordUnanswered(a.log, crit, err)
				continue
			}
			if isBehind {
				pair.recordStranded(a.log, crit, released)
			}
		}
	}
}

// owedPairCheck is one consumer and one provider it is owed, with the
// consumer's baseline in the provider's repository.
type owedPairCheck struct {
	consumer, provider *plan.Release
	boundary           string
}

// recordStranded records the pair E201 reports after publication: the
// provider's release sits at or behind the consumer's baseline.
func (c owedPairCheck) recordStranded(log zerolog.Logger, crit *criticals, released string) {
	consumerName, providerName := c.consumer.Pkg.Name, c.provider.Pkg.Name
	err := fmt.Errorf("%s was released at %s, which %s's baseline %s reaches, and %s did not release after it",
		c.provider.TagName(), released, consumerName, c.boundary, consumerName)
	crit.record(log, plan.CodeOwedAtBaseline, err,
		"a consumer did not publish after its provider was released at its baseline commit",
		func(e *zerolog.Event) *zerolog.Event {
			return e.Str("package", consumerName).Str("provider", providerName).Str("commit", released).
				Str("remedy", fmt.Sprintf("commit release(%s) with the footer Release-As: %s",
					consumerName, c.consumer.Next.String()))
		})
}

// recordUnanswered records a pair the check could not answer.
func (c owedPairCheck) recordUnanswered(log zerolog.Logger, crit *criticals, err error) {
	crit.record(log, plan.CodeOwedAtBaseline, err,
		"cannot tell whether a consumer was left behind a provider released at its baseline commit",
		func(e *zerolog.Event) *zerolog.Event {
			return e.Str("package", c.consumer.Pkg.Name).Str("provider", c.provider.Pkg.Name).
				Str("commit", c.boundary)
		})
}

// releaseAncestry is what locateProviderRelease asks Git.
type releaseAncestry interface {
	ResolveCommit(ctx context.Context, rev string) (string, error)
	TagExists(ctx context.Context, name string) (bool, error)
	IsAncestor(ctx context.Context, a, b string) (bool, error)
}

// locateProviderRelease reads the commit a provider's release tag sits on and
// whether a consumer's baseline reaches it. A tag known to be absent is no
// release, and answers with no error: it was never written, and the failure
// that stopped it is reported where it happened. Every other failure is
// returned, a cancelled context included, because the question stays open.
func locateProviderRelease(ctx context.Context, git releaseAncestry, tag, boundary string) (string, bool, error) {
	released, err := git.ResolveCommit(ctx, "refs/tags/"+tag)
	if err != nil {
		isWritten, existsErr := git.TagExists(ctx, tag)
		if existsErr == nil && !isWritten && ctx.Err() == nil {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading the commit of %s: %w", tag, err)
	}
	isBehind, err := git.IsAncestor(ctx, released, boundary)
	if err != nil {
		return "", false, fmt.Errorf("comparing %s at %s with the consumer's baseline %s: %w", tag, released, boundary, err)
	}
	return released, isBehind, nil
}

// isPublished reports whether a package's result is a publication.
func isPublished(result *release.Result) bool {
	return result != nil && result.Status == release.StatusPublished
}

// selectRepositoryGit is the Git client of the repository a package lives
// in: the recorder's own in a composed workspace, the run's otherwise.
func (a *App) selectRepositoryGit(repository string, fleet *workspaceRecorder) *gitx.LocalGitx {
	if fleet != nil {
		if record := fleet.byName[repository]; record != nil {
			return record.git
		}
	}
	return a.git
}
