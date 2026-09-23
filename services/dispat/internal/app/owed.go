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
	head, headErr := a.resolvePlanHeads(ctx, pl)
	pairs := pl.OwedAtHead(head)
	if err := headErr(); err != nil {
		a.log.Error().Err(err).Msg("cannot read the head the plan releases from")
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

// resolvePlanHeads answers each repository's head as the plan read it: the
// snapshot a composed plan records, and in one history the checkout's HEAD,
// read once and only when some pair needs it. The second function reports a
// failed read.
func (a *App) resolvePlanHeads(ctx context.Context, pl *plan.Plan) (func(string) string, func() error) {
	var singleHead string
	var readErr error
	isRead := false
	head := func(repository string) string {
		if a.workspace != nil {
			if sha, ok := pl.RepositoryHeads[repository]; ok {
				return sha
			}
			for name, sha := range pl.RepositoryHeads {
				if globx.Fold(name) == globx.Fold(repository) {
					return sha
				}
			}
			return ""
		}
		if !isRead {
			isRead = true
			singleHead, readErr = a.git.HeadSHA(ctx)
		}
		return singleHead
	}
	return head, func() error { return readErr }
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
// Each such pair is a critical, so the run fails naming it.
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
			released, err := git.ResolveCommit(ctx, "refs/tags/"+provider.TagName())
			if err != nil {
				continue // a tag that was never written is reported where it failed
			}
			isBehind, err := git.IsAncestor(ctx, released, boundary)
			if err != nil {
				a.log.Warn().Err(err).Str("package", consumerName).Str("provider", providerName).
					Msg("cannot compare the provider's release commit with the consumer's baseline")
				continue
			}
			if !isBehind {
				continue
			}
			err = fmt.Errorf("%s was released at %s, which %s's baseline %s reaches, and %s did not release after it",
				provider.TagName(), released, consumerName, boundary, consumerName)
			crit.record(a.log, plan.CodeOwedAtBaseline, err,
				"a consumer did not publish after its provider was released at its baseline commit",
				func(e *zerolog.Event) *zerolog.Event {
					return e.Str("package", consumerName).Str("provider", providerName).Str("commit", released).
						Str("remedy", fmt.Sprintf("commit release(%s) with the footer Release-As: %s",
							consumerName, consumer.Next.String()))
				})
		}
	}
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
