package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/github"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestWorkspaceControlPushFailureRetainsPublishedSourceAndLocalCheckpoint(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	sourceRemote := addRecordBareRemote(t, source.repo.Root, "publish-source")
	source.repo.Commit.Push = true
	source.repo.Commit.Remote = "publish-source"
	control.repo.Commit.Push = true
	control.repo.Commit.Remote = "missing-control-remote"
	source.branch, control.branch = "main", "main"
	rel.Pkg.Space.AliasTags = []model.AliasTag{
		{Format: "{name}-v{major}", Force: true},
		{Format: "{name}-v{major}.{minor}"},
	}
	source.repo.Config.Scripts = map[string]config.Script{
		"source-before": {"touch source-before-push"},
		"source-after":  {"touch source-after-push"},
	}
	source.repo.Config.Run.BeforePush = []string{"source-before"}
	source.repo.Config.Run.AfterPush = []string{"source-after"}
	control.repo.Config.Scripts = map[string]config.Script{
		"control-before": {"touch control-before-push"},
		"control-after":  {"touch control-after-push"},
	}
	control.repo.Config.Run.BeforePush = []string{"control-before"}
	control.repo.Config.Run.AfterPush = []string{"control-after"}
	for _, repository := range w.ordered {
		repository.expectedHead = recordGit(t, repository.repo.Root, "rev-parse", "HEAD")
	}
	controlBefore := control.expectedHead
	rel.Pkg.Changelog.Enabled = true

	err := w.Record(t.Context(), rel)
	require.ErrorContains(t, err, "control checkpoint failed after source source recorded tag")
	assert.Contains(t, err.Error(), "missing-control-remote")
	sourcePin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	controlPin := recordGit(t, control.repo.Root, "rev-parse", "HEAD")
	assert.NotEqual(t, controlBefore, controlPin, "the local checkpoint is a durable partial outcome")
	assert.Equal(t, sourcePin, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
	for _, tag := range []string{rel.TagName(), "lib-v1", "lib-v1.0"} {
		assert.Equal(t, sourcePin, recordGit(t, sourceRemote, "rev-parse", "refs/tags/"+tag+"^{commit}"))
	}
	assert.FileExists(t, filepath.Join(source.repo.Root, "source-before-push"))
	assert.FileExists(t, filepath.Join(source.repo.Root, "source-after-push"))
	assert.FileExists(t, filepath.Join(control.repo.Root, "control-before-push"))
	assert.NoFileExists(t, filepath.Join(control.repo.Root, "control-after-push"),
		"afterPush cannot run for the rejected control publication")
}

func TestWorkspaceOwnerPreflightUsesImportedBranchAndIncludePolicy(t *testing.T) {
	t.Run("owner branch policy", func(t *testing.T) {
		w, rel := recordFixture(t, true, false)
		source := w.byName["source"]
		source.repo.Config.Run.AllowBranch = []string{"release/*"}
		pl := workspaceCommitPlan(t, w, rel)

		err := w.prepare(t.Context(), pl)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository source")
		assert.Contains(t, err.Error(), "main")
	})

	t.Run("cyclic include", func(t *testing.T) {
		w, rel := recordFixture(t, true, false)
		source := w.byName["source"]
		if err := os.Symlink("include-loop", filepath.Join(source.repo.Root, "include-loop")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		source.repo.Commit.Include = []string{"include-loop/generated"}
		pl := workspaceCommitPlan(t, w, rel)
		before := pl.RepositoryHeads["source"]

		_, err := w.releaseCommitNeeded(t.Context(), source, rel)
		require.Error(t, err)
		err = w.verifyPublishBranch(t.Context(), rel)
		require.Error(t, err)
		err = w.prepare(t.Context(), pl)
		require.Error(t, err)
		assert.Equal(t, before, recordGit(t, source.repo.Root, "rev-parse", "HEAD"),
			"an invalid owner include must fail before Git mutation")
	})
}

func TestWorkspaceControlPackagePublishGuardDoesNotCreateCheckpoint(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	control := w.byName[config.ControlRepository]
	controlPkg := *rel.Pkg
	controlPkg.Name = "control-tool"
	controlPkg.Repository = config.ControlRepository
	controlPkg.RepoRoot = control.repo.Root
	controlPkg.Dir = control.repo.Root
	controlPkg.Space = &model.Space{Name: "tools", Repository: config.ControlRepository, RepoRoot: control.repo.Root}
	controlRel := *rel
	controlRel.Pkg = &controlPkg
	require.NoError(t, w.verifyPublishBranch(t.Context(), &controlRel))
}

func TestWorkspaceRejectsInvalidNestedPinAndChangedTagProof(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	badPin := "0123456789abcdef0123456789abcdef01234567"
	bad := pinnedRelease(rel, badPin)

	_, err := w.tagSource(t.Context(), source, bad, "")
	require.Error(t, err)
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	err = verifyPinnedSource(t.Context(), source, pinnedRelease(rel, pin), "missing-release-tag")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-release-tag")
	require.ErrorContains(t, verifyRemoteSource(t.Context(), source, "", pin), "no verified remote tag or branch")

	old := pin
	recordGit(t, source.repo.Root, "tag", rel.TagName())
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat(lib): later source state")
	current := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	changed := pinnedRelease(rel, current)
	err = verifyPinnedSource(t.Context(), source, changed, rel.TagName())
	require.ErrorContains(t, err, "tag "+rel.TagName()+" moved")
	assert.Contains(t, err.Error(), old)
	assert.Contains(t, err.Error(), current)
}

func TestWorkspaceCheckpointRejectsChangedControlHeadAndInvalidGitlink(t *testing.T) {
	t.Run("control moved", func(t *testing.T) {
		w, rel := recordFixture(t, false, true)
		source := w.byName["source"]
		control := w.byName[config.ControlRepository]
		pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		recordGit(t, source.repo.Root, "tag", rel.TagName())
		source.expectedHead = pin
		control.expectedHead = recordGit(t, control.repo.Root, "rev-parse", "HEAD")
		recordGit(t, control.repo.Root, "commit", "--allow-empty", "-qm", "fix: unrelated control update")

		err := w.checkpoint(t.Context(), source, pinnedRelease(rel, pin), rel.TagName())
		require.Error(t, err)
		assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
		assert.Contains(t, err.Error(), "control checkpoint failed")
	})

	t.Run("invalid logical gitlink", func(t *testing.T) {
		w, rel := recordFixture(t, false, true)
		source := w.byName["source"]
		pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		recordGit(t, source.repo.Root, "tag", rel.TagName())
		source.expectedHead = pin
		source.repo.GitlinkPath = "../source"

		err := w.checkpoint(t.Context(), source, pinnedRelease(rel, pin), rel.TagName())
		require.ErrorContains(t, err, "no valid gitlink path")
	})

	t.Run("missing logical gitlink", func(t *testing.T) {
		w, rel := recordFixture(t, false, true)
		source := w.byName["source"]
		pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		recordGit(t, source.repo.Root, "tag", rel.TagName())
		source.expectedHead = pin
		source.repo.GitlinkPath = "missing-source"

		err := w.checkpoint(t.Context(), source, pinnedRelease(rel, pin), rel.TagName())
		require.ErrorContains(t, err, "has no gitlink")
	})
}

func TestWorkspaceRecordPathsRejectCyclicChangelogAndMissingOwnerTargets(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	if err := os.Symlink("changelog-loop", filepath.Join(rel.Pkg.Dir, "changelog-loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rel.Pkg.Changelog.Enabled = true
	rel.Pkg.Changelog.File = "changelog-loop/CHANGELOG.md"
	err := w.Record(t.Context(), rel)
	require.Error(t, err)
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))

	missing := *rel.Pkg
	missing.Repository = "missing-source"
	err = w.app.resolveRepositoryRecords(t.Context(), []*model.Package{&missing})
	require.ErrorContains(t, err, "has no repository owner")
	owner, repo := githubCoordinates("https://%invalid", "")
	assert.Empty(t, owner)
	assert.Empty(t, repo)
	owner, repo = githubCoordinates("https://github.com/team/repo.git", "https://%invalid")
	assert.Empty(t, owner)
	assert.Empty(t, repo)
}

func TestWorkspaceRecordKeepsSourceTagWhenGitHubRecordingFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rejected", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	rel.Pkg.GitHub.Enabled = true
	w.gh = &ghDispatch{byPkg: map[string]*github.Releaser{
		rel.Pkg.Name: {
			APIURL: server.URL, Owner: "owner", Repo: "repo", Token: "token",
			AllPackages: true, Client: server.Client(),
		},
	}}

	err := w.Record(t.Context(), rel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	assert.Equal(t, pin, recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName()+"^{commit}"),
		"GitHub failure cannot erase the durable source tag")
}

func TestWorkspaceSnapshotFailsClosedWhenRepositoryDisappears(t *testing.T) {
	t.Run("before capture", func(t *testing.T) {
		w, rel := recordFixture(t, false, false)
		w.byName["source"].git.Dir = filepath.Join(t.TempDir(), "missing-source")
		err := w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg})
		require.Error(t, err)
		assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
	})

	t.Run("HEAD unreadable after capture", func(t *testing.T) {
		w, rel := recordFixture(t, false, false)
		require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
		pl := snapshotPlan(t, w, rel)
		w.setSnapshotPlan(pl)
		require.NoError(t, w.prepare(t.Context(), pl))
		source := w.byName["source"]
		headPath := recordGit(t, source.repo.Root, "rev-parse", "--git-path", "HEAD")
		if !filepath.IsAbs(headPath) {
			headPath = filepath.Join(source.repo.Root, headPath)
		}
		saved, err := os.ReadFile(headPath)
		require.NoError(t, err)
		// Retain a valid Git directory so the repository is taken; the
		// unborn reference makes the subsequent HEAD read fail.
		require.NoError(t, os.WriteFile(headPath, []byte("ref: refs/heads/missing-snapshot\n"), 0o644))
		t.Cleanup(func() { _ = os.WriteFile(headPath, saved, 0o644) })

		err = w.verifySnapshot(t.Context(), rel)
		require.Error(t, err)
		assert.Equal(t, config.DiagnosticRepositoryInvalid, config.DiagnosticCode(err))
		assert.Contains(t, err.Error(), "reading snapshot HEAD")
	})
}

func TestWorkspaceRecordPropagatesCancellationBeforeTagging(t *testing.T) {
	w, rel := recordFixture(t, false, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := w.Record(ctx, rel)
	require.Error(t, err)
	assert.Empty(t, recordGit(t, w.byName["source"].repo.Root, "tag", "--list", rel.TagName()))
}

func TestWorkspacePinIndexesRejectIncompleteAndAmbiguousOwners(t *testing.T) {
	assert.Nil(t, workspacePinRepositories(nil))
	workspace := &config.Workspace{Repositories: []config.Repository{
		{Name: config.ControlRepository, Control: true},
		{Name: "source-z"},
		{Name: ""},
		{Name: "source-a"},
	}}
	assert.Equal(t, []string{"source-a", "source-z"}, workspacePinRepositories(workspace))
	assert.Empty(t, workspacePinOwners(nil))

	pl := &plan.Plan{Releases: map[string]*plan.Release{
		"nil-release": nil,
		"nil-package": {Pkg: nil},
		"empty-owner": {Pkg: &model.Package{Name: "empty-owner"}},
		"a-b":         {Pkg: &model.Package{Name: "a-b", Repository: "source-a"}},
		"a_b":         {Pkg: &model.Package{Name: "a_b", Repository: "source-z"}},
		"ok":          {Pkg: &model.Package{Name: "ok", Repository: "source-a"}},
	}}
	owners := workspacePinOwners(pl)
	assert.Equal(t, map[string]string{"PACKAGE_OK": "source-a"}, owners,
		"incomplete releases and colliding environment keys cannot authorize a source")
}

func TestWorkspacePinContextStartRejectsMissingConfiguration(t *testing.T) {
	pins := &workspacePins{}
	cleanup, err := pins.start(t.TempDir(), filepath.Join(t.TempDir(), "missing-dispat.json"),
		map[string]string{"PACKAGE_LIB": "source"}, []string{"source"})
	require.Error(t, err)
	assert.Nil(t, cleanup)
	assert.Nil(t, pins.live)
}
