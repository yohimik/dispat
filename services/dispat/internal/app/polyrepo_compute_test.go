package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestCollectDependencyAdditionTargetsImportedConsumerConfig(t *testing.T) {
	root := t.TempDir()
	controlPath := filepath.Join(root, "dispat.json")
	ownerRoot := filepath.Join(root, "sources", "app")
	require.NoError(t, os.MkdirAll(ownerRoot, 0o755))
	ownerPath := filepath.Join(ownerRoot, "dispat.json")
	require.NoError(t, os.WriteFile(controlPath, []byte(`{"configs":["sources/app/dispat.json"]}`), 0o644))
	require.NoError(t, os.WriteFile(ownerPath, []byte(`{"dependencies":{"app":[{"provider":"remote","external":true,"keep":true}]}}`), 0o644))
	control := &config.File{}
	owner := &config.File{}
	workspace := &config.Workspace{ControlRoot: root, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: root, ConfigPath: controlPath, Config: control, Control: true},
		{Name: "app-source", Root: ownerRoot, ConfigPath: ownerPath, Config: owner, Imported: true},
	}}
	a := NewWorkspace(root, control, workspace, zerolog.Nop())
	var edits fileEdits
	err := a.collectDepEdits(&edits, controlPath, []suggestion{{
		action: actionAdd,
		entry:  config.DependencyConfig{Consumer: "app", Provider: "lib"},
		src:    config.DepSource{Repository: "app-source", KeyPath: []string{"dependencies"}},
	}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{ownerPath}, edits.order)
	assert.NotContains(t, edits.byFile, controlPath)
}
