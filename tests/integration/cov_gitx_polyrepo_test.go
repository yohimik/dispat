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
