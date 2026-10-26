package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

type planningRefCapture struct {
	name     string
	git      *gitx.LocalGitx
	expected gitx.TagSnapshot
}

// capturePlanningRefs pins the full authoritative tag inventory before
// planning. Selecting namespaces from discovered packages here would make
// independent package discovery depend on an unpinned live checkout.
func (a *App) capturePlanningRefs(ctx context.Context, fleet *workspaceRecorder) ([]planningRefCapture, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	if fleet == nil {
		if a.workspace != nil {
			captured := make([]planningRefCapture, pinnedPlanningInitialEntryCount, len(a.workspace.Repositories))
			for _, repository := range a.workspace.Repositories {
				git := a.openRepositoryGit(repository.Name)
				refs, err := git.AllTagSnapshot(ctx)
				if err != nil {
					return nil, fmt.Errorf(pinnedPlanningRefsReadErrorFormat, repository.Name, err)
				}
				captured = append(captured, planningRefCapture{
					name: repository.Name, git: git, expected: refs,
				})
			}
			return captured, nil
		}
		refs, err := a.git.AllTagSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		return []planningRefCapture{{git: a.git, expected: refs}}, nil
	}
	captured := make([]planningRefCapture, pinnedPlanningInitialEntryCount, len(fleet.ordered))
	for _, record := range fleet.ordered {
		refs, err := record.git.AllTagSnapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf(pinnedPlanningRefsReadErrorFormat, record.repo.Name, err)
		}
		captured = append(captured, planningRefCapture{name: record.repo.Name, git: record.git,
			expected: refs})
	}
	return captured, nil
}

func (a *App) finishPlanningRefs(ctx context.Context, captures []planningRefCapture) error {
	if len(captures) == pinnedPlanningEmptyEntryCount {
		return nil
	}
	var refs []execution.PinnedNativeRef
	for _, capture := range captures {
		current, err := capture.git.AllTagSnapshot(ctx)
		if err != nil {
			return fmt.Errorf(pinnedPlanningRefsRereadErrorFormat, capture.name, err)
		}
		if !samePlanningRefs(capture.expected, current) {
			return fmt.Errorf(pinnedPlanningRefsChangedErrorFormat, capture.name)
		}
		for name, ref := range capture.expected {
			refs = append(refs, execution.PinnedNativeRef{
				Repository: capture.name, Name: name, Object: ref.Object, Peeled: ref.Peeled,
			})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Repository != refs[j].Repository {
			return refs[i].Repository < refs[j].Repository
		}
		return refs[i].Name < refs[j].Name
	})
	a.pinnedNativeRefs = refs
	return nil
}

func samePlanningRefs(x, y gitx.TagSnapshot) bool {
	if len(x) != len(y) {
		return pinnedPlanningUnmatchedState
	}
	for name, ref := range x {
		if y[name] != ref {
			return pinnedPlanningUnmatchedState
		}
	}
	return pinnedPlanningMatchedState
}
