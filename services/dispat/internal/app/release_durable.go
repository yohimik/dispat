// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// A single history's record of each package, pushed after the publish command
// succeeds and before dependent work (CCME SPEC.md §19.1, §19.4).
//
// The release bound holds only for a run that records every completed task in
// the shared history as it completes (§13.11). A single history used to reach
// its remote once, at the end of the run, so a run stopped after a package
// published and before that push left the publication recorded in a checkout
// a CI runner was about to delete. A receipt closes that window:
//
//   - With no release commit, a receipt is pushed before the local tag is
//     created; the tag is then pushed and the receipt removed.
//   - With a release commit, the tag can only be written on that commit, at
//     the end of the run. A receipt is pushed in its place the instant the
//     publish command succeeds, and finalize atomically removes it while the
//     branch and tag are pushed.
//
// The next run reads receipts under the release lock, before any package task.
// A receipt is a publication with its record pending: no retry republishes it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// receiptLedger is what one run knows about receipts: the ones an earlier run
// left that this run finishes, and the ones this run wrote. Both are removed
// once the tag they stand in for is on the remote.
type receiptLedger struct {
	mu sync.Mutex
	// plannedHead is the commit this run was planned on, which a receipt
	// names when the release exports no commit of its own.
	plannedHead string
	// pending is keyed by package: the receipts whose publication this run
	// does not repeat.
	pending map[string]gitx.Receipt
	// held is keyed by tag: every receipt this checkout holds for a release
	// of this run, pending or written by it.
	held     map[string]gitx.Receipt
	prepared map[string]gitx.PreparedRelease
}

func newReceiptLedger(plannedHead string) *receiptLedger {
	return &receiptLedger{plannedHead: plannedHead,
		pending: make(map[string]gitx.Receipt), held: make(map[string]gitx.Receipt),
		prepared: make(map[string]gitx.PreparedRelease)}
}

// pendingIntegrity answers the identity a retry must reproduce before it can
// finish a prior publication in a new release commit. Nil-safe.
func (l *receiptLedger) pendingRecord(rel *plan.Release) (release.PendingRecord, bool) {
	if l == nil {
		return release.PendingRecord{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r, isPending := l.pending[rel.Pkg.Name]
	return release.PendingRecord{Integrity: r.Integrity, ExportedCommit: r.ExportedCommit}, isPending
}

// remember records a receipt this run now holds for a release.
func (l *receiptLedger) remember(r gitx.Receipt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held[r.Tag] = r
}

// take hands out the receipt held for a tag, once.
func (l *receiptLedger) take(tag string) (gitx.Receipt, bool) {
	if l == nil {
		return gitx.Receipt{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r, isHeld := l.held[tag]
	delete(l.held, tag)
	return r, isHeld
}

func (l *receiptLedger) heldFor(rels []*plan.Release) []gitx.Receipt {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var receipts []gitx.Receipt
	for _, rel := range rels {
		if r, isHeld := l.held[rel.TagName()]; isHeld {
			receipts = append(receipts, r)
		}
	}
	return receipts
}

func (l *receiptLedger) findHeld(tag string) (gitx.Receipt, bool) {
	if l == nil {
		return gitx.Receipt{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r, isHeld := l.held[tag]
	return r, isHeld
}

func (l *receiptLedger) rememberPrepared(tag string, prepared gitx.PreparedRelease) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prepared[tag] = prepared
}

func (l *receiptLedger) preparedFor(tag string) (gitx.PreparedRelease, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prepared, isHeld := l.prepared[tag]
	return prepared, isHeld
}

// isRecordPushing reports whether a single history writes its release records
// to the remote, at the end of the run or package by package.
func (a *App) isRecordPushing() bool {
	return a.cfg.Commit.IsPushEnabled() || a.cfg.Commit.IsTagPushEnabled()
}

// hasPreparedRemoteReceipt allows only a receipt-backed local finalization to
// pass the ordinary behind-branch guard. Settlement validates every target
// and merges the branch before any package task can start.
func (a *App) hasPreparedRemoteReceipt(ctx context.Context, remote string) (bool, error) {
	if !a.cfg.Commit.IsPushEnabled() {
		return false, nil
	}
	if err := a.git.FetchReceipts(ctx, remote); err != nil {
		return false, err
	}
	receipts, err := a.git.Receipts(ctx)
	if err != nil {
		return false, err
	}
	for _, receipt := range receipts {
		prepared, isHeld, err := a.git.ReadPreparedRelease(ctx, receipt.Tag)
		if err != nil {
			return false, err
		}
		if isHeld && prepared.ReceiptObject == receipt.Object {
			return true, nil
		}
	}
	return false, nil
}

// durableRecorder is the release.DurableRecorderx of a single history: the
// release tag pushed with no release commit, a receipt pushed with one.
type durableRecorder struct {
	app       *App
	remote    string
	ledger    *receiptLedger
	isReceipt bool
}

// newDurableRecorder answers the recorder a single history records through,
// nil when it pushes nothing and leaves the remote to CI.
func (a *App) newDurableRecorder() release.DurableRecorderx {
	switch {
	case a.cfg.Commit.IsPushEnabled():
		return &durableRecorder{app: a, remote: a.pushRemote(), ledger: a.receipts, isReceipt: true}
	case a.cfg.Commit.IsTagPushEnabled():
		return &durableRecorder{app: a, remote: a.pushRemote(), ledger: a.receipts}
	}
	return nil
}

func (d *durableRecorder) RecordDurably(ctx context.Context, rel *plan.Release) error {
	if d.isReceipt {
		return d.pushReceipt(ctx, rel)
	}
	if err := d.pushReceipt(ctx, rel); err != nil {
		return err
	}
	if err := release.CreateReleaseTag(ctx, d.app.git, rel, d.app.cfg.Commit.IsForceEnabled(), d.app.log); err != nil {
		return err
	}
	return d.pushTag(ctx, rel)
}

// pushTag writes the release tag and its aliases to the remote create-only,
// moving aliases forced. The same tag at the same commit is the retry of a
// write whose answer was lost and succeeds; at another commit it is a
// published record this run leaves where it is.
func (d *durableRecorder) pushTag(ctx context.Context, rel *plan.Release) error {
	git, log := d.app.git, d.app.log.With().Str("package", rel.Pkg.Name).Logger()
	receipt, isHeld := d.ledger.findHeld(rel.TagName())
	if !isHeld {
		return fmt.Errorf("%w: %s has no receipt before its tag push", release.ErrRecordNotPushed, rel.TagName())
	}
	target, err := git.ResolveCommit(ctx, "refs/tags/"+rel.TagName())
	if err != nil || target != receipt.Commit {
		return fmt.Errorf("%w: local tag %s differs from its receipt target: %w", release.ErrTagAtOtherCommit,
			rel.TagName(), err)
	}
	object, err := git.TagObject(ctx, rel.TagName())
	if err != nil {
		return err
	}
	if pinnedTarget, err := git.ResolveCommit(ctx, object); err != nil || pinnedTarget != receipt.Commit {
		return fmt.Errorf("%w: pinned tag %s differs from its receipt target: %w",
			release.ErrTagAtOtherCommit, rel.TagName(), err)
	}
	refs := []gitx.ReleaseRef{{Name: rel.TagName(), Object: object}}
	for _, alias := range rel.AliasTags() {
		// An alias that could not be written locally was already reported
		// (W232); pushing a name this checkout does not hold would fail the
		// whole push, the release tag with it.
		if isPresent, err := git.TagExists(ctx, alias.Name); err == nil && isPresent {
			refs = append(refs, gitx.ReleaseRef{Name: alias.Name, IsMoving: alias.Force})
		}
	}
	outcomes, err := git.PushReleaseRefs(ctx, d.remote, refs)
	if err != nil {
		return fmt.Errorf("%w: pushing %s to %s: %w", release.ErrRecordNotPushed, rel.TagName(), gitx.RedactURL(d.remote), err)
	}
	for _, outcome := range outcomes {
		if outcome.Result != gitx.RefAtOtherCommit {
			continue
		}
		if outcome.Name == rel.TagName() {
			return fmt.Errorf("%w: %s on %s is at %s, and this release's tag is not",
				release.ErrTagAtOtherCommit, outcome.Name, gitx.RedactURL(d.remote), outcome.Commit)
		}
		log.Warn().Str("code", plan.CodeAliasTagFailed).Str("tag", outcome.Name).Str("remote", gitx.RedactURL(d.remote)).
			Msg("alias tag is held on the remote at another commit, left as it is")
	}
	log.Info().Str("tag", rel.TagName()).Str("remote", gitx.RedactURL(d.remote)).Msg("pushed release tag")
	stored, err := git.RemoteTagCommit(ctx, d.remote, rel.TagName())
	if err != nil || stored != receipt.Commit {
		return fmt.Errorf("%w: remote tag %s does not name the receipt target: %w", release.ErrRecordNotPushed,
			rel.TagName(), err)
	}
	if r, isHeld := d.ledger.take(rel.TagName()); isHeld {
		d.app.removeReceipt(ctx, d.remote, r)
	}
	return nil
}

// pushReceipt writes and pushes the receipt that stands in for the release tag
// until the release commit exists.
func (d *durableRecorder) pushReceipt(ctx context.Context, rel *plan.Release) error {
	git := d.app.git
	exported := rel.ExportedCommit()
	commit := exported
	if exported == "" {
		commit = d.ledger.plannedHead
	} else {
		var err error
		commit, err = git.ResolveCommit(ctx, exported)
		if err != nil {
			return fmt.Errorf("%w: resolving the exported tag target for %s: %w",
				release.ErrRecordNotPushed, rel.TagName(), err)
		}
		exported = commit
	}
	integrity, _ := rel.Output(release.IntegrityOutput)
	r, err := git.WriteReceipt(ctx, gitx.Receipt{Tag: rel.TagName(), Package: rel.Pkg.Name,
		Version: rel.Next.String(), Commit: commit, PlannedHead: d.ledger.plannedHead,
		ExportedCommit: exported,
		Integrity:      strings.TrimSpace(integrity)})
	if err != nil {
		return fmt.Errorf("%w: writing the receipt for %s: %w", release.ErrRecordNotPushed, rel.TagName(), err)
	}
	d.ledger.remember(r)
	outcome, err := git.PushReceipt(ctx, d.remote, r)
	if err != nil {
		return fmt.Errorf("%w: pushing the receipt for %s to %s: %w",
			release.ErrRecordNotPushed, rel.TagName(), gitx.RedactURL(d.remote), err)
	}
	if outcome.Result == gitx.RefAtOtherCommit {
		return fmt.Errorf("%w: a receipt for %s on %s names %s, and this release's does not",
			release.ErrTagAtOtherCommit, rel.TagName(), gitx.RedactURL(d.remote), outcome.Commit)
	}
	d.app.log.Info().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Str("remote", gitx.RedactURL(d.remote)).
		Msg("pushed publication receipt; the release tag follows the release commit")
	return nil
}

// removeReceipt deletes a receipt whose tag is on the remote. A failure only
// warns: the next run finds the tag beside it and removes it then.
func (a *App) removeReceipt(ctx context.Context, remote string, r gitx.Receipt) {
	if err := a.git.DeleteReceipt(ctx, remote, r); err != nil {
		a.log.Warn().Err(err).Str("tag", r.Tag).Str("remote", gitx.RedactURL(remote)).
			Msg("the release tag is on the remote and its receipt could not be removed; the next run removes it")
		return
	}
	a.log.Debug().Str("tag", r.Tag).Msg("publication receipt removed")
}

// consumeReceipts removes the receipts of the releases finalize has just
// pushed, leaving the ones whose tag the remote holds at another commit.
func (a *App) consumeReceipts(ctx context.Context, remote string, rels []*plan.Release, report gitx.PushReport) {
	conflicted := make(map[string]bool, len(report.Conflicts))
	for _, conflict := range report.Conflicts {
		conflicted[conflict.Name] = true
	}
	for _, rel := range rels {
		if conflicted[rel.TagName()] {
			continue
		}
		if r, isHeld := a.receipts.take(rel.TagName()); isHeld {
			a.removeReceipt(ctx, remote, r)
			if err := a.git.DeletePreparedRelease(ctx, rel.TagName()); err != nil {
				a.log.Warn().Err(err).Str("tag", rel.TagName()).Msg("could not remove local prepared release")
			}
		}
	}
}

// settleReceipts reads the remote's receipts under the release lock and
// settles each one against the plan, answering the plan the run executes.
//
//   - A receipt whose tag this checkout holds is left over from a run whose
//     record finished; it is removed once the remote holds the tag as well.
//   - A receipt on this run's head whose package the plan releases at the
//     receipt's version is pending: the run publishes nothing for it and
//     records it as it records the rest.
//   - Any other receipt on a commit the head reaches is recorded where it was
//     planned: its release tag is written at that commit and pushed, and the
//     plan is computed again from that record.
//   - A receipt on a commit the head does not reach cannot affect this plan
//     and is left alone.
func (a *App) settleReceipts(ctx context.Context, opts ReleaseOptions, pl *plan.Plan) (*plan.Plan, error) {
	if !a.isRecordPushing() {
		return pl, nil
	}
	head, err := a.git.HeadSHA(ctx)
	if err != nil {
		return nil, err
	}
	a.receipts = newReceiptLedger(head)
	remote := a.pushRemote()
	if err := a.git.FetchReceipts(ctx, remote); err != nil {
		return nil, fmt.Errorf("publication receipts could not be read from the authoritative remote: %w", err)
	}
	receipts, err := a.git.Receipts(ctx)
	if err != nil {
		return nil, err
	}
	if isResumed, err := a.resumePreparedReleases(ctx, remote, head, receipts); err != nil {
		return nil, err
	} else if isResumed {
		if err := a.git.FetchReceipts(ctx, remote); err != nil {
			return nil, err
		}
		receipts, err = a.git.Receipts(ctx)
		if err != nil {
			return nil, err
		}
		pl, err = a.selectedPlan(ctx, opts)
		if err != nil {
			return nil, err
		}
	}
	for len(receipts) > 0 {
		var isReplanned bool
		receipts, isReplanned, err = a.settleReceiptsOnce(ctx, pl, head, remote, receipts)
		if err != nil {
			return nil, err
		}
		if !isReplanned {
			return pl, nil
		}
		if pl, err = a.selectedPlan(ctx, opts); err != nil {
			return nil, err
		}
	}
	return pl, nil
}

// resumePreparedReleases retries the exact local finalization bound to the
// receipts before its tags were created. All prepared tags for one branch head
// travel in one atomic push; no later commit is inferred from a tag name.
func (a *App) resumePreparedReleases(ctx context.Context, remote, head string, receipts []gitx.Receipt) (bool, error) {
	if !a.cfg.Commit.IsPushEnabled() {
		return false, nil
	}
	var prepared []gitx.Receipt
	var groupHead string
	for _, r := range receipts {
		marker, isHeld, err := a.git.ReadPreparedRelease(ctx, r.Tag)
		if err != nil {
			return false, err
		}
		if !isHeld {
			continue
		}
		reachable, err := a.git.IsAncestor(ctx, marker.BranchHead, head)
		if err != nil || !reachable || marker.ReceiptObject != r.Object ||
			(groupHead != "" && groupHead != r.PlannedHead) {
			return false, fmt.Errorf("prepared release %s does not match its receipt or current branch", r.Tag)
		}
		want := r.ExportedCommit
		if want == "" {
			want = marker.BranchHead
		}
		want, err = a.git.ResolveCommit(ctx, want)
		if err != nil || marker.TagTarget != want {
			return false, fmt.Errorf("prepared release %s has a different tag target: %w", r.Tag, err)
		}
		groupHead = r.PlannedHead
		prepared = append(prepared, r)
	}
	if len(prepared) == 0 {
		return false, nil
	}
	for _, r := range receipts {
		if r.PlannedHead != groupHead {
			continue
		}
		marker, isHeld, err := a.git.ReadPreparedRelease(ctx, r.Tag)
		if err != nil {
			return false, err
		}
		_ = marker
		if isHeld {
			continue
		}
		for _, preparedReceipt := range prepared {
			isTagged, err := a.git.TagExists(ctx, preparedReceipt.Tag)
			if err != nil || isTagged {
				return false, fmt.Errorf("prepared release group has a tag before all markers were written: %w", err)
			}
		}
		for _, preparedReceipt := range prepared {
			if err := a.git.DeletePreparedRelease(ctx, preparedReceipt.Tag); err != nil {
				return false, err
			}
		}
		return false, nil // no local tag exists; §19.4's original-target fallback applies
	}
	refs := make([]gitx.ReleaseRef, 0, len(prepared))
	for _, r := range prepared {
		marker, _, err := a.git.ReadPreparedRelease(ctx, r.Tag)
		if err != nil {
			return false, err
		}
		isTagged, err := a.git.TagExists(ctx, r.Tag)
		if err != nil {
			return false, err
		}
		if !isTagged {
			if err := a.git.CreateTag(ctx, r.Tag, "release "+r.Tag, marker.TagTarget); err != nil {
				return false, err
			}
		}
		target, err := a.git.ResolveCommit(ctx, "refs/tags/"+r.Tag)
		if err != nil || target != marker.TagTarget {
			return false, fmt.Errorf("local tag %s differs from prepared target: %w", r.Tag, err)
		}
		refs = append(refs, gitx.ReleaseRef{Name: r.Tag})
	}
	for attempt := 1; ; attempt++ {
		_, err := a.git.PushWithReceipts(ctx, remote, refs, prepared)
		if err == nil {
			break
		}
		if !errors.Is(err, gitx.ErrRejected) || attempt >= mergeAttempts {
			return false, fmt.Errorf("retrying prepared release atomically: %w", err)
		}
		branch, branchErr := a.git.CurrentBranch(ctx)
		if branchErr != nil {
			return false, branchErr
		}
		tags := make([]string, 0, len(refs))
		for _, ref := range refs {
			tags = append(tags, ref.Name)
		}
		if mergeErr := a.git.MergeRemote(ctx, remote, branch,
			mergeMessage(remote, branch, head, tags)); mergeErr != nil {
			return false, fmt.Errorf("prepared release's remote branch moved and could not be merged: %w", mergeErr)
		}
	}
	for _, r := range prepared {
		a.removeReceipt(ctx, remote, r)
		if err := a.git.DeletePreparedRelease(ctx, r.Tag); err != nil {
			a.log.Warn().Err(err).Str("tag", r.Tag).Msg("could not remove local prepared release")
		}
	}
	return true, nil
}

// settleReceiptsOnce settles every receipt it can against one plan, and
// answers the pending ones, which the next plan has to confirm, and whether a
// record it wrote calls for that plan.
func (a *App) settleReceiptsOnce(ctx context.Context, pl *plan.Plan, head, remote string,
	receipts []gitx.Receipt) ([]gitx.Receipt, bool, error) {
	a.receipts.mu.Lock()
	clear(a.receipts.pending)
	a.receipts.mu.Unlock()
	var pending []gitx.Receipt
	isReplanned := false
	for _, r := range receipts {
		log := a.log.With().Str("package", r.Package).Str("tag", r.Tag).Logger()
		isTagged, err := a.git.TagExists(ctx, r.Tag)
		if err != nil {
			return nil, false, err
		}
		if isTagged {
			if err := a.settleTaggedReceipt(ctx, remote, r, log); err != nil {
				return nil, false, err
			}
			continue
		}
		if r.PlannedHead == head && r.Integrity != "" && isReceiptPlanned(pl, r) {
			a.receipts.mu.Lock()
			a.receipts.pending[r.Package] = r
			a.receipts.held[r.Tag] = r
			a.receipts.mu.Unlock()
			pending = append(pending, r)
			continue
		}
		isReachable, err := a.git.IsReachableFromHead(ctx, r.PlannedHead)
		if err != nil {
			return nil, false, err
		}
		if !isReachable {
			log.Warn().Str("commit", r.Commit).
				Msg("a receipt names a commit this checkout's head does not reach; left as it is")
			continue
		}
		if err := a.recordReceiptAtCommit(ctx, remote, r, log); err != nil {
			return nil, false, err
		}
		isReplanned = true
	}
	if !isReplanned {
		return nil, false, nil
	}
	return pending, true, nil
}

// settleTaggedReceipt removes a receipt whose release tag this checkout holds,
// once the remote holds that tag too. A finalize whose push failed leaves the
// tag here and the receipt there, and removing the receipt then would leave
// the remote with neither, so the tag is pushed first. A tag that cannot be
// pushed keeps its receipt, with a warning.
func (a *App) settleTaggedReceipt(ctx context.Context, remote string, r gitx.Receipt, log zerolog.Logger) error {
	if a.cfg.Commit.IsPushEnabled() {
		return fmt.Errorf("local tag %s has no prepared finalization matching its receipt; receipt kept", r.Tag)
	}
	local, err := a.git.ResolveCommit(ctx, "refs/tags/"+r.Tag)
	if err != nil {
		return fmt.Errorf("checking the local tag for receipt %s: %w", r.Tag, err)
	}
	if local != r.Commit {
		return fmt.Errorf("the local tag %s names %s, not its publication receipt's commit %s", r.Tag, local, r.Commit)
	}
	stored, err := a.git.RemoteTagCommit(ctx, remote, r.Tag)
	if err == nil && stored == "" {
		object, objectErr := a.git.TagObject(ctx, r.Tag)
		if objectErr != nil {
			return objectErr
		}
		if pinnedTarget, err := a.git.ResolveCommit(ctx, object); err != nil || pinnedTarget != r.Commit {
			return fmt.Errorf("pinned local tag %s differs from its receipt target: %w", r.Tag, err)
		}
		var outcomes []gitx.RefOutcome
		outcomes, err = a.git.PushReleaseRefs(ctx, remote, []gitx.ReleaseRef{{Name: r.Tag, Object: object}})
		if err == nil && len(outcomes) == 1 && outcomes[0].Result == gitx.RefAtOtherCommit {
			err = fmt.Errorf("%w: %s on %s is at %s", release.ErrTagAtOtherCommit, r.Tag, gitx.RedactURL(remote), outcomes[0].Commit)
		}
		if err == nil {
			log.Info().Str("remote", gitx.RedactURL(remote)).Msg("pushed a release tag an earlier run left in this checkout")
		}
	}
	if err != nil {
		return fmt.Errorf("the release tag for receipt %s is not on the remote; receipt kept: %w", r.Tag, err)
	}
	stored, err = a.git.RemoteTagCommit(ctx, remote, r.Tag)
	if err != nil {
		return err
	}
	if stored != r.Commit {
		return fmt.Errorf("the remote tag %s names %s, not its publication receipt's commit %s", r.Tag, stored, r.Commit)
	}
	a.removeReceipt(ctx, remote, r)
	return nil
}

// isReceiptPlanned reports whether the plan releases the receipt's package at
// the receipt's version under the receipt's tag.
func isReceiptPlanned(pl *plan.Plan, r gitx.Receipt) bool {
	rel, isPlanned := pl.Releases[r.Package]
	return isPlanned && rel.IsReleasing() && rel.TagName() == r.Tag && rel.Next.String() == r.Version
}

// recordReceiptAtCommit finishes an earlier run's record where that run
// planned it: the release tag at the receipt's commit, pushed create-only. The
// release commit that run would have made is not made, so the version has no
// changelog entry of its own, which the warning says.
func (a *App) recordReceiptAtCommit(ctx context.Context, remote string, r gitx.Receipt, log zerolog.Logger) error {
	if err := a.git.CreateTag(ctx, r.Tag, "release "+r.Tag, r.Commit); err != nil {
		return fmt.Errorf("recording %s from its receipt: %w", r.Tag, err)
	}
	object, err := a.git.TagObject(ctx, r.Tag)
	if err != nil {
		return err
	}
	if pinnedTarget, err := a.git.ResolveCommit(ctx, object); err != nil || pinnedTarget != r.Commit {
		return fmt.Errorf("pinned local tag %s differs from its receipt target: %w", r.Tag, err)
	}
	outcomes, err := a.git.PushReleaseRefs(ctx, remote, []gitx.ReleaseRef{{Name: r.Tag, Object: object}})
	if err == nil && len(outcomes) == 1 && outcomes[0].Result == gitx.RefAtOtherCommit {
		err = fmt.Errorf("%w: %s on %s is at %s", release.ErrTagAtOtherCommit, r.Tag, gitx.RedactURL(remote), outcomes[0].Commit)
	}
	if err != nil {
		err = fmt.Errorf("recording %s from its receipt: %w", r.Tag, err)
		log.Error().Err(err).Str("code", release.TagFailureCode(err)).
			Msg("an earlier run's publication could not be recorded on the remote; refusing to plan past it")
		return err
	}
	stored, err := a.git.RemoteTagCommit(ctx, remote, r.Tag)
	if err != nil || stored != r.Commit {
		return fmt.Errorf("recording %s from its receipt: remote tag differs from recorded target: %w", r.Tag, err)
	}
	log.Warn().Str("code", plan.CodeRecordPending).Str("commit", r.Commit).
		Msg("recorded an earlier run's publication at the commit it was planned on; " +
			"the tag names no release commit, so a changelog entry that run did not push is missing")
	a.removeReceipt(ctx, remote, r)
	return nil
}
