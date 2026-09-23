package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestWorkspacePreflightSkipsUnusedDetachedPushRepository(t *testing.T) {
	w, _ := recordFixture(t, true, false)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	source.repo.Commit.Include = []string{"pkg/input"}
	source.repo.Config.Run.AllowBranch = []string{"release"}
	verify := false
	source.repo.Commit.Verify = &verify
	recordGit(t, source.repo.Root, "checkout", "-q", "--detach")
	require.NoError(t, os.WriteFile(filepath.Join(source.repo.Root, "pkg", "input"), []byte("unused local work"), 0o644))

	rel := &plan.Release{
		Pkg: &model.Package{
			Name:       "control-package",
			Repository: config.ControlRepository,
			Space:      &model.Space{Name: "control"},
		},
		Channel: "stable",
		Next:    ccme.Version{Major: 1},
		Bump:    ccme.BumpMinor,
		NewWork: true,
	}
	pl := &plan.Plan{
		Order:    []string{rel.Pkg.Name},
		Releases: map[string]*plan.Release{rel.Pkg.Name: rel},
		RepositoryHeads: map[string]string{
			"source":                 recordGit(t, source.repo.Root, "rev-parse", "HEAD"),
			config.ControlRepository: recordGit(t, w.byName[config.ControlRepository].repo.Root, "rev-parse", "HEAD"),
		},
	}

	require.NoError(t, w.verifyPushBranches(context.Background(), pl))
	require.NoError(t, w.prepare(context.Background(), pl))
	assert.Empty(t, source.branch)
}

func TestWorkspaceDetachedCleanReleasePushesTagWithoutBranch(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	verify := false
	source.repo.Commit.Verify = &verify
	recordGit(t, source.repo.Root, "checkout", "-q", "--detach")
	pl := &plan.Plan{Order: []string{rel.Pkg.Name}, Releases: map[string]*plan.Release{rel.Pkg.Name: rel}}

	require.NoError(t, w.verifyPushBranches(context.Background(), pl))
	before := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	require.NoError(t, w.Record(context.Background(), rel))

	assert.Empty(t, source.branch)
	assert.Equal(t, before, recordGit(t, source.repo.Root, "rev-parse", "HEAD"))
	origin := recordGit(t, source.repo.Root, "remote", "get-url", "origin")
	assert.Equal(t, before, recordGit(t, origin, "rev-parse", "refs/tags/"+rel.TagName()+"^{commit}"))
}

func TestWorkspaceDetachedReleaseCommitRequiresExplicitBranchBeforeGitMutation(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	verify := false
	source.repo.Commit.Verify = &verify
	recordGit(t, source.repo.Root, "checkout", "-q", "--detach")
	rel.Pkg.Changelog.Enabled = true
	pl := &plan.Plan{Order: []string{rel.Pkg.Name}, Releases: map[string]*plan.Release{rel.Pkg.Name: rel}}

	require.NoError(t, w.verifyPushBranches(context.Background(), pl))
	before := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	err := w.Record(context.Background(), rel)
	require.Error(t, err)
	assert.Equal(t, "E337", config.DiagnosticCode(err))
	assert.Contains(t, err.Error(), "set commit.branch")
	assert.Equal(t, before, recordGit(t, source.repo.Root, "rev-parse", "HEAD"))
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
	assert.Empty(t, strings.TrimSpace(recordGit(t, source.repo.Root, "diff", "--cached", "--name-only")),
		"the detached-branch refusal must happen before staging")
}

func TestWorkspaceDetachedReleaseCommitIsRejectedBeforePublish(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	verify := false
	source.repo.Commit.Verify = &verify
	recordGit(t, source.repo.Root, "checkout", "-q", "--detach")
	rel.Pkg.Changelog.Enabled = true
	pl := &plan.Plan{Order: []string{rel.Pkg.Name}, Releases: map[string]*plan.Release{rel.Pkg.Name: rel}}
	require.NoError(t, w.verifyPushBranches(context.Background(), pl))

	err := w.verifyPublishBranch(context.Background(), rel)
	require.Error(t, err)
	assert.Equal(t, "E337", config.DiagnosticCode(err))
	assert.Contains(t, err.Error(), "before publishing")
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
}

func TestWorkspaceDetachedCleanTagOnlyReleasePassesPublishGuard(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	verify := false
	source.repo.Commit.Verify = &verify
	recordGit(t, source.repo.Root, "checkout", "-q", "--detach")
	pl := &plan.Plan{Order: []string{rel.Pkg.Name}, Releases: map[string]*plan.Release{rel.Pkg.Name: rel}}
	require.NoError(t, w.verifyPushBranches(context.Background(), pl))

	require.NoError(t, w.verifyPublishBranch(context.Background(), rel))
}

func TestWorkspaceDetachedControlCheckpointIsRejectedBeforePublish(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	control.repo.Commit.Push = true
	verify := false
	control.repo.Commit.Verify = &verify
	recordGit(t, control.repo.Root, "checkout", "-q", "--detach")
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat: nested source record")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	rel = pinnedRelease(rel, pin)
	pl := &plan.Plan{Order: []string{rel.Pkg.Name}, Releases: map[string]*plan.Release{rel.Pkg.Name: rel}}
	require.NoError(t, w.verifyPushBranches(context.Background(), pl))

	err := w.verifyPublishBranch(context.Background(), rel)
	require.Error(t, err)
	assert.Equal(t, "E337", config.DiagnosticCode(err))
	assert.Contains(t, err.Error(), "control checkpoint")
}

func TestWorkspaceCommitWithoutPushKeepsControlCheckpointLocal(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	control.repo.Commit.Push = true
	control.repo.Commit.Remote = "missing-control-remote"
	require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte("release change"), 0o644))
	pl := &plan.Plan{
		Order:    []string{rel.Pkg.Name},
		Releases: map[string]*plan.Release{rel.Pkg.Name: rel},
		RepositoryHeads: map[string]string{
			"source":                 recordGit(t, source.repo.Root, "rev-parse", "HEAD"),
			config.ControlRepository: recordGit(t, control.repo.Root, "rev-parse", "HEAD"),
		},
	}
	controlBefore := pl.RepositoryHeads[config.ControlRepository]
	t.Setenv("DISPAT_OUTPUT", "")

	require.NoError(t, w.app.commitWorkspace(context.Background(), pl, []string{rel.Pkg.Name}, CommitOptions{Tag: true}))

	sourcePin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	assert.NotEqual(t, pl.RepositoryHeads["source"], sourcePin)
	assert.Equal(t, sourcePin, recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName()+"^{commit}"))
	assert.NotEqual(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
	assert.Equal(t, sourcePin, recordGit(t, control.repo.Root, "rev-parse", "HEAD:"+source.repo.GitlinkPath))
	assert.True(t, w.app.workspace.RepositoryByName(config.ControlRepository).Commit.Push,
		"the invocation-local no-push override must not mutate the configured policy")
}
