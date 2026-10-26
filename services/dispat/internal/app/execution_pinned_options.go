package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Pinned planning documents contain only path-independent policy and the
// invocation's selection. Repository bytes and tags are pinned separately.
type pinnedPlanningSelection struct {
	Command        string   `json:"command"`
	Packages       []string `json:"packages"`
	Spaces         []string `json:"spaces"`
	Groups         []string `json:"groups"`
	Dir            string   `json:"dir"`
	Strict         bool     `json:"strict"`
	RequireRelease bool     `json:"requireRelease"`
	Script         string   `json:"script,omitempty"`
	Since          string   `json:"since,omitempty"`
	Consumers      bool     `json:"consumers,omitempty"`
	OnError        string   `json:"onError,omitempty"`
	Covered        []string `json:"covered,omitempty"`
	ArgsSHA256     string   `json:"argsSha256,omitempty"`
}

type pinnedRepositoryPolicy struct {
	Name             string            `json:"name"`
	Path             string            `json:"path"`
	Control          bool              `json:"control"`
	Linker           string            `json:"linker,omitempty"`
	Links            map[string]string `json:"links,omitempty"`
	Parser           ccme.Config       `json:"parser"`
	NonPackageScopes []string          `json:"nonPackageScopes,omitempty"`
}

type pinnedPlanningOptions struct {
	Initials             map[string]ccme.Version   `json:"initials,omitempty"`
	Parser               ccme.Config               `json:"parser"`
	PublishOrderKinds    []model.DepKind           `json:"publishOrderKinds,omitempty"`
	PublishBlockingKinds []model.DepKind           `json:"publishBlockingKinds,omitempty"`
	IgnoredTags          []string                  `json:"ignoredTags,omitempty"`
	IgnoredTagsByRepo    map[string][]string       `json:"ignoredTagsByRepository,omitempty"`
	RepositoryBaselines  []plan.RepositoryBaseline `json:"repositoryBaselines,omitempty"`
	LinkEvidence         bool                      `json:"linkEvidence"`
	NonPackageScopes     []string                  `json:"nonPackageScopes,omitempty"`
	Repositories         []pinnedRepositoryPolicy  `json:"repositories,omitempty"`
}

func (a *App) pinnedSelection(command string, terms filter.Filter) (pinnedPlanningSelection, error) {
	selection := pinnedPlanningSelection{Command: command,
		Packages: append([]string{}, terms.Packages...), Spaces: append([]string{}, terms.Spaces...),
		Groups: append([]string{}, terms.Groups...)}
	if terms.Dir == pinnedPlanningAbsentText {
		return selection, nil
	}
	absoluteDir, err := filepath.Abs(terms.Dir)
	if err != nil {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSelectionDirectoryResolutionErrorFormat, terms.Dir, err)
	}
	resolvedDir, err := filepath.EvalSymlinks(absoluteDir)
	if err != nil {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSelectionDirectoryResolutionErrorFormat, terms.Dir, err)
	}
	absoluteAnchor, err := filepath.Abs(a.runAnchor())
	if err != nil {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningWorkspaceRootResolutionErrorFormat, a.runAnchor(), err)
	}
	resolvedAnchor, err := filepath.EvalSymlinks(absoluteAnchor)
	if err != nil {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningWorkspaceRootResolutionErrorFormat, a.runAnchor(), err)
	}
	relative, err := filepath.Rel(resolvedAnchor, resolvedDir)
	if err != nil || relative == pinnedPlanningParentPath || strings.HasPrefix(relative, pinnedPlanningParentPath+string(filepath.Separator)) {
		return pinnedPlanningSelection{}, fmt.Errorf(pinnedPlanningSelectionDirectoryEscapeErrorText)
	}
	selection.Dir = filepath.ToSlash(relative)
	return selection, nil
}

func (a *App) pinnedReleaseDocuments(opts ReleaseOptions) (json.RawMessage, json.RawMessage, error) {
	selection, err := a.pinnedSelection(pinnedPlanningReleaseCommandName, opts.Filter)
	if err != nil {
		return nil, nil, err
	}
	selection.Strict, selection.RequireRelease = opts.Strict, opts.RequireRelease
	return a.marshalPinnedDocuments(selection)
}

func (a *App) pinnedSweepDocuments(name string, opts RunOptions, covered []string) (json.RawMessage, json.RawMessage, error) {
	selection, err := a.pinnedSelection(pinnedPlanningRunCommandName, opts.Window.Filter)
	if err != nil {
		return nil, nil, err
	}
	selection.Script, selection.Since, selection.Consumers = name, opts.Window.Since, opts.Window.Consumers
	selection.OnError = opts.OnError
	if selection.OnError == pinnedPlanningAbsentText {
		selection.OnError = OnErrorSkip
	}
	selection.Covered = append([]string{}, covered...)
	sort.Strings(selection.Covered)
	encodedArgs, err := json.Marshal(opts.Args)
	if err != nil {
		return nil, nil, err
	}
	hash := sha256.Sum256(encodedArgs)
	selection.ArgsSHA256 = hex.EncodeToString(hash[:])
	return a.marshalPinnedDocuments(selection)
}

func (a *App) marshalPinnedDocuments(selection pinnedPlanningSelection) (json.RawMessage, json.RawMessage, error) {
	opts := a.plannedOptions
	policy := pinnedPlanningOptions{
		Initials: opts.Initials, Parser: opts.ParserConfig,
		PublishOrderKinds: opts.PublishOrderKinds, PublishBlockingKinds: opts.PublishBlockingKinds,
		IgnoredTags: opts.IgnoredTags, IgnoredTagsByRepo: opts.IgnoredTagsByRepository,
		RepositoryBaselines: opts.RepositoryBaselines, LinkEvidence: opts.LinkEvidence,
		NonPackageScopes: opts.NonPackageScopes,
	}
	for _, history := range opts.Repositories {
		policy.Repositories = append(policy.Repositories, pinnedRepositoryPolicy{
			Name: history.Name, Path: filepath.ToSlash(history.Path), Control: history.Control,
			Linker: history.Linker, Links: history.Links, Parser: history.ParserConfig,
			NonPackageScopes: history.NonPackageScopes,
		})
	}
	sort.Slice(policy.Repositories, func(i, j int) bool { return policy.Repositories[i].Name < policy.Repositories[j].Name })
	selectionBytes, err := json.Marshal(selection)
	if err != nil {
		return nil, nil, err
	}
	optionsBytes, err := json.Marshal(policy)
	if err != nil {
		return nil, nil, err
	}
	return selectionBytes, optionsBytes, nil
}
