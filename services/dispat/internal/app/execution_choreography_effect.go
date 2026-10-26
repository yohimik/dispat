package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type releaseEffectRequest struct {
	Coordinator *execution.Coordinator
	Task        string
	Kind        string
	Release     *plan.Release
	Succeeded   bool
	Outcome     release.PackageOutcome
}

const releaseEffectDeferredFinalizeMode = "deferred-finalize"

// releaseEffectEvidence is made only after Executor has finished the native
// publication/recording tail. The command result is the first node's signed
// observation; a configured pushed record additionally names the exact Git
// objects that this run can still verify before releasing graph successors.
func (a *App) releaseEffectEvidence(ctx context.Context,
	request releaseEffectRequest) (execution.ChoreographyLocalEffectEvidence, error) {
	c, rel := request.Coordinator, request.Release
	if a == nil || c == nil || rel == nil || rel.Pkg == nil ||
		(request.Kind != choreographyPublishStageName && request.Kind != choreographyRecordStageName) {
		return execution.ChoreographyLocalEffectEvidence{}, fmt.Errorf(choreographyIncompleteEffectIdentityErrorText)
	}
	repository := rel.Pkg.Repository
	commit, err := a.releaseCommitConfig(repository)
	if err != nil {
		return execution.ChoreographyLocalEffectEvidence{}, err
	}
	mode := releaseRecordingMode(commit)
	// A single-history release commit writes its tags in finalize, after the
	// publish vertex settles. The publish vertex can attest its command, but
	// cannot name a tag that does not exist yet. Composed workspaces record
	// each source release in the publish tail and retain their native mode.
	if a.workspace == nil && commit.IsEnabled() {
		mode = releaseEffectDeferredFinalizeMode
	}
	observation := choreographyFailedOutcomeName
	if request.Succeeded {
		observation = choreographySucceededOutcomeName
	}
	evidence := execution.ChoreographyLocalEffectEvidence{
		Operation: request.Kind, Repository: repository, Package: rel.Pkg.Name,
		Version: rel.Next.String(), Tag: rel.TagName(), Attempt: choreographyInitialAttemptCount,
		PlanDigest: c.PlanDigest, RecordingMode: mode, Observation: observation,
	}
	if !request.Succeeded {
		return evidence, nil
	}
	if mode == releaseEffectDeferredFinalizeMode {
		if a.returnJournal == nil {
			return evidence, fmt.Errorf(choreographyMissingDeferredTagJournalErrorFormat, rel.TagName())
		}
		if err := a.returnJournal.expectDeferredTag(rel.TagName()); err != nil {
			return evidence, err
		}
	}
	if receipt, held := a.receipts.findHeld(rel.TagName()); held {
		if receipt.Package != rel.Pkg.Name || receipt.Tag != rel.TagName() ||
			receipt.Object == choreographyAbsentIdentityText || receipt.Commit == choreographyAbsentIdentityText {
			return evidence, fmt.Errorf(choreographyHeldReceiptMismatchErrorFormat, rel.TagName())
		}
		evidence.ReceiptObject, evidence.ReceiptCommit = receipt.Object, receipt.Commit
		if prepared, ok := a.receipts.preparedFor(rel.TagName()); ok {
			if prepared.ReceiptObject != receipt.Object {
				return evidence, fmt.Errorf(choreographyPreparedReceiptMismatchErrorFormat, rel.TagName())
			}
			evidence.PreparedHead, evidence.PreparedTarget = prepared.BranchHead, prepared.TagTarget
		}
	}
	if mode == releaseEffectDeferredFinalizeMode && commit.IsPushEnabled() &&
		(evidence.ReceiptObject == choreographyAbsentIdentityText || evidence.ReceiptCommit == choreographyAbsentIdentityText) {
		return evidence, fmt.Errorf(choreographyMissingDeferredPushReceiptErrorFormat,
			rel.TagName())
	}
	git := a.openRepositoryGit(repository)
	if mode == choreographyTagPushModeName || (mode == choreographyCommitPushModeName && evidence.ReceiptObject == choreographyAbsentIdentityText) {
		remote := commitRemote(commit)
		object, err := git.RemoteTagObject(ctx, remote, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf(choreographyPushedTagReadErrorFormat, rel.TagName(), err)
		}
		target, err := git.RemoteTagCommit(ctx, remote, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf(choreographyPushedTargetReadErrorFormat, rel.TagName(), err)
		}
		if object != choreographyAbsentIdentityText && target != choreographyAbsentIdentityText {
			evidence.TagObject, evidence.TagTarget = object, target
		}
	}
	if mode == choreographyLocalRecordingModeName && evidence.ReceiptObject == choreographyAbsentIdentityText {
		object, err := git.TagObject(ctx, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf(choreographyLocalTagReadErrorFormat, rel.TagName(), err)
		}
		if object != choreographyAbsentIdentityText {
			target, err := git.ResolveCommit(ctx, rel.TagName())
			if err != nil {
				return evidence, fmt.Errorf(choreographyLocalTargetReadErrorFormat, rel.TagName(), err)
			}
			evidence.TagObject, evidence.TagTarget = object, target
		}
	}
	if mode != choreographyLocalRecordingModeName && mode != releaseEffectDeferredFinalizeMode &&
		request.Outcome != release.OutcomeRecordBlocked &&
		evidence.ReceiptObject == choreographyAbsentIdentityText && evidence.TagObject == choreographyAbsentIdentityText {
		return evidence, fmt.Errorf(choreographyMissingNativeRecordErrorFormat, rel.TagName())
	}
	return evidence, nil
}

func (a *App) releaseCommitConfig(repository string) (*config.CommitConfig, error) {
	commit := a.cfg.Commit
	if a.workspace != nil {
		owner := a.workspace.RepositoryByName(repository)
		if owner == nil {
			return nil, fmt.Errorf(choreographyMissingEffectRepositoryErrorFormat, repository)
		}
		commit = owner.Commit
	}
	return commit, nil
}

func releaseRecordingMode(commit *config.CommitConfig) string {
	if commit.IsPushEnabled() {
		return choreographyCommitPushModeName
	}
	if commit.IsTagPushEnabled() {
		return choreographyTagPushModeName
	}
	return choreographyLocalRecordingModeName
}
