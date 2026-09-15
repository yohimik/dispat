package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func workspaceCommitPlan(t *testing.T, w *workspaceRecorder, rel *plan.Release) *plan.Plan {
	t.Helper()
	heads := make(map[string]string, len(w.ordered))
	for _, repository := range w.ordered {
		heads[repository.repo.Name] = recordGit(t, repository.repo.Root, "rev-parse", "HEAD")
	}
	return &plan.Plan{
		Order:           []string{rel.Pkg.Name},
		Releases:        map[string]*plan.Release{rel.Pkg.Name: rel},
		RepositoryHeads: heads,
	}
}

func TestWorkspaceCommitAppliesInvocationPolicyAndPublishesSourceRecords(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.app.workspace.RepositoryByName("source")
	require.NotNil(t, source)
	source.Commit.Branch = "release"
	rel.Pkg.Space.AliasTags = []model.AliasTag{
		{Format: "{name}-v{major}", Force: true},
		{Format: "{name}-v{major}.{minor}"},
	}

	remote := t.TempDir()
	recordGit(t, remote, "init", "--bare", "-q")
	recordGit(t, source.Root, "remote", "add", "publish", remote)
	recordGit(t, source.Root, "push", "-q", "publish", "HEAD:refs/heads/main")
	recordGit(t, source.Root, "checkout", "-q", "--detach")

	require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte("release source"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(source.Root, "release-notes.txt"), []byte("release notes"), 0o644))
	output := filepath.Join(t.TempDir(), "step-output")
	t.Setenv(release.OutputEnvVar, output)
	pl := workspaceCommitPlan(t, w, rel)

	opts := CommitOptions{
		Tag:     true,
		Push:    true,
		Name:    "Standalone Bot",
		Email:   "standalone@example.test",
		Remote:  "publish",
		Message: "ship {packages}: {tags}",
		TagName: "lib-custom-1",
		Include: []string{"release-notes.txt"},
	}
	require.NoError(t, w.app.commitWorkspace(t.Context(), pl, []string{rel.Pkg.Name}, opts))

	pin := recordGit(t, source.Root, "rev-parse", "HEAD")
	assert.Equal(t, "Standalone Bot <standalone@example.test>",
		recordGit(t, source.Root, "log", "-1", "--format=%an <%ae>"))
	assert.Equal(t, "ship lib: lib-custom-1", recordGit(t, source.Root, "log", "-1", "--format=%s"))
	assert.Equal(t, "pkg/input\nrelease-notes.txt",
		recordGit(t, source.Root, "show", "--pretty=format:", "--name-only", "HEAD"))
	assert.Equal(t, pin, recordGit(t, remote, "rev-parse", "refs/heads/release"))
	for _, tag := range []string{"lib-custom-1", "lib-v1", "lib-v1.0"} {
		assert.Equal(t, pin, recordGit(t, source.Root, "rev-parse", "refs/tags/"+tag+"^{commit}"))
		assert.Equal(t, pin, recordGit(t, remote, "rev-parse", "refs/tags/"+tag+"^{commit}"))
	}
	contents, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, "PACKAGE_LIB="+pin, strings.TrimSpace(string(contents)))
	assert.False(t, source.Commit.IsEnabled(), "standalone overrides must not mutate workspace policy")
	assert.Empty(t, source.Commit.Remote)
	assert.Equal(t, "release", source.Commit.Branch)
}

func TestWorkspaceCommitPushPreflightLeavesSourceUntouched(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.app.workspace.RepositoryByName("source")
	require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte("uncommitted release"), 0o644))
	pl := workspaceCommitPlan(t, w, rel)
	before := pl.RepositoryHeads[source.Name]

	err := w.app.commitWorkspace(context.Background(), pl, []string{rel.Pkg.Name}, CommitOptions{
		Tag: true, Push: true, Remote: "missing-release-remote",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-release-remote")
	assert.Equal(t, before, recordGit(t, source.Root, "rev-parse", "HEAD"))
	assert.Empty(t, recordGit(t, source.Root, "tag", "--list", rel.TagName()))
	assert.Equal(t, "uncommitted release", string(requireFile(t, filepath.Join(rel.Pkg.Dir, "input"))))
	assert.False(t, source.Commit.IsEnabled(), "failed invocation must not mutate workspace policy")
	assert.Empty(t, source.Commit.Remote)
}

func TestWorkspaceCommitPushFailureKeepsLocalSourceRecord(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.app.workspace.RepositoryByName("source")
	verify := false
	source.Commit.Verify = &verify
	rel.Pkg.Space.AliasTags = []model.AliasTag{{Format: "{name}-v{major}", Force: true}}
	require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte("record before failed push"), 0o644))
	pl := workspaceCommitPlan(t, w, rel)
	before := pl.RepositoryHeads[source.Name]

	err := w.app.commitWorkspace(t.Context(), pl, []string{rel.Pkg.Name}, CommitOptions{
		Tag: true, Push: true, Remote: "missing-release-remote", NoForce: true,
	})
	require.Error(t, err)
	pin := recordGit(t, source.Root, "rev-parse", "HEAD")
	assert.NotEqual(t, before, pin)
	assert.Equal(t, pin, recordGit(t, source.Root, "rev-parse", "refs/tags/"+rel.TagName()+"^{commit}"))
	assert.Equal(t, pin, recordGit(t, source.Root, "rev-parse", "refs/tags/lib-v1^{commit}"))
	assert.False(t, source.Commit.IsEnabled(), "failed invocation must not mutate workspace policy")
	assert.Empty(t, source.Commit.Remote)
}

func requireFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return contents
}
