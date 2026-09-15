package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func TestExplicitWorkspaceDoesNotReadOrWriteMatchingInheritedLivePins(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(configPath, []byte("{}"), 0o600))
	owners := map[string]string{"PACKAGE_LIB": "source"}
	live, err := workspaceenv.NewLivePins(root, configPath, owners, []string{"source"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = live.Close() })
	t.Setenv(workspaceenv.Root, root)
	t.Setenv(workspaceenv.Config, "dispat.json")
	t.Setenv(workspaceenv.Imports, "[]")
	t.Setenv(workspaceenv.Owners, `{"PACKAGE_LIB":"source"}`)
	t.Setenv(workspaceenv.Repositories, `["source"]`)
	t.Setenv(workspaceenv.LivePins, strings.TrimPrefix(live.Environment(), workspaceenv.LivePins+"="))

	workspace := &config.Workspace{ControlRoot: root, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: root, ConfigPath: configPath, Config: &config.File{Shell: []string{"sh", "-c"}}, Control: true},
		{Name: "source", Root: filepath.Join(root, "source"), Config: &config.File{}},
	}}
	a := NewWorkspace(root, workspace.Repositories[0].Config, workspace, zerolog.Nop())
	pins := newWorkspacePins(a)
	pin := strings.Repeat("a", 40)
	require.NoError(t, pins.remember("source", &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}, pin))
	reader, err := workspaceenv.OpenLivePins(root, configPath, os.Environ())
	require.NoError(t, err)
	got, err := reader.Pins("source")
	require.NoError(t, err)
	assert.Nil(t, got, "explicit global workspace selection must not publish into inherited run state")

	var stdout bytes.Buffer
	require.NoError(t, a.packageRunner().Run(t.Context(), root,
		`printf '%s' "$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS"`, nil, &stdout, &stdout))
	assert.Empty(t, stdout.String(), "explicit workspace scripts must not carry the inherited coordinator")
}
