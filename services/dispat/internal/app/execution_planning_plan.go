package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// planFromMaterialization runs the ordinary semantic planner over pinned
// checkout bytes and exact Git refs. Only after it has settled the plan does
// it restore package paths to the authoritative worktrees used by execution.
func (a *App) planFromMaterialization(ctx context.Context, pinned *planningMaterialization,
	selection *ReleaseOptions) (*plan.Plan, error) {
	if pinned == nil {
		return nil, fmt.Errorf(pinnedPlanningPrivateInputAbsentErrorText)
	}
	workspace, err := a.workspace.MaterializedRoots(pinned.repositories)
	if err != nil {
		return nil, err
	}
	capturedConfig, err := a.loadPinnedConfigurations(pinned, workspace)
	if err != nil {
		return nil, err
	}
	private := NewWorkspace(pinned.root, capturedConfig, workspace, a.log)
	if a.planningRemotes == nil {
		a.planningRemotes, err = a.capturePlanningRemotes(ctx)
		if err != nil {
			return nil, err
		}
	}
	private.planningRemotes = a.planningRemotes
	private.ignoreTags = append([]string(nil), a.ignoreTags...)
	private.ignoreTagsByRepository = a.ignoreTagsByRepository
	private.runID = a.runID
	var computed *plan.Plan
	if selection == nil {
		computed, err = private.computePlan(ctx)
	} else {
		selected := *selection
		if selected.Filter.Dir != pinnedPlanningAbsentText {
			selected.Filter.Dir, err = rebasePlanningPath(selected.Filter.Dir, a.root, pinned.root)
			if err != nil {
				return nil, err
			}
		}
		computed, err = private.selectedPlan(ctx, selected)
	}
	if err != nil {
		return nil, err
	}
	realRoots := a.planningRealRoots()
	if err := rehomePlanningPackages(private.plannedOptions.Packages, pinned.repositories, realRoots); err != nil {
		return nil, err
	}
	private.plannedOptions.Root = a.root
	for name, history := range private.plannedOptions.Repositories {
		original := realRoots[name]
		if original == pinnedPlanningAbsentText {
			return nil, fmt.Errorf(pinnedPlanningHistoryUnknownRepositoryErrorFormat, name)
		}
		history.Root = original
		history.Git = a.openRepositoryGit(name)
		private.plannedOptions.Repositories[name] = history
	}
	a.plannedOptions = private.plannedOptions
	a.planDigest = private.planDigest
	a.plannedHeads = make(map[string]string, len(pinned.heads))
	for name, head := range pinned.heads {
		a.plannedHeads[name] = head
	}
	a.owedAtHead = private.owedAtHead
	a.discovered = private.discovered
	a.discoveryOnce = sync.Once{}
	a.discoveryOnce.Do(func() {})
	a.pkgsOnce, a.pkgs, a.pkgsErr = sync.Once{}, nil, nil
	return computed, nil
}

func (a *App) planningRealRoots() map[string]string {
	if a.workspace == nil {
		return map[string]string{pinnedPlanningAbsentText: a.root}
	}
	roots := make(map[string]string, len(a.workspace.Repositories))
	for _, repository := range a.workspace.Repositories {
		roots[repository.Name] = repository.Root
	}
	return roots
}

func rehomePlanningPackages(packages []*model.Package, privateRoots, realRoots map[string]string) error {
	spaces := make(map[*model.Space]bool)
	for _, pkg := range packages {
		if pkg == nil {
			continue
		}
		privateRoot, realRoot := privateRoots[pkg.Repository], realRoots[pkg.Repository]
		if privateRoot == pinnedPlanningAbsentText || realRoot == pinnedPlanningAbsentText {
			return fmt.Errorf(pinnedPlanningPackageRepositoryAbsentErrorFormat, pkg.Name)
		}
		var err error
		pkg.Dir, err = rebasePlanningPath(pkg.Dir, privateRoot, realRoot)
		if err != nil {
			return fmt.Errorf(pinnedPlanningPackagePathErrorFormat, pkg.Name, err)
		}
		if pkg.RepoRoot != pinnedPlanningAbsentText {
			pkg.RepoRoot = realRoot
		}
		if pkg.Space != nil && !spaces[pkg.Space] {
			spaceRoot := pkg.Space.Repository
			if spaceRoot == pinnedPlanningAbsentText {
				spaceRoot = pkg.Repository
			}
			pkg.Space.Dir, err = rebasePlanningPath(pkg.Space.Dir, privateRoots[spaceRoot], realRoots[spaceRoot])
			if err != nil {
				return fmt.Errorf(pinnedPlanningPackageSpacePathErrorFormat, pkg.Name, err)
			}
			if pkg.Space.RepoRoot != pinnedPlanningAbsentText {
				pkg.Space.RepoRoot = realRoots[spaceRoot]
			}
			spaces[pkg.Space] = pinnedPlanningSpaceRehomedState
		}
	}
	return nil
}

func rebasePlanningPath(path, sourceRoot, destinationRoot string) (string, error) {
	if sourceRoot == pinnedPlanningAbsentText || destinationRoot == pinnedPlanningAbsentText {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningPathRepositoryRootAbsentErrorText)
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return pinnedPlanningAbsentText, err
		}
		path = absolute
	}
	absoluteRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return pinnedPlanningAbsentText, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return pinnedPlanningAbsentText, err
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return pinnedPlanningAbsentText, err
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relative == pinnedPlanningParentPath || strings.HasPrefix(relative, pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningPathEscapeErrorFormat, path)
	}
	return filepath.Join(destinationRoot, relative), nil
}
