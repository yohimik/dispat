package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestWorkspaceReleaseRejectsHeadsThatMovedAfterComposition(t *testing.T) {
	for _, repository := range []string{"source", config.ControlRepository} {
		t.Run(repository, func(t *testing.T) {
			control, source, path := compositionReleaseFixture(t)
			loaded, err := config.Load(path, nil)
			require.NoError(t, err)
			workspace, err := config.ComposeWorkspaceWithPinResolver(context.Background(), loaded, path, control, nil, nil, nil)
			require.NoError(t, err)
			moved := control
			if repository == "source" {
				moved = source
			}
			recordGit(t, moved, "commit", "--allow-empty", "-qm", "fix(lib): concurrent change after composition")

			_, err = NewWorkspace(control, loaded, workspace, zerolog.Nop()).Release(t.Context(), ReleaseOptions{})
			require.ErrorContains(t, err, "E330")
			_, statErr := os.Stat(filepath.Join(source, "pkg", "published"))
			assert.ErrorIs(t, statErr, os.ErrNotExist, "head drift is refused before publication")
		})
	}
}

func compositionReleaseFixture(t *testing.T) (control, sourceCheckout, configPath string) {
	t.Helper()
	source, _ := guardRepo(t, &config.File{})
	require.NoError(t, os.MkdirAll(filepath.Join(source, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "pkg", "input"), []byte("source"), 0o644))
	recordGit(t, source, "add", "pkg")
	recordGit(t, source, "commit", "-qm", "feat(lib): package")
	control, _ = guardRepo(t, &config.File{})
	recordGit(t, control, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--name", "source", "--", source, "source")
	configPath = filepath.Join(control, "dispat.json")
	configJSON := `{
  "polyrepo": true,
  "unsafeDisableLock": true,
  "commit": {"enabled": false, "push": false},
  "scripts": {"publish": ["touch published"]},
  "packages": {"lib": {"path": "source/pkg", "flow": {"publish": ["publish"]}}}
}`
	require.NoError(t, os.WriteFile(configPath, []byte(configJSON), 0o644))
	recordGit(t, control, "add", ".gitmodules", "dispat.json", "source")
	recordGit(t, control, "commit", "-qm", "chore: configure workspace")
	sourceCheckout = filepath.Join(control, "source")
	recordGit(t, sourceCheckout, "config", "user.name", "Test")
	recordGit(t, sourceCheckout, "config", "user.email", "test@example.com")
	return control, sourceCheckout, configPath
}

func TestWorkspacePlanHeadsSupportManuallyConstructedWorkspace(t *testing.T) {
	w, _ := recordFixture(t, false, false)
	pl := &plan.Plan{RepositoryHeads: make(map[string]string)}
	for _, record := range w.ordered {
		pl.RepositoryHeads[record.repo.Name] = recordGit(t, record.repo.Root, "rev-parse", "HEAD")
	}
	require.NoError(t, w.verifyPlannedHeads(pl))
	for _, record := range w.ordered {
		assert.Equal(t, pl.RepositoryHeads[record.repo.Name], record.expectedHead)
	}
}
