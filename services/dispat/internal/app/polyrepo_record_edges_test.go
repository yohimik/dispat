package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func addRecordBareRemote(t *testing.T, repository, name string) string {
	t.Helper()
	remote := t.TempDir()
	recordGit(t, remote, "init", "--bare", "-q")
	recordGit(t, repository, "remote", "add", name, remote)
	recordGit(t, repository, "push", "-q", name, "HEAD:refs/heads/main")
	return remote
}

func TestWorkspaceRecordVerificationRejectsInvalidAndStaleBranches(t *testing.T) {
	t.Run("invalid explicit branch", func(t *testing.T) {
		w, _ := recordFixture(t, true, false)
		source := w.byName["source"]
		source.repo.Commit.Push = true
		source.repo.Commit.Branch = "release..invalid"
		verify := false
		source.repo.Commit.Verify = &verify

		err := w.verifySelected(t.Context(), map[string]bool{"source": true})
		require.Error(t, err)
		assert.Equal(t, "E337", config.DiagnosticCode(err))
		assert.Empty(t, source.branch)
	})

	t.Run("remote branch advanced", func(t *testing.T) {
		w, _ := recordFixture(t, true, false)
		source := w.byName["source"]
		remote := addRecordBareRemote(t, source.repo.Root, "publish")
		source.repo.Commit.Push = true
		source.repo.Commit.Remote = "publish"
		base := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat: remote-only release")
		recordGit(t, source.repo.Root, "push", "-q", "publish", "HEAD:refs/heads/main")
		remoteTip := recordGit(t, remote, "rev-parse", "refs/heads/main")
		recordGit(t, source.repo.Root, "reset", "--hard", "-q", base)

		err := w.verifySelected(t.Context(), map[string]bool{"source": true})
		require.ErrorContains(t, err, "behind remote branch main")
		assert.Equal(t, "main", source.branch)
		assert.NotEqual(t, base, remoteTip)
	})
}

func TestWorkspaceBranchOnlyCheckpointRequiresAndPublishesRemoteSourceRevision(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	sourceRemote := addRecordBareRemote(t, source.repo.Root, "publish-source")
	controlRemote := addRecordBareRemote(t, control.repo.Root, "publish-control")
	source.repo.Commit.Remote = "publish-source"
	control.repo.Commit.Remote = "publish-control"
	control.repo.Commit.Push = true
	source.branch, control.branch = "main", "main"

	require.NoError(t, os.WriteFile(filepath.Join(source.repo.Root, "branch-record"), []byte("release"), 0o644))
	recordGit(t, source.repo.Root, "add", "branch-record")
	recordGit(t, source.repo.Root, "commit", "-qm", "feat(lib): branch-only source record")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	recordGit(t, source.repo.Root, "push", "-q", "publish-source", "HEAD:refs/heads/main")
	source.expectedHead = pin
	control.expectedHead = recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	pinned := pinnedRelease(rel, pin)

	require.NoError(t, w.checkpoint(t.Context(), source, pinned, ""))
	controlPin := recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	assert.Equal(t, pin, recordGit(t, sourceRemote, "rev-parse", "refs/heads/main"))
	assert.Equal(t, controlPin, recordGit(t, controlRemote, "rev-parse", "refs/heads/main"))
	assert.Equal(t, pin, recordGit(t, controlRemote, "rev-parse", "refs/heads/main:source"))
}

func TestWorkspaceBranchOnlyCheckpointRefusesUnpublishedSourceRevision(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	addRecordBareRemote(t, source.repo.Root, "publish-source")
	addRecordBareRemote(t, control.repo.Root, "publish-control")
	source.repo.Commit.Remote = "publish-source"
	control.repo.Commit.Remote = "publish-control"
	control.repo.Commit.Push = true
	source.branch, control.branch = "main", "main"
	controlBefore := recordGit(t, control.repo.Root, "rev-parse", "HEAD")

	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat(lib): local-only source record")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	source.expectedHead = pin
	err := w.checkpoint(t.Context(), source, pinnedRelease(rel, pin), "")
	require.ErrorContains(t, err, "control checkpoint cannot be pushed")
	assert.Contains(t, err.Error(), "not available as publish-source/main")
	assert.Equal(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
}

func TestWorkspaceChangelogFailureKeepsTagAndSkipsCheckpoint(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	controlBefore := recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	rel.Pkg.Changelog.Enabled = true
	rel.Pkg.Changelog.File = "blocked-changelog"
	require.NoError(t, os.Mkdir(filepath.Join(rel.Pkg.Dir, rel.Pkg.Changelog.File), 0o755))

	err := w.Record(t.Context(), rel)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "directory")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	assert.Equal(t, pin, recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName()+"^{commit}"))
	assert.Equal(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"),
		"a failed changelog cannot produce a complete control checkpoint")
}

func TestWorkspaceInheritedPinFailureStopsAfterDurableSourceCommit(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	controlRoot := control.repo.Root
	configPath := filepath.Join(controlRoot, "dispat.json")
	require.NoError(t, os.WriteFile(configPath, []byte("{}"), 0o600))
	store, err := workspaceenv.NewLivePins(controlRoot, configPath,
		map[string]string{"PACKAGE_LIB": "source"}, []string{"source"})
	require.NoError(t, err)
	t.Setenv(workspaceenv.LivePins, strings.TrimPrefix(store.Environment(), workspaceenv.LivePins+"="))
	t.Setenv(workspaceenv.Owners, `{"PACKAGE_LIB":"source"}`)
	t.Setenv(workspaceenv.Repositories, `["source"]`)
	require.NoError(t, store.Close(), "make the inherited coordinator stale before recording")
	w.pins.inherited, w.pins.root, w.pins.config = true, controlRoot, configPath
	for _, repository := range w.ordered {
		repository.expectedHead = recordGit(t, repository.repo.Root, "rev-parse", "HEAD")
	}
	sourceBefore := source.expectedHead
	controlBefore := control.expectedHead
	rel.Pkg.Changelog.Enabled = true

	err = w.Record(t.Context(), rel)
	require.ErrorContains(t, err, "source release commit failed")
	assert.Contains(t, err.Error(), "opening live pin context")
	assert.NotEqual(t, sourceBefore, recordGit(t, source.repo.Root, "rev-parse", "HEAD"),
		"the source commit is durable even when publishing its inherited pin fails")
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
	assert.Equal(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
	assert.Empty(t, w.pins.environment(nil), "a failed live publication is not admitted in memory")
}

func TestWorkspaceRecordOwnerAndControlCleanupRouting(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	missing := *rel
	missingPkg := *rel.Pkg
	missingPkg.Repository = "missing-source"
	missing.Pkg = &missingPkg
	require.ErrorContains(t, w.Record(t.Context(), &missing), "no repository owner")
	require.ErrorContains(t, w.verifyPublishBranch(t.Context(), &missing), "missing repository owner")
	require.ErrorContains(t, w.prepare(t.Context(), &plan.Plan{
		Order: []string{missingPkg.Name}, Releases: map[string]*plan.Release{missingPkg.Name: &missing},
	}), "missing repository owner")
	unlock, err := w.acquirePublish(t.Context(), nil)
	require.NoError(t, err)
	unlock()
	_, err = w.acquirePublish(t.Context(), &missing)
	require.ErrorContains(t, err, "no repository owner")
	err = w.verifyPlannedHeads(nil)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))

	control := w.byName[config.ControlRepository]
	cleanupDir := filepath.Join(control.repo.Root, "generated")
	require.NoError(t, os.Mkdir(cleanupDir, 0o755))
	tracked := filepath.Join(cleanupDir, "tracked")
	require.NoError(t, os.WriteFile(tracked, []byte("original"), 0o644))
	recordGit(t, control.repo.Root, "add", "generated/tracked")
	recordGit(t, control.repo.Root, "commit", "-qm", "chore: seed control package")
	control.expectedHead = recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(tracked, []byte("changed"), 0o644))
	untracked := filepath.Join(cleanupDir, "temporary")
	require.NoError(t, os.WriteFile(untracked, []byte("remove"), 0o644))
	require.NoError(t, w.RevertDir(t.Context(), cleanupDir))
	assert.NoFileExists(t, untracked)
	contents, err := os.ReadFile(tracked)
	require.NoError(t, err)
	assert.Equal(t, "original", string(contents))
}

func TestWorkspaceSnapshotRejectsDeletedReleaseTag(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	recordGit(t, source.repo.Root, "tag", rel.TagName())
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	recordGit(t, source.repo.Root, "tag", "-d", rel.TagName())

	err := w.verifySnapshot(t.Context(), rel)
	require.Error(t, err)
	assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
	assert.ErrorContains(t, err, "relevant tag "+rel.TagName()+" at")
	assert.ErrorContains(t, err, "was deleted")
}
