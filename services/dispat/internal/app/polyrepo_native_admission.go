package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

type pendingNativeTransition struct {
	owner string
	token string
}

func (p pendingNativeTransition) Error() string {
	return "native source transition is still in progress"
}

type retryNativeObservation struct{}

func (retryNativeObservation) Error() string {
	return "native journal changed during repository snapshot read"
}

type nativeAdmission struct {
	headReached bool
	exact       map[string]workspaceenv.NativeTagRef
}

func (g *workspaceSnapshotGuard) nativeAdmission(ctx context.Context, record *repositoryRecord, head string) (nativeAdmission, error) {
	result := nativeAdmission{exact: make(map[string]workspaceenv.NativeTagRef)}
	if g.pins == nil {
		return result, nil
	}
	records, err := g.pins.nativeTransitions(record.repo.Name)
	if err != nil {
		return result, snapshotError(record.repo.Name, fmt.Sprintf("reading run-native transitions: %v", err))
	}
	if len(records) == 0 {
		return result, nil
	}
	byPrior := make(map[string][]*workspaceenv.NativeTransitionRecord, len(records))
	for i := range records {
		proof := &records[i]
		if err := g.validateNativeTransition(record.repo.Name, proof.NativeTransition); err != nil {
			return result, err
		}
		byPrior[proof.PriorHead] = append(byPrior[proof.PriorHead], proof)
	}
	cursor := record.expectedHead
	seen := make(map[string]bool)
	for {
		still := make(map[string]workspaceenv.NativeTagRef)
		for _, proof := range byPrior[cursor] {
			switch proof.State {
			case workspaceenv.NativeTransitionPending:
				return result, pendingNativeTransition{owner: record.repo.Name, token: proof.Token}
			case workspaceenv.NativeTransitionFailed:
				return result, snapshotError(record.repo.Name, "a native transition from the accepted HEAD failed")
			case workspaceenv.NativeTransitionCompleted:
				if proof.NewHead == cursor {
					if err := addNativeRefs(still, proof.Tags, false); err != nil {
						return result, snapshotError(record.repo.Name, err.Error())
					}
				}
			}
		}
		if err := addNativeMap(result.exact, still, true); err != nil {
			return result, snapshotError(record.repo.Name, err.Error())
		}
		if cursor == head {
			for _, proof := range byPrior[cursor] {
				if proof.State == workspaceenv.NativeTransitionCompleted && proof.NewHead != cursor {
					return result, snapshotError(record.repo.Name, "live HEAD reverted behind a completed native transition")
				}
			}
			result.headReached = true
			return result, nil
		}
		var next *workspaceenv.NativeTransitionRecord
		for _, proof := range byPrior[cursor] {
			if proof.State != workspaceenv.NativeTransitionCompleted || proof.NewHead == cursor {
				continue
			}
			if next != nil {
				return result, snapshotError(record.repo.Name, "native transitions fork from one accepted HEAD")
			}
			next = proof
		}
		if next == nil {
			return result, nil
		}
		if seen[next.NewHead] {
			return result, snapshotError(record.repo.Name, "native transitions form a HEAD cycle")
		}
		ancestor, err := record.git.IsAncestor(ctx, cursor, next.NewHead)
		if err != nil || !ancestor {
			return result, snapshotError(record.repo.Name, "native transition is not a forward source-history step")
		}
		seen[next.NewHead] = true
		if err := addNativeRefs(result.exact, next.Tags, true); err != nil {
			return result, snapshotError(record.repo.Name, err.Error())
		}
		cursor = next.NewHead
	}
}

func addNativeRefs(dst map[string]workspaceenv.NativeTagRef, refs []workspaceenv.NativeTagRef, ordered bool) error {
	for _, ref := range refs {
		if prior, exists := dst[ref.Name]; exists && prior != ref && (!ordered || !ref.Moving) {
			return fmt.Errorf("native records give conflicting exact objects for tag %s", ref.Name)
		}
		dst[ref.Name] = ref
	}
	return nil
}

func addNativeMap(dst, refs map[string]workspaceenv.NativeTagRef, ordered bool) error {
	for _, ref := range refs {
		if err := addNativeRefs(dst, []workspaceenv.NativeTagRef{ref}, ordered); err != nil {
			return err
		}
	}
	return nil
}

func (g *workspaceSnapshotGuard) validateNativeTransition(owner string, proof workspaceenv.NativeTransition) error {
	rel := g.nativePlan[proof.Package]
	if rel == nil || rel.Pkg == nil || rel.Next.String() != proof.Version || !g.nativeOwners[proof.Package][owner] {
		return snapshotError(owner, "native transition is not in the fixed release plan and owner route")
	}
	if !fullObjectID(proof.PriorHead) || proof.PushRequired && !proof.PushComplete && proof.NewHead != "" {
		return snapshotError(owner, "native transition has an invalid source or incomplete required push")
	}
	switch proof.Kind {
	case workspaceenv.NativeTransitionSourceRecord:
		if owner != rel.Pkg.Repository {
			return snapshotError(owner, "source record names another repository owner")
		}
	case workspaceenv.NativeTransitionLinkSettlement:
		if proof.TagRequested || len(proof.Tags) != 0 {
			return snapshotError(owner, "fleet link settlement contains tag authority")
		}
	default:
		return snapshotError(owner, "native transition has an unknown operation kind")
	}
	if proof.NewHead == "" {
		return nil // Pending and failed attempts do not authorize any Git state.
	}
	if !fullObjectID(proof.NewHead) {
		return snapshotError(owner, "native transition has an invalid recorded HEAD")
	}
	if !proof.TagRequested {
		if len(proof.Tags) != 0 {
			return snapshotError(owner, "tagless native transition contains tag authority")
		}
		return nil
	}
	if owner != rel.Pkg.Repository {
		return snapshotError(owner, "a settlement owner cannot write the package release tag")
	}
	allowed := map[string]bool{rel.TagName(): false}
	for _, alias := range rel.AliasTags() {
		allowed[alias.Name] = alias.Force
	}
	foundRelease := false
	for _, ref := range proof.Tags {
		moving, ok := allowed[ref.Name]
		if !ok || moving != ref.Moving || !fullObjectID(ref.Object) || ref.Commit != proof.NewHead {
			return snapshotError(owner, "native transition names a foreign or mismatched tag object")
		}
		if ref.Name == rel.TagName() {
			foundRelease = true
		}
	}
	if !foundRelease {
		return snapshotError(owner, "native transition omitted its planned release tag")
	}
	return nil
}

func nativeRefMatches(actual gitx.TagRefTarget, admitted workspaceenv.NativeTagRef) bool {
	return actual.Object == admitted.Object && actual.Commit() == admitted.Commit
}

func nativeRefConflicts(name string, actual gitx.TagRefTarget, admitted map[string]workspaceenv.NativeTagRef) bool {
	proof, exists := admitted[name]
	return exists && !nativeRefMatches(actual, proof)
}

func (g *workspaceSnapshotGuard) waitNative(ctx context.Context, pending pendingNativeTransition) error {
	waitCtx, cancel := context.WithTimeout(ctx, release.RecordTimeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, found, err := g.pins.nativeTransition(pending.owner, pending.token)
		if err != nil {
			return snapshotError(pending.owner, fmt.Sprintf("reading pending native transition: %v", err))
		}
		if !found {
			return snapshotError(pending.owner, "pending native transition disappeared")
		}
		switch record.State {
		case workspaceenv.NativeTransitionCompleted:
			return nil
		case workspaceenv.NativeTransitionFailed:
			return snapshotError(pending.owner, "pending native transition failed")
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return snapshotError(pending.owner, "pending native transition did not finish before the record deadline")
			}
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}
