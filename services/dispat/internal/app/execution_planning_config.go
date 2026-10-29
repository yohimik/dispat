package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// PlanningConfigInvocation records only the command-line edits to the
// effective configuration. The private planner loads the captured files with
// the ordinary loader, then applies this same explicit overlay.
type PlanningConfigInvocation struct {
	Concurrency     []int
	ConcurrencySet  bool
	LogLevel        string
	LogLevelSet     bool
	LogFormat       string
	LogFormatSet    bool
	Polyrepo        bool
	PolyrepoSet     bool
	QuietParser     bool
	QuietParserSet  bool
	Workers         []string
	ImportedConfigs []string
}

func (a *App) SetPlanningConfigInvocation(invocation PlanningConfigInvocation) {
	a.planningInvocation = invocation
}

func (a *App) pinnedConfigFiles() ([]execution.PinnedConfigFile, error) {
	if len(a.cfg.SourceFiles) == pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningControlConfigProvenanceAbsentErrorText)
	}
	paths := []string{a.cfg.SourceFiles[pinnedPlanningFirstEntryIndex]}
	if a.workspace != nil {
		for _, repository := range a.workspace.Repositories {
			if repository.Imported && repository.ConfigPath != pinnedPlanningAbsentText {
				paths = append(paths, repository.ConfigPath)
			}
		}
	}
	files := make([]execution.PinnedConfigFile, pinnedPlanningInitialEntryCount, len(paths))
	for _, path := range paths {
		name, relative, err := a.pinnedRelativeConfigPath(path)
		if err != nil {
			return nil, err
		}
		files = append(files, execution.PinnedConfigFile{Repository: name, Path: filepath.ToSlash(relative)})
	}
	return files, nil
}

func (a *App) pinnedRelativeConfigPath(path string) (string, string, error) {
	absolute, err := a.absolutePlanningConfigPath(path)
	if err != nil {
		return pinnedPlanningAbsentText, pinnedPlanningAbsentText, err
	}
	resolvedPath, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return pinnedPlanningAbsentText, pinnedPlanningAbsentText, err
	}
	bestName, bestRoot, bestRelative := pinnedPlanningAbsentText, pinnedPlanningAbsentText, pinnedPlanningAbsentText
	for name, root := range a.planningRealRoots() {
		if root == pinnedPlanningAbsentText {
			continue
		}
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return pinnedPlanningAbsentText, pinnedPlanningAbsentText, err
		}
		resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
		if err != nil {
			return pinnedPlanningAbsentText, pinnedPlanningAbsentText, err
		}
		relative, err := filepath.Rel(resolvedRoot, resolvedPath)
		if err == nil && relative != pinnedPlanningParentPath && !strings.HasPrefix(relative, pinnedPlanningParentPath+string(filepath.Separator)) &&
			len(resolvedRoot) > len(bestRoot) {
			bestName, bestRoot, bestRelative = name, resolvedRoot, relative
		}
	}
	if bestRelative == pinnedPlanningAbsentText {
		return pinnedPlanningAbsentText, pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningConfigOutsideRepositoriesErrorFormat, path)
	}
	return bestName, bestRelative, nil
}

func (a *App) absolutePlanningConfigPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.root, path)
	}
	return filepath.Abs(path)
}

func (a *App) loadPinnedConfigurations(private *planningMaterialization,
	workspace *config.Workspace) (*config.File, error) {
	if len(a.cfg.SourceFiles) == pinnedPlanningEmptyEntryCount {
		return nil, fmt.Errorf(pinnedPlanningEffectiveConfigProvenanceAbsentErrorText)
	}
	rootPath, err := a.pinnedConfigPath(private, a.cfg.SourceFiles[pinnedPlanningFirstEntryIndex])
	if err != nil {
		return nil, err
	}
	flags, err := a.planningInvocation.flagSet()
	if err != nil {
		return nil, err
	}
	root, err := config.LoadControl(rootPath, flags, len(a.planningInvocation.ImportedConfigs) != pinnedPlanningEmptyEntryCount)
	if err != nil {
		return nil, fmt.Errorf(pinnedPlanningCapturedControlConfigErrorFormat, err)
	}
	if err := config.ValidateCapturedSources(private.root, root.SourceFiles); err != nil {
		return nil, err
	}
	if a.planningInvocation.PolyrepoSet {
		root.Polyrepo = a.planningInvocation.Polyrepo
	}
	if len(root.Configs)+len(a.planningInvocation.ImportedConfigs) > pinnedPlanningEmptyEntryCount {
		root.Polyrepo = pinnedPlanningPolyrepoEnabledState
	}
	if a.planningInvocation.QuietParserSet {
		root.Parser.Quiet = a.planningInvocation.QuietParser
	}
	if err := comparePinnedConfig(a.cfg, root); err != nil {
		return nil, fmt.Errorf(pinnedPlanningControlConfigChangedErrorFormat, err)
	}
	if err := a.comparePinnedFiles(private, a.cfg.SourceFiles, root.SourceFiles); err != nil {
		return nil, err
	}
	if workspace != nil {
		for i := range workspace.Repositories {
			repository := &workspace.Repositories[i]
			if repository.Control {
				repository.Config = root
				continue
			}
			if !repository.Imported || repository.Config == nil {
				continue
			}
			if len(repository.Config.SourceFiles) == pinnedPlanningEmptyEntryCount {
				return nil, fmt.Errorf(pinnedPlanningImportedConfigProvenanceAbsentErrorFormat, repository.Name)
			}
			loaded, loadErr := config.Load(repository.ConfigPath, nil)
			if loadErr != nil {
				return nil, fmt.Errorf(pinnedPlanningCapturedConfigErrorFormat, repository.Name, loadErr)
			}
			if err := config.ValidateCapturedSources(private.root, loaded.SourceFiles); err != nil {
				return nil, err
			}
			if err := comparePinnedConfig(repository.Config, loaded); err != nil {
				return nil, fmt.Errorf(pinnedPlanningImportedConfigChangedErrorFormat, repository.Name, err)
			}
			if err := a.comparePinnedFiles(private, repository.Config.SourceFiles, loaded.SourceFiles); err != nil {
				return nil, err
			}
			repository.Config = loaded
		}
	}
	return root, nil
}

func (a *App) pinnedConfigPath(private *planningMaterialization, path string) (string, error) {
	if path == pinnedPlanningAbsentText {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningConfigPathEmptyErrorText)
	}
	path, err := a.absolutePlanningConfigPath(path)
	if err != nil {
		return pinnedPlanningAbsentText, err
	}
	bestRoot, bestPath := pinnedPlanningAbsentText, pinnedPlanningAbsentText
	for name, root := range a.planningRealRoots() {
		if root == pinnedPlanningAbsentText || private.repositories[name] == pinnedPlanningAbsentText {
			continue
		}
		mapped, mapErr := rebasePlanningPath(path, root, private.repositories[name])
		if mapErr == nil && len(root) > len(bestRoot) {
			bestRoot, bestPath = root, mapped
		}
	}
	if bestPath == pinnedPlanningAbsentText {
		return pinnedPlanningAbsentText, fmt.Errorf(pinnedPlanningConfigOutsideRepositoriesErrorFormat, path)
	}
	return bestPath, nil
}

func (a *App) comparePinnedFiles(private *planningMaterialization, original, captured []string) error {
	if len(original) != len(captured) {
		return fmt.Errorf(pinnedPlanningConfigReferenceListChangedErrorText)
	}
	for i, path := range original {
		mapped, err := a.pinnedConfigPath(private, path)
		if err != nil {
			return err
		}
		if filepath.Clean(mapped) != filepath.Clean(captured[i]) {
			return fmt.Errorf(pinnedPlanningConfigReferenceChangedErrorFormat, path)
		}
	}
	return nil
}

func comparePinnedConfig(original, captured *config.File) error {
	oldJSON, err := json.Marshal(original)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(captured)
	if err != nil {
		return err
	}
	if !bytes.Equal(oldJSON, newJSON) {
		return fmt.Errorf(pinnedPlanningEffectiveConfigMismatchErrorText)
	}
	return nil
}

func (i PlanningConfigInvocation) flagSet() (*pflag.FlagSet, error) {
	flags := pflag.NewFlagSet(pinnedPlanningFlagSetName, pflag.ContinueOnError)
	flags.IntSlice(pinnedPlanningConcurrencyFlagName, nil, pinnedPlanningAbsentText)
	flags.String(pinnedPlanningLogLevelFlagName, pinnedPlanningAbsentText, pinnedPlanningAbsentText)
	flags.String(pinnedPlanningLogFormatFlagName, pinnedPlanningAbsentText, pinnedPlanningAbsentText)
	flags.StringArray(config.WorkerFlag, nil, pinnedPlanningAbsentText)
	if i.ConcurrencySet {
		values := make([]string, len(i.Concurrency))
		for index, value := range i.Concurrency {
			values[index] = strconv.Itoa(value)
		}
		if err := flags.Set(pinnedPlanningConcurrencyFlagName, strings.Join(values, pinnedPlanningFlagListSeparatorText)); err != nil {
			return nil, err
		}
	}
	for _, entry := range []struct {
		enabled     bool
		name, value string
	}{
		{i.LogLevelSet, pinnedPlanningLogLevelFlagName, i.LogLevel},
		{i.LogFormatSet, pinnedPlanningLogFormatFlagName, i.LogFormat},
	} {
		if entry.enabled {
			if err := flags.Set(entry.name, entry.value); err != nil {
				return nil, err
			}
		}
	}
	for _, link := range i.Workers {
		if err := flags.Set(config.WorkerFlag, link); err != nil {
			return nil, err
		}
	}
	return flags, nil
}
