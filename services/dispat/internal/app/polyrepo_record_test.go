package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func recordGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func recordFixture(t *testing.T, sourceCommit, controlCommit bool) (*workspaceRecorder, *plan.Release) {
	t.Helper()
	controlCfg := &config.File{Commit: &config.CommitConfig{Enabled: &controlCommit}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	sourceCfg := &config.File{Commit: &config.CommitConfig{Enabled: &sourceCommit, Name: "Source Bot", Email: "source@example.test"}, Run: &config.RunConfig{}, UnsafeDisableLock: true}
	control, a := guardRepo(t, controlCfg)
	origin, _ := guardRepo(t, sourceCfg)
	require.NoError(t, os.MkdirAll(filepath.Join(origin, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(origin, "pkg", "input"), []byte("source"), 0o644))
	recordGit(t, origin, "add", "pkg")
	recordGit(t, origin, "commit", "-qm", "feat(lib): package")
	recordGit(t, control, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--name", "source", "--", origin, "source")
	recordGit(t, control, "commit", "-qam", "chore: pin source")
	source := filepath.Join(control, "source")
	recordGit(t, source, "config", "user.name", "Test")
	recordGit(t, source, "config", "user.email", "test@example.test")
	a.workspace = &config.Workspace{ControlRoot: control, Repositories: []config.Repository{
		{Name: config.ControlRepository, Root: control, Config: controlCfg, Commit: controlCfg.Commit, Control: true},
		{Name: "source", Root: source, GitlinkPath: "source", Config: sourceCfg, Commit: sourceCfg.Commit},
	}}
	p := &model.Package{Name: "lib", Repository: "source", RepoRoot: source, Dir: filepath.Join(source, "pkg"), Space: &model.Space{Name: "libs"}}
	rel := &plan.Release{Pkg: p, Channel: "stable", Next: ccme.Version{Major: 1}, Bump: ccme.BumpMinor, NewWork: true}
	return a.newWorkspaceRecorder(), rel
}

func TestWorkspaceIncludesRespectSourceAndControlOwnership(t *testing.T) {
	w, _ := recordFixture(t, true, true)
	source := w.byName["source"]
	source.repo.Commit.Include = []string{"pkg/input"}
	dirs, err := w.includeDirs(source, nil)
	require.NoError(t, err)
	assert.Contains(t, dirs, filepath.Join(source.repo.Root, "pkg", "input"))
	control := w.byName[config.ControlRepository]
	control.repo.Commit.Include = []string{"source/pkg/input"}
	_, err = w.includeDirs(control, nil)
	assert.ErrorContains(t, err, "spans repository source")
}

func TestWorkspaceRecordsRejectUnplannedSourceHead(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			w, rel := recordFixture(t, commit, true)
			source := w.byName["source"]
			source.expectedHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")
			recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix: unrelated concurrent commit")
			err := w.Record(t.Context(), rel)
			require.ErrorContains(t, err, "E330")
			assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
		})
	}
}

func TestWorkspaceRecordsRejectExistingReleaseTagAtAnotherCommit(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	old := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	recordGit(t, source.repo.Root, "tag", rel.TagName())
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "feat(lib): later release")
	source.expectedHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")

	err := w.Record(t.Context(), rel)
	require.Error(t, err)
	assert.Equal(t, old, recordGit(t, source.repo.Root, "rev-parse", rel.TagName()+"^{commit}"))
}

func TestWorkspaceRecordsAdvanceOwnedHeads(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	for _, r := range w.ordered {
		r.expectedHead = recordGit(t, r.repo.Root, "rev-parse", "HEAD")
	}
	for i := range 2 {
		rel.Next.Patch = uint64(i)
		require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte(fmt.Sprint(i)), 0o644))
		require.NoError(t, w.Record(t.Context(), rel))
		for _, r := range w.ordered {
			assert.Equal(t, recordGit(t, r.repo.Root, "rev-parse", "HEAD"), r.expectedHead)
		}
	}
}

func TestWorkspaceRevertRefusesUnplannedSourceHead(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	source := w.byName["source"]
	source.expectedHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix: unrelated work")
	path := filepath.Join(rel.Pkg.Dir, "input")
	require.NoError(t, os.WriteFile(path, []byte("keep my edit"), 0o644))
	assert.ErrorContains(t, w.RevertDir(t.Context(), rel.Pkg.Dir), "E330")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep my edit", string(content))
}

func TestWorkspaceRecordsHonorOptionalCommits(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		source, control, write bool
	}{
		{"tag only", false, false, true},
		{"control enabled source disabled", false, true, true},
		{"source only", true, false, true},
		{"source then control", true, true, true},
		{"no empty commits", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, rel := recordFixture(t, tc.source, tc.control)
			r := w.byName["source"]
			control := w.byName[config.ControlRepository]
			before := recordGit(t, r.repo.Root, "rev-parse", "HEAD")
			controlBefore := recordGit(t, control.repo.Root, "rev-parse", "HEAD")
			rel.Pkg.Changelog.Enabled = tc.write
			require.NoError(t, w.Record(context.Background(), rel))
			after := recordGit(t, r.repo.Root, "rev-parse", "HEAD")
			assert.Equal(t, after, recordGit(t, r.repo.Root, "rev-parse", rel.TagName()+"^{commit}"))
			assert.Empty(t, recordGit(t, control.repo.Root, "tag", "--list"), "no source tag at control")
			if tc.source && tc.write {
				assert.NotEqual(t, before, after)
				assert.Equal(t, "Source Bot <source@example.test>", recordGit(t, r.repo.Root, "log", "-1", "--format=%an <%ae>"))
			} else {
				assert.Equal(t, before, after)
			}
			if tc.control && tc.source && tc.write {
				assert.NotEqual(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
				assert.Equal(t, after, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
			} else {
				assert.Equal(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
			}
		})
	}
}

func TestControlSourceDiffersReadsExactGitlinkEntry(t *testing.T) {
	w, _ := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")

	differs, err := controlSourceDiffers(context.Background(), control, source, pin)
	require.NoError(t, err)
	assert.False(t, differs)

	require.NoError(t, os.WriteFile(filepath.Join(source.repo.Root, "new"), []byte("change"), 0o644))
	recordGit(t, source.repo.Root, "add", "new")
	recordGit(t, source.repo.Root, "commit", "-qm", "feat: next source revision")
	differs, err = controlSourceDiffers(context.Background(), control, source,
		recordGit(t, source.repo.Root, "rev-parse", "HEAD"))
	require.NoError(t, err)
	assert.True(t, differs)
}

func TestWorkspaceCheckpointUsesLogicalGitlinkWithCheckoutAlias(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	alias := filepath.Join(t.TempDir(), "checkout-alias")
	require.NoError(t, os.Symlink(source.repo.Root, alias))
	source.repo.Root = alias
	source.repo.GitlinkPath = "./source"
	rel.Pkg.Changelog.Enabled = true
	before := recordGit(t, control.repo.Root, "rev-parse", "HEAD:source")
	require.NoError(t, w.Record(t.Context(), rel))
	current := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	assert.NotEqual(t, before, current)
	assert.Equal(t, current, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
	assert.Equal(t, current, recordGit(t, source.repo.Root, "rev-parse", rel.TagName()+"^{commit}"))
}

func TestRecordPathsCompareCanonicalRepositoryRoot(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	rel.Pkg.Changelog.Enabled = true
	rel.Pkg.Changelog.File = "CHANGELOG.md"

	require.NoError(t, validateRecordPath(source, rel))
	source.repo.Commit.Include = []string{"pkg/generated.txt"}
	_, err := w.includeDirs(source, nil)
	require.NoError(t, err)
}

func TestWorkspaceCheckpointFailurePreservesSource(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	control := w.byName[config.ControlRepository]
	source := w.byName["source"]
	before := recordGit(t, control.repo.Root, "rev-parse", "HEAD:source")
	require.NoError(t, os.WriteFile(filepath.Join(control.repo.Root, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	rel.Pkg.Changelog.Enabled = true
	err := w.Record(context.Background(), rel)
	require.ErrorContains(t, err, "control checkpoint failed")
	sha := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	assert.Contains(t, err.Error(), sha)
	assert.Equal(t, sha, recordGit(t, source.repo.Root, "rev-parse", rel.TagName()+"^{commit}"))
	assert.Equal(t, before, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
}

func TestWorkspaceSourceCommitFailureDoesNotTag(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	hooks := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	recordGit(t, source.repo.Root, "config", "core.hooksPath", hooks)
	before := recordGit(t, control.repo.Root, "rev-parse", "HEAD:source")
	rel.Pkg.Changelog.Enabled = true
	require.ErrorContains(t, w.Record(context.Background(), rel), "source release commit failed")
	assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list"))
	assert.Equal(t, before, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
}

func TestWorkspaceCannotPushCheckpointForUnpushedSource(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	control.repo.Commit.Push = true
	control.branch = recordGit(t, control.repo.Root, "branch", "--show-current")
	before := recordGit(t, control.repo.Root, "rev-parse", "HEAD:source")
	rel.Pkg.Changelog.Enabled = true
	require.ErrorContains(t, w.Record(context.Background(), rel), "control checkpoint cannot be pushed")
	assert.Equal(t, before, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
	assert.Equal(t, rel.TagName(), recordGit(t, source.repo.Root, "tag", "--list"))
}

func TestWorkspaceSourcePushFailureDoesNotCheckpoint(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	source.repo.Commit.Push = true
	source.repo.Commit.Remote = "missing-release-remote"
	source.branch = "main"
	control := w.byName[config.ControlRepository]
	before := recordGit(t, control.repo.Root, "rev-parse", "HEAD:source")
	rel.Pkg.Changelog.Enabled = true
	require.ErrorContains(t, w.Record(context.Background(), rel), "source push failed")
	assert.Equal(t, before, recordGit(t, control.repo.Root, "rev-parse", "HEAD:source"))
	assert.Equal(t, rel.TagName(), recordGit(t, source.repo.Root, "tag", "--list"))
}

func TestWorkspaceRecordPreflightProtectsSourceAndIncludes(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	pl := &plan.Plan{Order: []string{"lib"}, Releases: map[string]*plan.Release{"lib": rel}}
	require.NoError(t, w.prepare(context.Background(), pl))
	require.NoError(t, os.WriteFile(filepath.Join(rel.Pkg.Dir, "input"), []byte("local work"), 0o644))
	require.ErrorContains(t, w.prepare(context.Background(), pl), "pre-existing local changes")
	source := w.byName["source"]
	source.repo.Commit.Include = []string{"../f.txt"}
	_, err := w.includeDirs(source, nil)
	require.ErrorContains(t, err, "escapes its owner")
	control := w.byName[config.ControlRepository]
	control.repo.Commit.Include = []string{"."}
	_, err = w.includeDirs(control, nil)
	require.ErrorContains(t, err, "spans repository")
	control.repo.Commit.Include = []string{"source/pkg/input"}
	_, err = w.includeDirs(control, nil)
	require.ErrorContains(t, err, "spans repository")
	external := t.TempDir()
	require.NoError(t, os.Symlink(external, filepath.Join(source.repo.Root, "outside")))
	source.repo.Commit.Include = []string{"outside/future.txt"}
	_, err = w.includeDirs(source, nil)
	require.ErrorContains(t, err, "escapes its owner")
	rel.Pkg.Changelog.Enabled = true
	rel.Pkg.Changelog.File = "../outside/future.txt"
	require.ErrorContains(t, w.Record(context.Background(), rel), "changelog path")
	assert.NoFileExists(t, filepath.Join(external, "future.txt"))
}

func TestWorkspaceIncludeCacheRejectsSymlinkRetargetAfterPreflight(t *testing.T) {
	w, _ := recordFixture(t, true, true)
	source := w.byName["source"]
	inside := filepath.Join(source.repo.Root, "generated")
	require.NoError(t, os.MkdirAll(inside, 0o755))
	link := filepath.Join(source.repo.Root, "include-link")
	require.NoError(t, os.Symlink(inside, link))
	source.repo.Commit.Include = []string{"include-link/future.txt"}
	_, err := w.includeDirs(source, nil)
	require.NoError(t, err)

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(t.TempDir(), link))
	_, err = w.includeDirs(source, nil)
	require.ErrorContains(t, err, "changed its resolved owner after preflight")
}

func TestWorkspaceIncludeCacheRejectsRepositoryRootRetarget(t *testing.T) {
	w, _ := recordFixture(t, true, true)
	source := w.byName["source"]
	link := filepath.Join(t.TempDir(), "source-root")
	require.NoError(t, os.Symlink(source.repo.Root, link))
	source.repo.Root = link
	source.repo.Commit.Include = []string{"pkg/input"}
	_, err := w.includeDirs(source, nil)
	require.NoError(t, err)

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(t.TempDir(), link))
	_, err = w.includeDirs(source, nil)
	require.ErrorContains(t, err, "root changed its resolved location after preflight")
}

func TestPinnedReleaseCopiesOutputsWithoutMutatingPlan(t *testing.T) {
	_, rel := recordFixture(t, false, false)
	rel.Outputs = []plan.Output{{Name: "TOKEN", Value: "original", Source: "lib:build"}}
	pinned := pinnedRelease(rel, strings.Repeat("a", 40))

	assert.Empty(t, rel.ExportedCommit())
	assert.Equal(t, strings.Repeat("a", 40), pinned.ExportedCommit())
	pinned.Outputs[0].Value = "changed"
	assert.Equal(t, "original", rel.Outputs[0].Value, "the recorder must not mutate the shared plan release")
}

func TestWorkspaceCheckpointRejectsInterveningSourceHead(t *testing.T) {
	w, rel := recordFixture(t, true, true)
	source := w.byName["source"]
	control := w.byName[config.ControlRepository]
	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	pinned := pinnedRelease(rel, pin)
	recordGit(t, source.repo.Root, "tag", "-a", rel.TagName(), "-m", "release")
	require.NoError(t, os.WriteFile(filepath.Join(source.repo.Root, "intervening"), []byte("change"), 0o644))
	recordGit(t, source.repo.Root, "add", "intervening")
	recordGit(t, source.repo.Root, "commit", "-qm", "chore: intervening commit")
	controlBefore := recordGit(t, control.repo.Root, "rev-parse", "HEAD")

	err := w.checkpoint(context.Background(), source, pinned, rel.TagName())
	require.ErrorContains(t, err, "HEAD moved from recorded source revision")
	assert.Contains(t, err.Error(), pin)
	assert.Equal(t, controlBefore, recordGit(t, control.repo.Root, "rev-parse", "HEAD"))
}

func TestSourceRecordTargetsIgnoreControlEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "control/workspace")
	t.Setenv("GITHUB_TOKEN", "test-token")
	w, rel := recordFixture(t, false, false)
	a := w.app
	source := w.byName["source"]
	recordGit(t, source.repo.Root, "remote", "set-url", "origin", "git@github.com:source/project.git")
	rel.Pkg.GitHub.Enabled = true
	require.NoError(t, a.resolveRepositoryRecords(context.Background(), []*model.Package{rel.Pkg}))
	assert.Equal(t, "source", rel.Pkg.GitHub.Owner)
	assert.Equal(t, "project", rel.Pkg.Changelog.Format.LinkRepo)
	gh, err := githubReleaser(rel.Pkg.GitHub, zerolog.Nop())
	require.NoError(t, err)
	assert.Equal(t, "source", gh.Owner)
	assert.Equal(t, "project", gh.Repo)
	blank := model.GitHubSpec{Enabled: true, Format: model.RecordFormat{RepositoryResolved: true}}
	_, err = githubReleaser(blank, zerolog.Nop())
	require.ErrorContains(t, err, "no repository configured")
}

func TestGitHubCoordinates(t *testing.T) {
	for _, tc := range []struct{ remote, api, owner, repo string }{
		{"https://github.com/team/project.git", "", "team", "project"},
		{"ssh://git@github.com/team/project.git", "", "team", "project"},
		{"git@github.com:team/project.git", "", "team", "project"},
		{"git@enterprise.test:team/project.git", "https://enterprise.test/api/v3", "team", "project"},
		{"https://github.com/team/project.git", "https://api.github.com", "team", "project"},
		{"/local/repository", "", "", ""},
		{"https://gitlab.com/team/project.git", "", "", ""},
		{"https://github.com/team/group/project.git", "", "", ""},
	} {
		t.Run(tc.remote+tc.api, func(t *testing.T) {
			owner, repo := githubCoordinates(tc.remote, tc.api)
			assert.Equal(t, tc.owner, owner)
			assert.Equal(t, tc.repo, repo)
		})
	}
}
