package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeWorkspaceReservesControlRepositoryIdentity(t *testing.T) {
	source := workspaceRepo(t, "lib", nil)
	for _, name := range []string{"control", "Control", "CONTROL", "cOnTrOl"} {
		t.Run(name, func(t *testing.T) {
			cfg := File{Polyrepo: true, Packages: map[string]PackageConfig{
				"lib": {Path: "sources/" + name + "/pkgs/lib"},
			}}
			root, path := workspaceControl(t, map[string]string{name: source}, cfg)
			loaded, err := Load(path, nil)
			require.NoError(t, err)
			workspace, err := ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, root, nil, nil, nil)
			requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
			assert.ErrorContains(t, err, "reserved submodule name")
			assert.Nil(t, workspace, "reject the name before constructing an ownership map")
		})
	}
}
