package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// prePlanningSources captures the same participating checkout layout before
// the planner reads it. The post-plan capture must match this commitment.
func (a *App) prePlanningSources(ctx context.Context) ([]execution.Source, error) {
	if a.workspace == nil {
		head, err := a.git.HeadSHA(ctx)
		if err != nil {
			return nil, err
		}
		return []execution.Source{{Path: pinnedPlanningRootRelativePath, Dir: a.root, Head: head}}, nil
	}
	sources := make([]execution.Source, pinnedPlanningInitialEntryCount, len(a.workspace.Repositories))
	for _, repository := range a.workspace.Repositories {
		git := a.openRepositoryGit(repository.Name)
		head, err := git.HeadSHA(ctx)
		if err != nil {
			return nil, fmt.Errorf(pinnedPlanningHeadReadErrorFormat, repository.Name, err)
		}
		sources = append(sources, execution.Source{Name: repository.Name,
			Path: relativeRepositoryPath(a.workspace.ControlRoot, repository.Root),
			Dir:  repository.Root, Head: head})
	}
	orderSourcesByCheckoutDepth(sources)
	return sources, nil
}

func (a *App) capturePrePlanningSources(ctx context.Context) (*execution.PinnedRunSources, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, nil
	}
	sources, err := a.prePlanningSources(ctx)
	if err != nil {
		return nil, err
	}
	return execution.CapturePinnedRunSources(ctx, execution.PinnedSourceCaptureOptions{
		Sources: sources, OpenRepository: a.openRepository, Store: a.git, Log: a.log,
	})
}

func samePinnedPlanningSources(before, after *execution.PinnedRunSources) bool {
	if before == nil || after == nil || len(before.Repositories) != len(after.Repositories) {
		return pinnedPlanningUnmatchedState
	}
	byName := make(map[string]execution.AssignmentRepository, len(before.Repositories))
	for _, repo := range before.Repositories {
		byName[repo.Name] = repo
	}
	for _, repo := range after.Repositories {
		prior, ok := byName[repo.Name]
		if !ok || prior.Path != repo.Path || prior.PlannedHead != repo.PlannedHead ||
			before.Trees[repo.Name] == pinnedPlanningAbsentText || before.Trees[repo.Name] != after.Trees[repo.Name] {
			return pinnedPlanningUnmatchedState
		}
	}
	return pinnedPlanningMatchedState
}

// planningSources is the complete participating workspace, ordered by
// checkout nesting. A task later receives only its declared input closure;
// independent planning needs the configuration of every participant.
func (a *App) planningSources() ([]execution.Source, string, error) {
	if a.workspace == nil {
		head := a.plannedHeads[pinnedPlanningAbsentText]
		if head == pinnedPlanningAbsentText {
			return nil, pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningSingleHistoryHeadAbsentErrorText)
		}
		return []execution.Source{{Path: pinnedPlanningRootRelativePath, Dir: a.root, Head: head}}, pinnedPlanningAbsentText, nil
	}
	sources := make([]execution.Source, pinnedPlanningInitialEntryCount, len(a.workspace.Repositories))
	rootRepository := pinnedPlanningAbsentText
	for _, repository := range a.workspace.Repositories {
		head := a.plannedHeads[repository.Name]
		if head == pinnedPlanningAbsentText {
			return nil, pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningRepositoryHeadAbsentErrorFormat, repository.Name)
		}
		sources = append(sources, execution.Source{
			Name: repository.Name, Path: relativeRepositoryPath(a.workspace.ControlRoot, repository.Root),
			Dir: repository.Root, Head: head,
		})
		if repository.Root == a.workspace.ControlRoot {
			rootRepository = repository.Name
		}
	}
	if rootRepository == pinnedPlanningAbsentText {
		return nil, pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningWorkspaceRootAbsentErrorText)
	}
	orderSourcesByCheckoutDepth(sources)
	return sources, rootRepository, nil
}

func (a *App) capturePinnedRunSources(ctx context.Context, pl *plan.Plan) (*execution.PinnedRunSources, string, error) {
	if !a.cfg.Execution.IsDistributed() {
		return nil, pinnedPlanningAbsentText, nil
	}
	sources, rootRepository, err := a.planningSources()
	if err != nil {
		return nil, pinnedPlanningAbsentText, err
	}
	outputRoots, _, err := a.dispatchOutputRoots(pl, a.resolveInputSources(pl))
	if err != nil {
		return nil, pinnedPlanningAbsentText, err
	}
	pinned, err := execution.CapturePinnedRunSources(ctx, execution.PinnedSourceCaptureOptions{
		Sources: sources, OutputRoots: outputRoots, OpenRepository: a.openRepository,
		Store: a.git, Log: a.log,
	})
	if err != nil {
		return nil, pinnedPlanningAbsentText, err
	}
	return pinned, rootRepository, nil
}
