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
		(request.Kind != "publish" && request.Kind != "record") {
		return execution.ChoreographyLocalEffectEvidence{}, fmt.Errorf("execution: incomplete release effect identity")
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
	observation := "failed"
	if request.Succeeded {
		observation = "succeeded"
	}
	evidence := execution.ChoreographyLocalEffectEvidence{
		Operation: request.Kind, Repository: repository, Package: rel.Pkg.Name,
		Version: rel.Next.String(), Tag: rel.TagName(), Attempt: 1,
		PlanDigest: c.PlanDigest, RecordingMode: mode, Observation: observation,
	}
	if !request.Succeeded {
		return evidence, nil
	}
	if mode == releaseEffectDeferredFinalizeMode {
		if a.returnJournal == nil {
			return evidence, fmt.Errorf("execution: deferred release tag %s has no protected run journal", rel.TagName())
		}
		if err := a.returnJournal.expectDeferredTag(rel.TagName()); err != nil {
			return evidence, err
		}
	}
	if receipt, held := a.receipts.findHeld(rel.TagName()); held {
		if receipt.Package != rel.Pkg.Name || receipt.Tag != rel.TagName() ||
			receipt.Object == "" || receipt.Commit == "" {
			return evidence, fmt.Errorf("execution: held publication receipt differs from %s", rel.TagName())
		}
		evidence.ReceiptObject, evidence.ReceiptCommit = receipt.Object, receipt.Commit
		if prepared, ok := a.receipts.preparedFor(rel.TagName()); ok {
			if prepared.ReceiptObject != receipt.Object {
				return evidence, fmt.Errorf("execution: prepared release differs from held receipt for %s", rel.TagName())
			}
			evidence.PreparedHead, evidence.PreparedTarget = prepared.BranchHead, prepared.TagTarget
		}
	}
	if mode == releaseEffectDeferredFinalizeMode && commit.IsPushEnabled() &&
		(evidence.ReceiptObject == "" || evidence.ReceiptCommit == "") {
		return evidence, fmt.Errorf("execution: deferred pushed release %s has no exact durable publication receipt",
			rel.TagName())
	}
	git := a.openRepositoryGit(repository)
	if mode == "tag-push" || (mode == "commit-push" && evidence.ReceiptObject == "") {
		remote := commitRemote(commit)
		object, err := git.RemoteTagObject(ctx, remote, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf("execution: reading exact pushed tag %s: %w", rel.TagName(), err)
		}
		target, err := git.RemoteTagCommit(ctx, remote, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf("execution: reading pushed tag target %s: %w", rel.TagName(), err)
		}
		if object != "" && target != "" {
			evidence.TagObject, evidence.TagTarget = object, target
		}
	}
	if mode == "local-only" && evidence.ReceiptObject == "" {
		object, err := git.TagObject(ctx, rel.TagName())
		if err != nil {
			return evidence, fmt.Errorf("execution: reading local release tag %s: %w", rel.TagName(), err)
		}
		if object != "" {
			target, err := git.ResolveCommit(ctx, rel.TagName())
			if err != nil {
				return evidence, fmt.Errorf("execution: reading local release target %s: %w", rel.TagName(), err)
			}
			evidence.TagObject, evidence.TagTarget = object, target
		}
	}
	if mode != "local-only" && mode != releaseEffectDeferredFinalizeMode &&
		request.Outcome != release.OutcomeRecordBlocked &&
		evidence.ReceiptObject == "" && evidence.TagObject == "" {
		return evidence, fmt.Errorf("execution: successful pushed release %s has no exact native record", rel.TagName())
	}
	return evidence, nil
}

func (a *App) releaseCommitConfig(repository string) (*config.CommitConfig, error) {
	commit := a.cfg.Commit
	if a.workspace != nil {
		owner := a.workspace.RepositoryByName(repository)
		if owner == nil {
			return nil, fmt.Errorf("execution: release effect has no source repository %q", repository)
		}
		commit = owner.Commit
	}
	return commit, nil
}

func releaseRecordingMode(commit *config.CommitConfig) string {
	if commit.IsPushEnabled() {
		return "commit-push"
	}
	if commit.IsTagPushEnabled() {
		return "tag-push"
	}
	return "local-only"
}
