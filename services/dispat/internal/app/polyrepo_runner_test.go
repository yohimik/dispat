package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func TestWorkspaceRunnerUsesOwnerShellAndCarriesNestedContext(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "sources", "lib")
	require.NoError(t, os.MkdirAll(owner, 0o755))
	controlConfig := filepath.Join(root, "dispat.json")
	importedConfig := filepath.Join(owner, "dispat.json")
	workspace := &config.Workspace{ControlRoot: root, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: root, ConfigPath: controlConfig, Config: &config.File{Shell: []string{"sh", "-c"}}, Control: true},
		{Name: "lib-source", Root: owner, ConfigPath: importedConfig, Config: &config.File{Shell: []string{"bash", "-c"}}, Imported: true},
	}}
	a := NewWorkspace(root, workspace.Repositories[0].Config, workspace, zerolog.Nop())
	var stdout bytes.Buffer
	err := a.packageRunner().Run(context.Background(), owner,
		`[[ "$OWNER" == lib ]] && printf '%s\n%s\n%s\n' "$DISPAT_INTERNAL_WORKSPACE_ROOT" "$DISPAT_INTERNAL_WORKSPACE_CONFIG" "$DISPAT_INTERNAL_WORKSPACE_CONFIGS"`,
		[]string{"OWNER=lib"}, &stdout, &stdout)
	require.NoError(t, err, stdout.String())
	assert.Contains(t, stdout.String(), root+"\n")
	assert.Contains(t, stdout.String(), "dispat.json\n")
	assert.Contains(t, stdout.String(), `sources/lib/dispat.json`)
}

func TestWorkspaceRunnerCarriesExplicitOwnersAndClearsInheritedOwners(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "sources", "lib")
	require.NoError(t, os.MkdirAll(owner, 0o755))
	workspace := &config.Workspace{ControlRoot: root, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: root, Config: &config.File{}, Control: true},
		{Name: "lib-source", Root: owner, Config: &config.File{}},
	}}
	runner := &workspaceScriptRunner{workspace: workspace, fallback: []string{"sh", "-c"}}
	t.Setenv(workspaceenv.Owners, `{"PACKAGE_FOREIGN":"wrong-source"}`)
	for _, dir := range []string{root, owner} {
		var output bytes.Buffer
		require.NoError(t, runner.Run(t.Context(), dir, `printf '%s' "$DISPAT_INTERNAL_WORKSPACE_OWNERS"`,
			[]string{workspaceenv.Owners + `={"PACKAGE_LIB":"lib-source"}`}, &output, &output))
		assert.JSONEq(t, `{"PACKAGE_LIB":"lib-source"}`, output.String())
		output.Reset()
		require.NoError(t, runner.Run(t.Context(), dir, `printf '%s' "$DISPAT_INTERNAL_WORKSPACE_OWNERS"`, nil, &output, &output))
		assert.Empty(t, output.String())
	}
}

func TestImportedSelectorNamesUnionAcrossRepositories(t *testing.T) {
	root := t.TempDir()
	aRoot := filepath.Join(root, "sources", "a")
	bRoot := filepath.Join(root, "sources", "b")
	workspace := &config.Workspace{ControlRoot: root, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: root, Config: &config.File{}, Control: true},
		{Name: "a", Root: aRoot, Config: &config.File{Spaces: map[string]config.SpaceConfig{"workspace": {Path: config.PathList{"packages"}}}, VersionGroups: map[string]config.VersionGroupConfig{"shared": {}}}, Imported: true},
		{Name: "b", Root: bRoot, Config: &config.File{Spaces: map[string]config.SpaceConfig{"Workspace": {Path: config.PathList{"packages"}}}, VersionGroups: map[string]config.VersionGroupConfig{"Shared": {}}}, Imported: true},
	}}
	app := NewWorkspace(root, workspace.Repositories[0].Config, workspace, zerolog.Nop())
	pkgs := []*model.Package{
		{Name: "lib", Dir: filepath.Join(aRoot, "packages", "lib"), Space: &model.Space{Name: "workspace", Versioning: model.VersioningFixed, VersionGroup: "shared", GroupIdentity: "a\x00shared"}},
		{Name: "app", Dir: filepath.Join(bRoot, "packages", "app"), Space: &model.Space{Name: "Workspace", Versioning: model.VersioningFixed, VersionGroup: "Shared", GroupIdentity: "b\x00Shared"}},
	}
	ws := app.discoveredWorkspace(pkgs)
	bySpace, err := filter.Resolve(filter.Filter{Spaces: []string{"workspace"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"lib", "app"}, bySpace.Names)
	byGroup, err := filter.Resolve(filter.Filter{Groups: []string{"shared"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"lib", "app"}, byGroup.Names)
	assert.NotEqual(t, pkgs[0].VersionGroupIdentity(), pkgs[1].VersionGroupIdentity(), "planner identities remain repository-local")
}
