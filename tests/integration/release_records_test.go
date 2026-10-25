// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// A release plans from the records of the store it writes to, read under the
// lock, and writes its own records create-only (CCME SPEC.md §13.2, §19.1).
//
// Every scenario here is a checkout that disagrees with its remote, which is
// what a second clone, a CI runner's `--no-tags` checkout and a run that
// started before another one finished all are. The registry log is the
// evidence that matters: a version published twice is the failure these
// refusals exist to prevent, and a log with two lines in it says so whatever
// the tags ended up looking like.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// recordsConfig is the shape every scenario here shares: a release that
// records to a remote and writes nothing to commit, so two clones of one head
// plan the same version and the branch never tells them apart. The publish
// stage appends the version it published to a log outside the repository,
// which is what a registry is from a test's point of view.
func recordsConfig(registry string) models.File {
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{
		`printf '%s@%s\n' "$DISPAT_PACKAGE" "$DISPAT_NEW_VERSION" >> ` + shellQuote(registry)}
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	return cfg
}

// newRecordsOrigin is the repository a first release runs in, with its bare
// remote and its branch already pushed. The registry is a path outside both,
// so a second clone of the same configuration appends to the same log.
func newRecordsOrigin(t *testing.T, cfg models.File) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	bare := r.AddBareRemote()
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return r, bare
}

// publishedVersions is what the registry log holds, one entry per publish.
func publishedVersions(t *testing.T, registry string) []string {
	t.Helper()
	data, err := os.ReadFile(registry)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// remoteRecord is what a bare repository holds a tag at, peeled, and the
// empty string when it holds no such name. A name that is not there is an
// answer rather than a failure: half these scenarios are about a record the
// store must not have ended up with.
func remoteRecord(t *testing.T, bare, tag string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-list", "-n", "1", tag, "--").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestReleaseRecordsRefuseACloneWithoutTheRemoteTags: the defect this gate
// exists for. A clone made before another run recorded holds no tag for the
// version that run published, so it plans it again, publishes it a second
// time and writes its own tag over the record of the first. The lock does not
// help: the two runs never overlap.
//
// Under the lock the run now compares the store's records with its own and
// refuses before any script runs, and the remedy it names is enough to make
// the same clone correct.
func TestReleaseRecordsRefuseACloneWithoutTheRemoteTags(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	first, bare := newRecordsOrigin(t, recordsConfig(registry))
	// The second checkout is made now, while the remote carries no records at
	// all, which is what makes it stale a moment later.
	stale := harness.Clone(t, bare)

	require.Equal(t, 0, first.CommandEnv(harness.LockEnabled).Code)
	require.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	recorded := remoteRecord(t, bare, "core@0.1.0")
	require.NotEmpty(t, recorded, "the first run recorded on the remote")

	res := stale.CommandEnv(harness.LockEnabled)
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "core@0.1.0", "the record it is missing is named")
	assert.Contains(t, res.Stdout, "git fetch --tags", "with the remedy")
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry),
		"the refusal came before any script ran")
	assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"), "and the record did not move")

	// The lock was given back on the way out, so the up-to-date clone can
	// still release, and the stale one converges on the remedy.
	first.WriteFile("packages/core/more.txt", "more\n")
	first.Commit("feat(core): second")
	first.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	require.Equal(t, 0, first.CommandEnv(harness.LockEnabled).Code, "the lock is free")
	assert.Equal(t, []string{"core@0.1.0", "core@0.2.0"}, publishedVersions(t, registry))

	stale.Git("fetch", "--tags", "-q", "origin")
	stale.Git("merge", "-q", "--ff-only", "origin/"+harness.DefaultBranch)
	healed := stale.CommandEnv(harness.LockEnabled)
	assert.Equal(t, 0, healed.Code, "nothing left to release; stdout:\n%s", healed.Stdout)
	assert.Equal(t, []string{"core@0.1.0", "core@0.2.0"}, publishedVersions(t, registry),
		"and the fetched clone re-publishes nothing")
}

// TestReleaseRecordsNeverMoveAPublishedTag: the `--no-tags` checkout a CI
// runner makes to save a fetch. It sits at a newer head, so the record it
// cannot see is on a commit its own head reaches, and the run that follows
// used to publish the version a third time and move the published tag onto
// its own commit.
func TestReleaseRecordsNeverMoveAPublishedTag(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	first, bare := newRecordsOrigin(t, recordsConfig(registry))
	require.Equal(t, 0, first.CommandEnv(harness.LockEnabled).Code)
	recorded := remoteRecord(t, bare, "core@0.1.0")

	first.WriteFile("packages/core/more.txt", "more\n")
	first.Commit("feat(core): second")
	first.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	runner := harness.CloneWithoutTags(t, bare)
	res := runner.CommandEnv(harness.LockEnabled)
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry), "nothing published")
	assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"),
		"and the published record still names the commit it was written on")
	assert.Empty(t, runner.TagList(), "the refused run wrote no tag of its own")
}

// TestReleaseRecordsRefuseATagAtAnotherCommit: one version named at two
// commits, one in the checkout and one on the remote. No rule decides which
// of them the version really is, so neither side is touched and a person is
// told what to correct.
func TestReleaseRecordsRefuseATagAtAnotherCommit(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	first, bare := newRecordsOrigin(t, recordsConfig(registry))
	require.Equal(t, 0, first.CommandEnv(harness.LockEnabled).Code)
	recorded := remoteRecord(t, bare, "core@0.1.0")

	// A clone that holds the same version somewhere else: the tag was written
	// again, locally, at a later commit.
	other := harness.Clone(t, bare)
	other.Git("tag", "-d", "core@0.1.0")
	other.WriteFile("packages/core/more.txt", "more\n")
	other.Commit("feat(core): second")
	other.Git("tag", "-a", "core@0.1.0", "-m", "the same version, elsewhere")
	local := other.Git("rev-list", "-n1", "core@0.1.0")

	res := other.CommandEnv(harness.LockEnabled)
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E191"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, recorded, "the commit the store recorded")
	assert.Contains(t, res.Stdout, local, "and the one this checkout holds")
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"), "nothing moved on the remote")
	assert.Equal(t, local, other.Git("rev-list", "-n1", "core@0.1.0"), "nor in the checkout")
}

// TestReleaseRecordsIgnoreRemoteTagsOffTheHistory: a record on a commit the
// planned head does not reach cannot change what this run plans, so it is not
// a difference to refuse on. A release from another branch is the ordinary
// way a remote comes to hold one.
func TestReleaseRecordsIgnoreRemoteTagsOffTheHistory(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, bare := newRecordsOrigin(t, recordsConfig(registry))

	// Somebody releases from a branch of their own and pushes only the record.
	r.Git("checkout", "-q", "-b", "elsewhere")
	r.WriteFile("packages/core/sideline.txt", "unrelated\n")
	r.Commit("feat(core): work on another branch")
	r.Git("tag", "-a", "core@9.9.9", "-m", "released from elsewhere")
	r.Git("push", "-q", "origin", "core@9.9.9")
	sideline := r.Git("rev-list", "-n1", "core@9.9.9")
	r.Git("tag", "-d", "core@9.9.9")
	r.Git("checkout", "-q", harness.DefaultBranch)

	res := r.CommandEnv(harness.LockEnabled)
	assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry),
		"the run went ahead on its own history")
	assert.Equal(t, sideline, remoteRecord(t, bare, "core@9.9.9"), "and left the other record alone")
}

// TestReleaseRecordsIgnoreATagWithNoVersionInIt: a name carrying no version
// records no release. `core@backup` has a release tag's shape and states
// nothing this run could publish a second time, and the planner reads no
// baseline and no duplicate out of it either.
func TestReleaseRecordsIgnoreATagWithNoVersionInIt(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, bare := newRecordsOrigin(t, recordsConfig(registry))

	r.Git("tag", "-a", "core@backup", "-m", "a name somebody parked here")
	r.Git("push", "-q", "origin", "core@backup")
	parked := r.Git("rev-list", "-n1", "core@backup")
	r.Git("tag", "-d", "core@backup")

	res := r.CommandEnv(harness.LockEnabled)
	assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	assert.Equal(t, parked, remoteRecord(t, bare, "core@backup"), "and the name is left alone")
}

// TestReleaseRecordsCreateOnlyPush: a record may appear on the store between
// this run's plan and its push. A new name uses a create-only update; a name
// already at the prepared commit is an immutable record this run can finish.
//
// The two answers are opposite. The same commit is this release's own record
// arriving twice, which a partly delivered push leaves behind and which must
// converge. Another commit is somebody else's published record, which is left
// exactly where it is and reported, with the package still published, because
// what failed is the recording and not the release.
func TestReleaseRecordsCreateOnlyPush(t *testing.T) {
	// injectDuringPublish makes a second clone push the tag this run is about
	// to write, while the run is between its publish and its push.
	inject := func(t *testing.T, other *harness.Repo) models.Script {
		t.Helper()
		return models.Script{"git -C " + shellQuote(other.Root) + " push -q origin core@0.1.0"}
	}

	t.Run("a record at another commit is left where it is", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "registry.log")
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		bare := r.AddBareRemote()
		r.Commit("feat(core): first")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

		other := harness.Clone(t, bare)
		other.CommitEmpty("chore: another run's release commit")
		// Lightweight on purpose: a record written by hand carries no tag
		// object, so the store advertises one line for it rather than two.
		other.Git("tag", "core@0.1.0")
		theirs := other.Git("rev-list", "-n1", "core@0.1.0")

		cfg := recordsConfig(registry)
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"build"}, Publish: []string{"publish"},
				PostPublish: []string{"inject"},
			},
		}
		cfg.Scripts["inject"] = inject(t, other)
		r.WriteConfigModel(cfg)
		r.Commit("chore: let another run record while this one publishes")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

		res := r.Release()
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresent(res.Events, "E221"), "stdout:\n%s", res.Stdout)
		assert.Equal(t, theirs, remoteRecord(t, bare, "core@0.1.0"),
			"the record the store published is exactly where it was")
		assert.Contains(t, res.Stdout, "\"status\":\"published\"",
			"the package published; it is the recording that failed")
		assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	})

	t.Run("a store that cannot say what it holds fails the recording", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "registry.log")
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		bare := r.AddBareRemote()
		r.Commit("feat(core): first")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

		other := harness.Clone(t, bare)
		other.CommitEmpty("chore: another run's release commit")
		other.Git("tag", "core@0.1.0")
		theirs := other.Git("rev-list", "-n1", "core@0.1.0")

		cfg := recordsConfig(registry)
		cfg.Spaces["libs"] = models.SpaceConfig{
			Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"build"}, Publish: []string{"publish"},
				PostPublish: []string{"inject"},
			},
		}
		cfg.Scripts["inject"] = inject(t, other)
		r.WriteConfigModel(cfg)
		r.Commit("chore: let another run record while this one publishes")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		// A store that cannot answer the authoritative preflight tag read
		// leaves the run unable to distinguish a retry from a foreign record.
		fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*ls-remote -- origin refs/tags/*"})

		res := r.CommandEnv(fault.Env())
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, 1, fault.Matches(), "the unreadable preflight refuses before any atomic push")
		assert.Contains(t, res.Stdout, "push failed")
		assert.Equal(t, theirs, remoteRecord(t, bare, "core@0.1.0"),
			"and the record the store published is still where it was")
		assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	})

	t.Run("a record at this release's commit is the same record", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "registry.log")
		r := harness.New(t)
		r.SeedPackage("packages", "core")
		bare := r.AddBareRemote()
		r.Commit("feat(core): first")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

		other := harness.Clone(t, bare)

		cfg := recordsConfig(registry)
		cfg.Scripts["inject"] = models.Script{"git -C " + shellQuote(other.Root) + " fetch -q --no-tags " +
			shellQuote(r.Root) + " HEAD && git -C " + shellQuote(other.Root) +
			" tag -a core@0.1.0 -m 'another annotation of this release' FETCH_HEAD && git -C " +
			shellQuote(other.Root) + " push -q origin refs/tags/core@0.1.0"}
		cfg.Run = &models.RunConfig{PostCommit: []string{"inject"}}
		r.WriteConfigModel(cfg)
		r.Commit("chore: let the same record arrive twice")
		r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
		// postCommit runs after the exact release commit and local tag exist.
		// The other clone writes a different annotation of that same target
		// before the required atomic update; the remote annotation must stay.

		res := r.Release()
		assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		same := other.Git("rev-list", "-n1", "core@0.1.0")
		assert.Equal(t, same, remoteRecord(t, bare, "core@0.1.0"))
		assert.Equal(t, other.Git("rev-parse", "refs/tags/core@0.1.0"),
			strings.TrimSpace(bareGit(t, bare, "rev-parse", "refs/tags/core@0.1.0")),
			"the published annotation is not replaced by this run's tag object")
		assert.Empty(t, r.Git("ls-remote", "origin", "refs/dispat/receipts/core@0.1.0"),
			"the same-target record atomically discharges the receipt")
		assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
		again := r.Release()
		assert.Equal(t, 0, again.Code, "same-checkout retry: stdout:\n%s\nstderr:\n%s", again.Stdout, again.Stderr)
		assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry),
			"a retry must not upload the published version again")
		assert.Equal(t, other.Git("rev-parse", "refs/tags/core@0.1.0"),
			strings.TrimSpace(bareGit(t, bare, "rev-parse", "refs/tags/core@0.1.0")),
			"a retry must preserve the remote annotation")
	})
}

// TestReleaseRecordsAliasTagsStillMove: a moving alias is not a record. It is
// re-pointed on every release, on the remote as well as locally, which is the
// one thing the create-only rule must not take away.
func TestReleaseRecordsAliasTagsStillMove(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(),
		AliasTags: []models.AliasTagConfig{{Format: "{name}-v{major}", Moving: true}},
	}
	r, bare := newRecordsOrigin(t, cfg)

	require.Equal(t, 0, r.Release().Code)
	first := remoteRecord(t, bare, "core-v0")
	require.Equal(t, remoteRecord(t, bare, "core@0.1.0"), first, "the alias follows the release")

	r.WriteFile("packages/core/more.txt", "more\n")
	r.Commit("feat(core): second")
	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	second := remoteRecord(t, bare, "core@0.2.0")
	assert.NotEqual(t, first, second)
	assert.Equal(t, second, remoteRecord(t, bare, "core-v0"), "the alias moved on the remote")
	assert.Contains(t, res.Stdout, "moving alias re-pointed on the remote")
	assert.Equal(t, first, remoteRecord(t, bare, "core@0.1.0"),
		"and the record of the earlier release stayed where it was")
}

// TestReleaseRecordsRespectVerifyOffAndNoPush: the two runs that have nothing
// to compare. commit.verify=false is the setting for a remote that rejects
// ls-remote and accepts pushes, and the comparison is another ls-remote; a
// run that pushes nothing records in its own repository, which is the input
// it plans from. Both behave exactly as they did before the comparison
// existed.
func TestReleaseRecordsRespectVerifyOffAndNoPush(t *testing.T) {
	t.Run("commit.verify off keeps its exemption", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "registry.log")
		cfg := recordsConfig(registry)
		cfg.Commit.Verify = models.Bool(false)
		first, bare := newRecordsOrigin(t, cfg)
		stale := harness.Clone(t, bare)
		require.Equal(t, 0, first.Release().Code)
		recorded := remoteRecord(t, bare, "core@0.1.0")

		res := stale.Release()
		assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.False(t, harness.IsCodePresent(res.Events, "E196"), "no records were read at all")
		assert.Contains(t, res.Stdout, "release records are not compared",
			"and the run says so rather than reading as though they had been")
		assert.Contains(t, res.Stdout, `"level":"warn"`)
		assert.Equal(t, []string{"core@0.1.0", "core@0.1.0"}, publishedVersions(t, registry),
			"which is the behaviour the setting always had")
		assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"),
			"and the record is still the first run's, because the push creates and never replaces")
	})

	t.Run("a run that pushes nothing compares nothing", func(t *testing.T) {
		registry := filepath.Join(t.TempDir(), "registry.log")
		cfg := recordsConfig(registry)
		cfg.Commit.Push = false
		first, bare := newRecordsOrigin(t, cfg)
		stale := harness.Clone(t, bare)
		require.Equal(t, 0, first.Release().Code)
		first.Git("push", "-q", "origin", "core@0.1.0")
		recorded := remoteRecord(t, bare, "core@0.1.0")

		res := stale.Release()
		assert.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, []string{"core@0.1.0", "core@0.1.0"}, publishedVersions(t, registry))
		assert.True(t, stale.IsTagged("core@0.1.0"), "it recorded in its own repository")
		assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"), "and pushed nothing anywhere")
	})
}

// TestReleaseRecordsLeaveAnUndiscoverableWorkspaceToThePlanner: the
// comparison needs the workspace to know which names are records at all, and
// it is the first thing on a pushing release to ask for it. A configuration
// the walk refuses is still the planner's failure to report, in the planner's
// own words: a run whose workspace cannot be read has no records to compare
// and no plan either.
func TestReleaseRecordsLeaveAnUndiscoverableWorkspaceToThePlanner(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, _ := newRecordsOrigin(t, recordsConfig(registry))
	r.WriteFile("packages/core/dispat.json", `{
  "changelog": {
    "sections": [{"title": "Added", "types": ["add"], "bump": "minor"}]
  }
}`)
	r.Commit("feat(core): a folder config the walk refuses")

	res := r.CommandEnv(harness.LockEnabled)
	require.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "package discovery failed",
		"the failure keeps the name the run has always given it")
	assert.Contains(t, res.Stdout, "bump cannot be set in a folder's own config file")
	assert.False(t, harness.IsCodePresent(res.Events, "E196"),
		"a workspace nobody could read is not a records problem")
	assert.Empty(t, publishedVersions(t, registry))
}

// TestReleaseRecordsUnreadableCheckoutRefusesTheRun: the comparison reads two
// inventories, the store's and this checkout's, and asks the checkout's own
// history about the records it lacks. A repository that cannot answer either
// question refuses the run rather than guessing, which is what the store read
// already does.
func TestReleaseRecordsUnreadableCheckoutRefusesTheRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		wanted  string
	}{
		{name: "its own release tags", pattern: "*for-each-ref*",
			wanted: "reading this checkout's release tags"},
		{name: "the history the records are held against", pattern: "*rev-list --parents HEAD*",
			wanted: "is in this checkout's history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := filepath.Join(t.TempDir(), "registry.log")
			r, bare := newRecordsOrigin(t, recordsConfig(registry))
			// The record is on the store and the branch is not, so nothing
			// before the comparison asks this checkout about its history.
			r.Git("tag", "-a", "core@0.1.0", "-m", "an earlier release")
			r.Git("push", "-q", "origin", "core@0.1.0")
			recorded := remoteRecord(t, bare, "core@0.1.0")
			r.Git("tag", "-d", "core@0.1.0")
			bareGit(t, bare, "update-ref", "-d", "refs/heads/"+harness.DefaultBranch)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			res := r.CommandEnv(fault.Env())
			assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
			assert.Contains(t, res.Stdout, tc.wanted)
			assert.Empty(t, publishedVersions(t, registry), "nothing ran")
			assert.Equal(t, recorded, remoteRecord(t, bare, "core@0.1.0"), "and nothing moved")
		})
	}
}

// TestReleaseRecordsUnreadableStoreRefusesTheRun: the records are read with
// one ls-remote per repository, and a run whose store cannot be read has no
// plan anything checked. It is refused the way a failed remote verification
// is refused, before any script runs.
func TestReleaseRecordsUnreadableStoreRefusesTheRun(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, _ := newRecordsOrigin(t, recordsConfig(registry))
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*ls-remote --tags --*"})

	res := r.CommandEnv(fault.Env())
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "cannot read the release records")
	assert.Equal(t, 1, fault.Matches(), "one read per repository")
	assert.Empty(t, publishedVersions(t, registry), "and nothing ran")
}

// TestReleaseRecordsUnwritableStoreKeepsThePublication: the create-only push
// is the last thing a release does, and a store that refuses it leaves a
// published package whose record is missing. That is a critical, not a failed
// package: the artefact is out, and the run says so while still exiting
// non-zero.
func TestReleaseRecordsUnwritableStoreKeepsThePublication(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, bare := newRecordsOrigin(t, recordsConfig(registry))
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*atomic*"})

	res := r.CommandEnv(fault.Env())
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 1, fault.Matches(), "one push carries every record of the run")
	assert.Contains(t, res.Stdout, "push failed")
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry), "the package published")
	assert.True(t, r.IsTagged("core@0.1.0"), "and recorded in its own repository")
	assert.Empty(t, remoteRecord(t, bare, "core@0.1.0"), "the store holds nothing")
}

// TestReleaseRecordsLostTagPushIsFinishedFromItsReceipt refuses an atomic
// branch, tag and receipt-deletion push. A fresh clone still sees the receipt
// and records the publication at its original target without uploading twice.
func TestReleaseRecordsLostTagPushIsFinishedFromItsReceipt(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r, bare := newRecordsOrigin(t, cfg)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push --atomic --porcelain*"})

	failed := r.CommandEnv(fault.Env())
	assert.NotEqual(t, 0, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Equal(t, 1, fault.Matches(), "the atomic branch, tag and receipt update failed")
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
	assert.Contains(t, failed.Stdout, `"status":"published"`, "the package is reported published")
	assert.NotEqual(t, "chore(release): core@0.1.0",
		strings.TrimSpace(bareGit(t, bare, "log", "-1", "--format=%s", harness.DefaultBranch)),
		"the atomic push left branch and tag unchanged together")
	assert.Empty(t, remoteRecord(t, bare, "core@0.1.0"), "and its tag did not")

	retry := harness.Clone(t, bare)
	status := retry.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "core").Str("version"),
		"the fresh checkout plans the same version again: %s", status.Stdout)
	released := retry.ReleaseOK()
	assert.True(t, harness.IsCodePresentForPackage(released.Events, "W246", "core"), "stdout:\n%s", released.Stdout)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry),
		"the receipt keeps the plain re-run from publishing it a second time")
	assert.Equal(t, strings.TrimSpace(bareGit(t, bare, "rev-parse", harness.DefaultBranch)),
		remoteRecord(t, bare, "core@0.1.0"), "the record names the commit the release was planned on")
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/")), "and the receipt is gone")
}

func TestAtomicReleasePushLostResponseIsVerified(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, bare := newRecordsOrigin(t, recordsConfig(registry))
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push --atomic --porcelain*", After: true, Code: 1})
	result := r.CommandEnv(fault.Env())
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.Equal(t, 1, fault.Matches(), "the answer was lost after the atomic remote update")
	assert.NotEmpty(t, remoteRecord(t, bare, "core@0.1.0"))
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/receipts/")))
	retry := harness.Clone(t, bare).Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestPreparedFinalizationRetriesSameCheckout(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r, bare := newRecordsOrigin(t, cfg)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push --atomic --porcelain*"})
	first := r.CommandEnv(fault.Env())
	require.NotZero(t, first.Code)
	assert.Equal(t, 1, fault.Matches())
	assert.Empty(t, remoteRecord(t, bare, "core@0.1.0"))
	assert.NotEmpty(t, r.Git("for-each-ref", "refs/dispat/prepared/"))
	retry := r.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, r.Git("rev-parse", "HEAD"), remoteRecord(t, bare, "core@0.1.0"))
	assert.Empty(t, r.Git("for-each-ref", "refs/dispat/prepared/"))
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/receipts/")))
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestPreparedFinalizationMergesRemoteBeforeRetry(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r, bare := newRecordsOrigin(t, cfg)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*push --atomic --porcelain*"})
	require.NotZero(t, r.CommandEnv(fault.Env()).Code)
	other := harness.Clone(t, bare)
	other.CommitEmpty("chore: landed during failed finalization")
	other.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	retry := r.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.NotEmpty(t, remoteRecord(t, bare, "core@0.1.0"))
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/receipts/")))
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestCrashBeforePreparedMarkerUsesOriginalTarget(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r, bare := newRecordsOrigin(t, cfg)
	planned := r.Git("rev-parse", "HEAD")
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*update-ref refs/dispat/prepared/*"})
	first := r.CommandEnv(fault.Env())
	require.NotZero(t, first.Code)
	assert.Equal(t, 1, fault.Matches())
	assert.NotEqual(t, planned, r.Git("rev-parse", "HEAD"), "the release commit exists locally")
	assert.False(t, r.IsTagged("core@0.1.0"), "no tag is written before the prepared marker")
	retry := r.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, planned, remoteRecord(t, bare, "core@0.1.0"),
		"without a prepared marker, the original publication target is the only safe record")
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestCrashAfterPreparedMarkerBeforeTagRetriesSameCheckout(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	r, bare := newRecordsOrigin(t, cfg)
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*tag -a core@0.1.0*"})
	first := r.CommandEnv(fault.Env())
	require.NotZero(t, first.Code)
	assert.Equal(t, 1, fault.Matches())
	assert.False(t, r.IsTagged("core@0.1.0"))
	assert.NotEmpty(t, r.Git("for-each-ref", "refs/dispat/prepared/"))
	retry := r.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, r.Git("rev-parse", "HEAD"), remoteRecord(t, bare, "core@0.1.0"))
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestBeforePushCannotReplacePreparedReleaseTag(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(true)}
	cfg.Scripts["move-release-tag"] = models.Script{`git tag -f -a core@0.1.0 -m wrong HEAD~1`}
	cfg.Run = &models.RunConfig{BeforePush: []string{"move-release-tag"}}
	r, bare := newRecordsOrigin(t, cfg)
	planned := r.Git("rev-parse", "HEAD")
	result := r.Release()
	require.NotZero(t, result.Code)
	assert.Contains(t, result.Stdout, "prepared target")
	assert.Equal(t, planned, strings.TrimSpace(bareGit(t, bare, "rev-parse", harness.DefaultBranch)))
	assert.Empty(t, remoteRecord(t, bare, "core@0.1.0"))
	assert.NotEmpty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/receipts/")))
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry))
}

func TestAliasRefusalDoesNotRejectRequiredAtomicRecords(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	cfg := recordsConfig(registry)
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(),
		AliasTags: []models.AliasTagConfig{{Format: "v{version}"}}}
	r, bare := newRecordsOrigin(t, cfg)
	hookPath := filepath.Join(bare, "hooks", "update")
	require.NoError(t, os.WriteFile(hookPath,
		[]byte("#!/bin/sh\ncase \"$1\" in refs/tags/v*) exit 1 ;; esac\n"), 0o755))
	result := r.Release()
	require.Zero(t, result.Code, "stdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
	assert.NotEmpty(t, remoteRecord(t, bare, "core@0.1.0"))
	assert.Empty(t, remoteRecord(t, bare, "v0.1.0"))
	assert.Empty(t, strings.TrimSpace(bareGit(t, bare, "for-each-ref", "refs/dispat/receipts/")))
}

// TestReleaseRecordsTagRefusedByTheRemoteIsAFailedPush: a server rule that
// declines the release tag, a pre-receive hook here as a tag ruleset would be
// on a hosted remote, leaves no record on the remote. That is a push that
// failed, reported as E224 with the reason the remote gave, and never E221,
// which would send the operator looking for a release another run recorded
// at another commit when no record is in the way at all.
func TestReleaseRecordsTagRefusedByTheRemoteIsAFailedPush(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "registry.log")
	r, bare := newRecordsOrigin(t, recordsConfig(registry))
	hook := "#!/bin/sh\nwhile read old new ref; do\n  case \"$ref\" in\n  refs/tags/core@*)\n" +
		"    echo \"release tags are created by the release team\" >&2\n    exit 1 ;;\n  esac\ndone\n"
	require.NoError(t, os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755))

	res := r.Release()
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, []string{"core@0.1.0"}, publishedVersions(t, registry), "the package published")
	assert.Contains(t, res.Stdout, `"status":"published"`, "and is reported published")
	assert.True(t, harness.IsCodePresent(res.Events, "E224"), "the push failed: %s", res.Stdout)
	assert.False(t, harness.IsCodePresent(res.Events, "E221"), "no record is in the way: %s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "release tags are created by the release team",
		"the remote's reason is named")
	assert.Empty(t, remoteRecord(t, bare, "core@0.1.0"), "and the remote holds no record")
}

// TestReleaseRecordsInAComposedWorkspace: a fleet plans from several stores at
// once, so a stale checkout of any one of them plans a version that
// repository has already published. The comparison covers every participating
// repository, names the one that disagrees, and refuses before anything
// publishes anywhere.
func TestReleaseRecordsInAComposedWorkspace(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "published.log")
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	bare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", bare)
	control.Git("-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": false}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{`printf '%s\n' "$DISPAT_PACKAGE" >> ` + shellQuote(marker)},
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{
			"commit": map[string]any{
				"enabled": true, "push": true, "remote": "origin",
				"branch": harness.DefaultBranch,
			},
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a source repository that records to its own remote")

	// The source's remote carries a record its checkout does not: the release
	// another run made, on the commit this one plans from.
	control.Git("-C", "sources/lib", "tag", "-a", "lib@0.1.0", "-m", "an earlier release")
	control.Git("-C", "sources/lib", "push", "-q", "origin", "lib@0.1.0")
	recorded := remoteRecord(t, bare, "lib@0.1.0")
	control.Git("-C", "sources/lib", "tag", "-d", "lib@0.1.0")

	res := control.Release()
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "E196"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "repository lib-source:", "the repository to run the remedy in")
	assert.Contains(t, res.Stdout, "lib@0.1.0")
	assert.NoFileExists(t, marker, "no package published anywhere in the fleet")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "and the source recorded nothing")
	assert.Equal(t, recorded, remoteRecord(t, bare, "lib@0.1.0"), "the store keeps what it had")
}
