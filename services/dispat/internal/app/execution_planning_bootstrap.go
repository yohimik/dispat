package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func (a *App) pinnedTagSources() ([]execution.ChoreographyTagSource, error) {
	roots := a.planningRealRoots()
	out := make([]execution.ChoreographyTagSource, pinnedPlanningInitialEntryCount, len(a.pinnedNativeRefs))
	for _, ref := range a.pinnedNativeRefs {
		root, exists := roots[ref.Repository]
		if !exists || root == pinnedPlanningAbsentText {
			return nil, fmt.Errorf(pinnedPlanningTagSourceAbsentErrorFormat, ref.Name, ref.Repository)
		}
		root, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf(pinnedPlanningTagAbsoluteSourceAbsentErrorFormat, ref.Name, err)
		}
		out = append(out, execution.ChoreographyTagSource{
			Repository: ref.Repository, Name: ref.Name, Object: ref.Object, Peeled: ref.Peeled,
			SourceDir: root,
		})
	}
	return out, nil
}

func (a *App) prepareRunChoreographyBootstrap(ctx context.Context,
	coordinator *execution.Coordinator) error {
	tags, err := a.pinnedTagSources()
	if err != nil {
		return err
	}
	sources := execution.CoordinatorBootstrapSources{TagSources: tags}
	if coordinator == nil || coordinator.PinnedInputs == nil {
		return fmt.Errorf(pinnedPlanningRunInputsAbsentErrorText)
	}
	if anchor := coordinator.PinnedInputs.SinceAnchor; anchor != nil {
		root, exists := a.planningRealRoots()[anchor.Repository]
		if !exists || root == pinnedPlanningAbsentText {
			return fmt.Errorf(pinnedPlanningSinceAnchorRepositoryAbsentErrorText)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return fmt.Errorf(pinnedPlanningSinceAnchorAbsoluteRepositoryAbsentErrorFormat, err)
		}
		sources.SinceSourceDir = root
	}
	if err := coordinator.PrepareChoreographyBootstrap(ctx, sources); err != nil {
		return err
	}
	if a.returnJournal != nil {
		return a.returnJournal.recordBootstrap(coordinator.BootstrapOID, coordinator.Generation)
	}
	return nil
}
