package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// planningMaterialization is a private Git workspace whose HEADs describe
// release history while its indexes and working trees hold the pinned source
// bytes. The snapshot commits themselves never become release history.
type planningMaterialization struct {
	root         string
	repositories map[string]string
	heads        map[string]string
}

type planningMaterializationOptions struct {
	repositories   []execution.AssignmentRepository
	sourceDirs     map[string]string
	refs           []execution.PinnedNativeRef
	rootRepository string
	maxObjectBytes int64
	sinceAnchor    *execution.PinnedSinceAnchor
}

func materializePlanningInputs(ctx context.Context, options planningMaterializationOptions) (_ *planningMaterialization, err error) {
	if len(options.repositories) == pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningRepositoriesAbsentErrorText)
	}
	root, err := os.MkdirTemp(pinnedPlanningAbsentText, pinnedPlanningWorkspaceDirectoryPattern)
	if err != nil {
		return nil, fmt.Errorf(pinnedPlanningWorkspaceCreationErrorFormat, err)
	}
	// Ownership checks compare canonical package paths with repository roots.
	// On systems where the temporary directory has a symlinked prefix, keep
	// both sides of that comparison in the same path namespace.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf(pinnedPlanningWorkspaceResolutionErrorFormat, err)
	}
	root = canonicalRoot
	materialized := &planningMaterialization{root: root,
		repositories: make(map[string]string, len(options.repositories)),
		heads:        make(map[string]string, len(options.repositories))}
	defer func() {
		if err != nil {
			err = errors.Join(err, materialized.close())
		}
	}()

	repositories := append([]execution.AssignmentRepository(nil), options.repositories...)
	sort.Slice(repositories, func(i, j int) bool {
		if len(repositories[i].Path) != len(repositories[j].Path) {
			return len(repositories[i].Path) < len(repositories[j].Path)
		}
		return repositories[i].Path < repositories[j].Path
	})
	refsByRepository := make(map[string][]execution.PinnedNativeRef)
	for _, ref := range options.refs {
		refsByRepository[ref.Repository] = append(refsByRepository[ref.Repository], ref)
	}
	for _, repository := range repositories {
		if _, exists := materialized.repositories[repository.Name]; exists {
			return nil, fmt.Errorf(pinnedPlanningRepositoryRepeatedErrorFormat, repository.Name)
		}
		sourceDir := options.sourceDirs[repository.Name]
		if sourceDir == pinnedPlanningAbsentText || repository.Snapshot == pinnedPlanningAbsentText || repository.PlannedHead == pinnedPlanningAbsentText {
			return nil, fmt.Errorf(pinnedPlanningRepositoryIncompleteErrorFormat, repository.Name)
		}
		sourceDir, err = filepath.Abs(sourceDir)
		if err != nil {
			return nil, fmt.Errorf(pinnedPlanningSourceResolutionErrorFormat, repository.Name, err)
		}
		destination, pathErr := planningRepositoryPath(root, repository.Path)
		if pathErr != nil {
			return nil, fmt.Errorf(pinnedPlanningRepositoryPathErrorFormat, repository.Name, pathErr)
		}
		if initErr := os.MkdirAll(destination, pinnedPlanningPrivateDirectoryMode); initErr != nil {
			return nil, fmt.Errorf(pinnedPlanningRepositoryCreationErrorFormat, repository.Name, initErr)
		}
		if _, initErr := runPlanningGit(ctx, destination, pinnedPlanningInitCommandName, pinnedPlanningQuietFlag); initErr != nil {
			return nil, initErr
		}
		private := &gitx.LocalGitx{Dir: destination}
		if importErr := private.ImportCommitFrom(ctx, sourceDir, repository.Snapshot); importErr != nil {
			return nil, fmt.Errorf(pinnedPlanningSourceImportErrorFormat, repository.Name, importErr)
		}
		if anchor := options.sinceAnchor; anchor != nil && anchor.Repository == repository.Name {
			if anchor.OID == pinnedPlanningAbsentText {
				return nil, fmt.Errorf(pinnedPlanningSinceAnchorCommitAbsentErrorText)
			}
			if importErr := private.ImportCommitFrom(ctx, sourceDir, anchor.OID); importErr != nil {
				return nil, fmt.Errorf(pinnedPlanningSinceAnchorImportErrorFormat, importErr)
			}
		}
		parents, parentErr := private.CommitParents(ctx, repository.Snapshot)
		if parentErr != nil {
			return nil, fmt.Errorf(pinnedPlanningSnapshotParentsUnavailableErrorFormat, repository.Name, parentErr)
		}
		if len(parents) != pinnedPlanningSingleParentCount || parents[pinnedPlanningFirstEntryIndex] != repository.PlannedHead {
			return nil, fmt.Errorf(pinnedPlanningSnapshotParentsMismatchErrorFormat, repository.Name, parents, repository.PlannedHead)
		}
		if _, checkoutErr := runPlanningGit(ctx, destination, pinnedPlanningCheckoutCommandName, pinnedPlanningQuietFlag, pinnedPlanningDetachFlag, repository.Snapshot); checkoutErr != nil {
			return nil, checkoutErr
		}
		// Keep the snapshot's tree and index, but restore HEAD to the semantic
		// history commit. Git history and change windows therefore exclude the
		// transport commit while config discovery reads its exact bytes.
		if _, resetErr := runPlanningGit(ctx, destination, pinnedPlanningResetCommandName, pinnedPlanningQuietFlag, pinnedPlanningKeepSnapshotTreeFlag, repository.PlannedHead); resetErr != nil {
			return nil, resetErr
		}
		for _, ref := range refsByRepository[repository.Name] {
			limit := options.maxObjectBytes
			if limit == pinnedPlanningNoObjectBytesCount {
				limit = public.DefaultExecutionMaxPackBytes
			}
			if refErr := installPlanningRef(ctx, planningRefInstallOptions{destination: destination, source: sourceDir, ref: ref, maxBytes: limit}); refErr != nil {
				return nil, fmt.Errorf(pinnedPlanningRefInstallationErrorFormat, repository.Name, refErr)
			}
		}
		delete(refsByRepository, repository.Name)
		materialized.repositories[repository.Name] = destination
		materialized.heads[repository.Name] = repository.PlannedHead
	}
	if len(refsByRepository) != pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningRefsUnknownRepositoryErrorText)
	}
	if _, exists := materialized.repositories[options.rootRepository]; !exists {
		return nil, fmt.Errorf(pinnedPlanningRootRepositoryAbsentErrorFormat, options.rootRepository)
	}
	if options.sinceAnchor != nil {
		if _, exists := materialized.repositories[options.sinceAnchor.Repository]; !exists {
			return nil, fmt.Errorf(pinnedPlanningSinceAnchorUnknownRepositoryErrorText)
		}
	}
	if err := validatePlanningSymlinks(root); err != nil {
		return nil, err
	}
	return materialized, nil
}

// Every config, ignore and package file the ordinary planner may open lives
// under this private snapshot root. A symlink out of it would turn a captured
// byte read back into an ambient host read.
func validatePlanningSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == pinnedPlanningSymlinkTypeAbsentMask {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf(pinnedPlanningSymlinkResolutionErrorFormat, path, err)
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == pinnedPlanningParentPath || strings.HasPrefix(relative, pinnedPlanningParentPath+string(filepath.Separator)) {
			return fmt.Errorf(pinnedPlanningSymlinkEscapeErrorFormat, path)
		}
		return nil
	})
}

func planningRepositoryPath(root, path string) (string, error) {
	if path == pinnedPlanningAbsentText || filepath.IsAbs(path) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningCheckoutPathInvalidErrorFormat, path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == pinnedPlanningParentPath || strings.HasPrefix(clean, pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningCheckoutPathEscapeErrorText)
	}
	return filepath.Join(root, clean), nil
}

type planningRefInstallOptions struct {
	destination string
	source      string
	ref         execution.PinnedNativeRef
	maxBytes    int64
}

func installPlanningRef(ctx context.Context, options planningRefInstallOptions) error {
	destination, source, ref, maxBytes := options.destination, options.source, options.ref, options.maxBytes
	if ref.Name == pinnedPlanningAbsentText || strings.HasPrefix(ref.Name, pinnedPlanningRefPrefixText) || ref.Object == pinnedPlanningAbsentText {
		return fmt.Errorf(pinnedPlanningTagInvalidErrorFormat, ref.Name)
	}
	fullName := pinnedPlanningTagRefPrefixText + ref.Name
	if _, err := runPlanningGit(ctx, destination, pinnedPlanningValidateRefCommandName, fullName); err != nil {
		return err
	}
	private := &gitx.LocalGitx{Dir: destination}
	if err := private.ImportObjectFrom(ctx, source, ref.Object, maxBytes); err != nil {
		return err
	}
	peeled, err := private.PeelObject(ctx, ref.Object)
	if err != nil {
		return err
	}
	objectType, err := private.ObjectType(ctx, ref.Object)
	if err != nil {
		return err
	}
	if objectType == pinnedPlanningTagObjectTypeName && peeled != ref.Peeled || objectType != pinnedPlanningTagObjectTypeName && ref.Peeled != pinnedPlanningAbsentText {
		return fmt.Errorf(pinnedPlanningTagPeeledObjectMismatchErrorFormat, ref.Name)
	}
	if _, err := runPlanningGit(ctx, destination, pinnedPlanningUpdateRefCommandName, fullName, ref.Object); err != nil {
		return err
	}
	return nil
}

func runPlanningGit(ctx context.Context, directory string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, pinnedPlanningGitCommandName, append([]string{pinnedPlanningGitDirectoryFlag, directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningPrivateGitErrorFormat, args[pinnedPlanningFirstEntryIndex], err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (p *planningMaterialization) close() error {
	if p == nil || p.root == pinnedPlanningAbsentText {
		return nil
	}
	return os.RemoveAll(p.root)
}

func (a *App) openPinnedPlanning(ctx context.Context, pinned *execution.PinnedRunSources,
	captures []planningRefCapture) (*planningMaterialization, error) {
	if pinned == nil {
		return nil, fmt.Errorf(pinnedPlanningSourcePinAbsentErrorText)
	}
	rootRepository := pinnedPlanningAbsentText
	if a.workspace != nil {
		for _, repository := range a.workspace.Repositories {
			if repository.Root == a.workspace.ControlRoot {
				rootRepository = repository.Name
				break
			}
		}
		if rootRepository == pinnedPlanningAbsentText {
			return nil, fmt.Errorf(pinnedPlanningControlRepositoryAbsentErrorText)
		}
	}
	var refs []execution.PinnedNativeRef
	for _, capture := range captures {
		for name, target := range capture.expected {
			refs = append(refs, execution.PinnedNativeRef{
				Repository: capture.name, Name: name, Object: target.Object, Peeled: target.Peeled,
			})
		}
	}
	return materializePlanningInputs(ctx, planningMaterializationOptions{
		repositories: pinned.Repositories, sourceDirs: a.planningRealRoots(), refs: refs,
		rootRepository: rootRepository, maxObjectBytes: a.cfg.Execution.ResolveTransfer().MaxPackBytes,
	})
}
