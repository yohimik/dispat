// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Two more of the git layer's shapes, each driven by the flow that reaches it:
// the ref namespaces a composed run's guard has to watch when a space writes
// alias tags, and the conflict git cannot answer with a file at all, because
// one side deleted what the other edited.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPolyrepoRefGuardWatchesAliasNamespaces: the guard reads every ref a
// configured package may write before planning, so a ref created during the
// run is a detectable change rather than an input half the plan accepted. A
// space that writes alias tags widens that namespace beyond the release tag,
// and an alias left out of it would be a ref nobody was watching.
func TestPolyrepoRefGuardWatchesAliasNamespaces(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = map[string]any{
		"libs": map[string]any{
			"path":      []string{"sources/lib/packages"},
			"aliasTags": []any{map[string]any{"format": "release-v{version}"}},
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a space that writes alias tags")

	control.ReleaseOK()
	tags := polyrepoTags(control, "sources/lib")
	assert.Contains(t, tags, "lib@0.1.0")
	assert.Contains(t, tags, "release-v0.1.0", "the alias is written beside the release tag")

	// A second release reads the first one's refs back through the same
	// namespace, which is the read the alias prefix exists for.
	control.WriteFile("sources/lib/packages/lib/api.txt", "more\n")
	commitPolyrepoSource(t, control, "sources/lib", "feat(lib): add to the API")
	checkpointPolyrepoSource(t, control, "sources/lib")

	control.ReleaseOK()
	tags = polyrepoTags(control, "sources/lib")
	assert.Contains(t, tags, "lib@0.2.0")
	assert.Contains(t, tags, "release-v0.2.0")
	assert.Contains(t, tags, "release-v0.1.0", "and the earlier alias is left where it was")
}

// TestReleaseSettlesAConflictOverAFileItDeleted: the conflict with no file to
// take. This side removed the file the release no longer ships; the commits
// that landed mid-release edited it. There is nothing to check out as this
// side's version, so this side is the absence, and the path is removed from
// the merge instead. Their edit survives on the branch the run sets aside, and
// both halves are named in the record.
func TestReleaseSettlesAConflictOverAFileItDeleted(t *testing.T) {
	r := harness.New(t)
	bare := r.AddBareRemote()
	// The build lands their edit on the remote and then deletes the file this
	// release is dropping, so the release commit records a deletion of the
	// path they just changed.
	script := midReleasePush(t, bare, "docs(core): edit the file this release drops",
		"packages/core/doomed.txt", "their edit\n") + "; rm -f doomed.txt"
	cfg := libsConfig(script, 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/doomed.txt", "the original\n")
	r.Commit("feat(core): first")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	res := r.Release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.True(t, harness.IsCodePresent(res.Events, "W243"), "the conflict is reported: %v", res.Events)

	assert.NoFileExists(t, r.Path("packages", "core", "doomed.txt"),
		"this side of the conflict is the file's absence")
	assert.NoFileExists(t, r.Path(".git", "MERGE_HEAD"), "and the merge was finished, not left open")
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Contains(t, r.Git("ls-remote", "origin"), "refs/tags/core@0.1.0")

	quarantine := conflictBranchOf(t, r)
	assert.Contains(t, r.Git("log", "--format=%s", "origin/"+quarantine),
		"edit the file this release drops", "their edit is kept where it can be read")
	assert.Contains(t, r.Git("show", "origin/"+quarantine+":packages/core/doomed.txt"), "their edit")

	entry := changelogOf(t, r, "core")
	assert.Contains(t, entry, "packages/core/doomed.txt", "the record names the file")
	assert.Contains(t, entry, quarantine, "and the branch their side is on")
	assert.False(t, strings.Contains(entry, "<<<<"), "no conflict markers were committed")
}

// TestPolyrepoCheckpointVerifiesTheSourceBranchItPinsTo: a control checkpoint
// is a gitlink, and a gitlink to a commit the source's own remote does not
// carry is a pointer into nothing the moment anybody else clones the fleet. A
// checkpoint made without a release tag has only the source branch to prove
// the revision is durable, so that is what it reads before the control push
// makes the pointer permanent.
func TestPolyrepoCheckpointVerifiesTheSourceBranchItPinsTo(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)

	// Each repository coordinates through a remote of its own, as a fleet's
	// do: the source's branch is what the checkpoint verifies, and the
	// control's remote is where the checkpoint itself lands.
	sourceBare := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", sourceBare)
	control.Git("-C", sourceBare, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", sourceBare)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{
		"enabled": true, "push": true, "remote": "origin",
		"messageFormat": "chore(control): checkpoint {tags}",
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{
			"commit": map[string]any{
				"enabled": true, "push": true, "remote": "origin",
				"branch":        harness.DefaultBranch,
				"messageFormat": "chore(source): record {tags}",
			},
		},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a pushing fleet")
	controlBare := control.AddBareRemote()
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)

	// Something for the source to record: without a change inside the package
	// folder there is no source commit, and a checkpoint of a pointer that did
	// not move is a checkpoint nobody needs.
	control.WriteFile("sources/lib/packages/lib/generated.txt", "built by the release\n")

	// No --tag: the checkpoint then has no immutable source tag to point at,
	// and the branch is the only evidence the revision is reachable.
	res := control.Command("commit", "--push")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	sourceHead := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.Equal(t, sourceHead,
		strings.TrimSpace(bareGit(t, sourceBare, "rev-parse", "refs/heads/"+harness.DefaultBranch)),
		"the source branch carries the revision the checkpoint pins to")
	assert.Equal(t, sourceHead, control.Git("rev-parse", "HEAD:sources/lib"),
		"and the control checkpoint names it")
	assert.Contains(t, control.Git("log", "-1", "--format=%s"), "chore(control): checkpoint")
	assert.Equal(t, control.Git("rev-parse", "HEAD"),
		strings.TrimSpace(bareGit(t, controlBare, "rev-parse", "refs/heads/"+harness.DefaultBranch)),
		"the checkpoint reached the control remote")
	assert.Empty(t, polyrepoTags(control, "sources/lib"), "a commit without --tag writes no tag")
}
