// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// peerReleasePlan retains the private checkout for task frames after the peer
// has independently checked both the semantic plan and the shared graph.
type peerReleasePlan struct {
	app      *App
	inputs   *planningMaterialization
	plan     *plan.Plan
	compiled *release.CompiledTaskGraph
}

func (p *peerReleasePlan) close() error {
	if p == nil {
		return nil
	}
	return p.inputs.close()
}

type peerReleasePlanningOptions struct {
	Inputs              execution.PinnedRunInputs
	Binding             execution.ChoreographyRunBinding
	SemanticGraphDigest string
	SourceDirs          map[string]string
	MaxPackBytes        int64
	Log                 zerolog.Logger
}

type peerPlanningEnvironment struct {
	app    *App
	inputs *planningMaterialization
}

func openPeerPlanningEnvironment(ctx context.Context,
	options peerReleasePlanningOptions) (_ *peerPlanningEnvironment, err error) {
	inputs := options.Inputs
	if options.MaxPackBytes <= pinnedPlanningNoObjectBytesCount || len(inputs.ConfigFiles) == pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningPeerInputIncompleteErrorText)
	}
	invocation, err := parsePinnedInvocation(inputs.Invocation)
	if err != nil {
		return nil, err
	}
	materialized, err := materializePlanningInputs(ctx, planningMaterializationOptions{
		repositories: inputs.Repositories, sourceDirs: options.SourceDirs,
		refs: inputs.NativeRefs, rootRepository: inputs.RootRepository,
		maxObjectBytes: options.MaxPackBytes, sinceAnchor: inputs.SinceAnchor,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = materialized.close()
		}
	}()
	root := materialized.repositories[inputs.RootRepository]
	configPath, err := materializedConfigPath(materialized, inputs.ConfigFiles[pinnedPlanningFirstEntryIndex])
	if err != nil {
		return nil, err
	}
	if inputs.ConfigFiles[pinnedPlanningFirstEntryIndex].Repository != inputs.RootRepository {
		return nil, fmt.Errorf(pinnedPlanningControlConfigRepositoryMismatchErrorText)
	}
	flags, err := (PlanningConfigInvocation{Concurrency: invocation.Concurrency,
		ConcurrencySet: invocation.ConcurrencySet}).flagSet()
	if err != nil {
		return nil, err
	}
	configFile, err := config.LoadControl(configPath, flags, len(invocation.Imports) != pinnedPlanningEmptyEntryCount)
	if err != nil {
		return nil, fmt.Errorf(pinnedPlanningControlConfigLoadErrorFormat, err)
	}
	if err := config.ValidateCapturedSources(materialized.root, configFile.SourceFiles); err != nil {
		return nil, err
	}
	if err := pinPeerEffectiveConcurrency(configFile, invocation); err != nil {
		return nil, err
	}
	if invocation.PolyrepoSet {
		configFile.Polyrepo = invocation.Polyrepo
	}
	if len(configFile.Configs)+len(invocation.Imports) != pinnedPlanningEmptyEntryCount {
		configFile.Polyrepo = pinnedPlanningPolyrepoEnabledState
	}
	if invocation.QuietParserSet {
		configFile.Parser.Quiet = invocation.QuietParser
	}
	imports := make([]string, pinnedPlanningInitialEntryCount, len(invocation.Imports))
	for _, imported := range invocation.Imports {
		path, pathErr := materializedConfigPath(materialized, imported)
		if pathErr != nil {
			return nil, pathErr
		}
		imports = append(imports, path)
	}
	workspace, err := config.ComposeWorkspaceWithPinResolver(ctx, configFile, configPath, root,
		imports, nil, nil)
	if err != nil {
		return nil, fmt.Errorf(pinnedPlanningWorkspaceCompositionErrorFormat, err)
	}
	if err := comparePeerRepositoryRoster(materialized, inputs.Repositories, workspace); err != nil {
		return nil, err
	}
	if workspace != nil {
		for _, repository := range workspace.Repositories {
			if repository.Config != nil {
				if err := config.ValidateCapturedSources(materialized.root, repository.Config.SourceFiles); err != nil {
					return nil, err
				}
			}
		}
	}
	private := NewWorkspace(root, configFile, workspace, options.Log)
	private.configSourceRoot = materialized.root
	private.planningRemotes = make(map[string]planningRemoteCoordinate, len(inputs.RepositoryCoordinates))
	for _, coordinate := range inputs.RepositoryCoordinates {
		if materialized.repositories[coordinate.Repository] == pinnedPlanningAbsentText {
			return nil, fmt.Errorf(pinnedPlanningRemoteCoordinateUnknownRepositoryErrorFormat, coordinate.Repository)
		}
		if _, exists := private.planningRemotes[coordinate.Repository]; exists {
			return nil, fmt.Errorf(pinnedPlanningRemoteCoordinateRepeatedErrorFormat, coordinate.Repository)
		}
		private.planningRemotes[coordinate.Repository] = planningRemoteCoordinate{
			host: coordinate.Host, owner: coordinate.Owner, repo: coordinate.Repo,
		}
	}
	if workspace == nil {
		if len(private.planningRemotes) != pinnedPlanningEmptyEntryCount {
			return nil, fmt.Errorf(pinnedPlanningSingleRepositoryCoordinatesUnexpectedErrorText)
		}
	} else {
		for _, repository := range workspace.Repositories {
			if repository.Control {
				continue
			}
			if _, exists := private.planningRemotes[repository.Name]; !exists {
				return nil, fmt.Errorf(pinnedPlanningRemoteCoordinateAbsentErrorFormat, repository.Name)
			}
		}
	}
	private.ignoreTags = append([]string(nil), invocation.IgnoredTags...)
	private.ignoreTagsByRepository = invocation.IgnoredByRepo
	private.runID = options.Binding.Run
	private.planningInvocation = PlanningConfigInvocation{
		Concurrency: invocation.Concurrency, ConcurrencySet: invocation.ConcurrencySet,
		Polyrepo: invocation.Polyrepo, PolyrepoSet: invocation.PolyrepoSet,
		QuietParser: invocation.QuietParser, QuietParserSet: invocation.QuietParserSet,
		ImportedConfigs: imports,
	}
	files, err := private.pinnedConfigFiles()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(files, inputs.ConfigFiles) {
		return nil, fmt.Errorf(pinnedPlanningConfigProvenanceMismatchErrorText)
	}
	return &peerPlanningEnvironment{app: private, inputs: materialized}, nil
}

// recomputePeerReleasePlan reads only the authenticated source closure. The
// signed digests are comparisons, never substitutes for this planner pass.
func recomputePeerReleasePlan(ctx context.Context, options peerReleasePlanningOptions) (_ *peerReleasePlan, err error) {
	inputs := options.Inputs
	if options.Binding.Run == pinnedPlanningAbsentText || options.Binding.Generation == pinnedPlanningAbsentText ||
		options.Binding.PlanDigest == pinnedPlanningAbsentText || options.Binding.GraphDigest == pinnedPlanningAbsentText ||
		options.SemanticGraphDigest == pinnedPlanningAbsentText ||
		options.MaxPackBytes <= pinnedPlanningNoObjectBytesCount || len(inputs.ConfigFiles) == pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningPeerIdentityIncompleteErrorText)
	}
	selection, err := decodePinnedReleaseSelection(inputs.Selection)
	if err != nil {
		return nil, err
	}
	if inputs.SinceAnchor != nil {
		return nil, fmt.Errorf(pinnedPlanningReleaseSinceAnchorUnexpectedErrorText)
	}
	environment, err := openPeerPlanningEnvironment(ctx, options)
	if err != nil {
		return nil, err
	}
	materialized, private := environment.inputs, environment.app
	defer func() {
		if err != nil {
			_ = materialized.close()
		}
	}()
	root := materialized.repositories[inputs.RootRepository]
	selected := ReleaseOptions{Strict: selection.Strict, RequireRelease: selection.RequireRelease,
		Filter: filter.Filter{Packages: selection.Packages, Spaces: selection.Spaces, Groups: selection.Groups}}
	if selection.Dir != pinnedPlanningAbsentText {
		selected.Filter.Dir, err = rebasePlanningPath(filepath.Join(root, filepath.FromSlash(selection.Dir)), root, root)
		if err != nil {
			return nil, err
		}
	}
	computed, err := private.selectedPlan(ctx, selected)
	if err != nil {
		return nil, err
	}
	digest, err := private.calculatePlanDigest(ctx, computed, selected)
	if err != nil {
		return nil, err
	}
	if digest != options.Binding.PlanDigest {
		return nil, fmt.Errorf(pinnedPlanningPeerPlanMismatchErrorText)
	}
	selectionBytes, policyBytes, err := private.pinnedReleaseDocuments(selected)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(selectionBytes, inputs.Selection) || !bytes.Equal(policyBytes, inputs.Options) {
		return nil, fmt.Errorf(pinnedPlanningPeerPolicyMismatchErrorText)
	}
	compiled, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(computed)
	if err != nil {
		return nil, err
	}
	if err := private.includePreparedReleaseGraph(computed, compiled); err != nil {
		return nil, err
	}
	if compiled.Digest() != options.SemanticGraphDigest {
		return nil, fmt.Errorf(pinnedPlanningPeerGraphMismatchErrorText)
	}
	return &peerReleasePlan{app: private, inputs: materialized, plan: computed, compiled: compiled}, nil
}

func decodePinnedReleaseSelection(document json.RawMessage) (pinnedPlanningSelection, error) {
	selection, err := decodePinnedSelection(document)
	if err != nil || selection.Command != pinnedPlanningReleaseCommandName {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningReleaseSelectionInvalidErrorText)
	}
	if selection.Script != pinnedPlanningAbsentText || selection.Since != pinnedPlanningAbsentText || selection.Consumers || selection.OnError != pinnedPlanningAbsentText ||
		len(selection.Covered) != pinnedPlanningEmptyEntryCount ||
		selection.ArgsSHA256 != pinnedPlanningAbsentText || filepath.IsAbs(selection.Dir) ||
		selection.Dir == pinnedPlanningParentPath || strings.HasPrefix(filepath.Clean(selection.Dir), pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningReleaseSelectionEscapeErrorText)
	}
	return selection, nil
}

func decodePinnedSelection(document json.RawMessage) (pinnedPlanningSelection, error) {
	var selection pinnedPlanningSelection
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSelectionInvalidErrorFormat, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSelectionTrailingDataErrorText)
	}
	return selection, nil
}

func materializedConfigPath(inputs *planningMaterialization, file execution.PinnedConfigFile) (string, error) {
	root := inputs.repositories[file.Repository]
	if root == pinnedPlanningAbsentText || file.Path == pinnedPlanningAbsentText || filepath.IsAbs(file.Path) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningInvalidConfigRepositoryPathErrorText)
	}
	path := filepath.Clean(filepath.FromSlash(file.Path))
	if path == pinnedPlanningParentPath || strings.HasPrefix(path, pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningConfigPathEscapeErrorText)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return pinnedPlanningAbsentText, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == pinnedPlanningParentPath || strings.HasPrefix(relative, pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningConfigResolutionEscapeErrorText)
	}
	return resolved, nil
}

func comparePeerRepositoryRoster(inputs *planningMaterialization,
	repositories []execution.AssignmentRepository, workspace *config.Workspace) error {
	if workspace == nil {
		if len(repositories) == pinnedPlanningSingleRepositoryCount && repositories[pinnedPlanningFirstEntryIndex].Name == pinnedPlanningAbsentText {
			return nil
		}
		return fmt.Errorf(pinnedPlanningFleetCompositionAbsentErrorText)
	}
	if len(workspace.Repositories) != len(repositories) {
		return fmt.Errorf(pinnedPlanningRepositoryRosterMismatchErrorText)
	}
	paths := make(map[string]string, len(repositories))
	for _, repository := range repositories {
		paths[repository.Name] = repository.Path
	}
	for _, repository := range workspace.Repositories {
		if inputs.repositories[repository.Name] != repository.Root {
			return fmt.Errorf(pinnedPlanningRepositoryOwnerMismatchErrorFormat, repository.Name)
		}
		if relativeRepositoryPath(workspace.ControlRoot, repository.Root) != paths[repository.Name] {
			return fmt.Errorf(pinnedPlanningRepositoryPathMismatchErrorFormat, repository.Name)
		}
	}
	return nil
}
