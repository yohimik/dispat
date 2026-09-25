// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: reading a repository that is not a straight line.
//
// Ancestry is what makes cancellation and correction deterministic under
// merges, rebases and equal timestamps, so the commit graph a run loads has to
// survive a history where two paths lead back to the same commit. And a git
// invocation that fails has to say what failed without saying what the
// credential in the remote URL was, since its text reaches a log, a CI
// ingestion and a hook script's DISPAT_ERROR alike.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailAncestryAcrossAMergeVisitsEachCommitOnce: a merge gives the graph
// two paths to the same commit, which is the shape that turns a naive walk
// into a repeated one — and, on a long history, into a quadratic one. The
// correction below asks the ancestry question about a commit both paths reach,
// and the answer has to be the same one a linear history would have given.
func TestCovTailAncestryAcrossAMergeVisitsEachCommitOnce(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/shared.txt", "work\n")
	r.Commit("feat(core)!: the record both paths lead back to")
	shared := r.Git("rev-parse", "HEAD")

	r.Git("checkout", "-q", "-b", "sidebranch")
	r.WriteFile("packages/utils/side.txt", "work\n")
	r.Commit("fix(utils): work done on the branch")
	r.Git("checkout", "-q", harness.DefaultBranch)
	r.WriteFile("packages/core/trunk.txt", "work\n")
	r.Commit("fix(core): work done on the trunk")
	r.Git("merge", "-q", "--no-ff", "-m", "chore: merge the branch", "sidebranch")

	r.CommitEmpty("fix(core): restate the record both paths reach\n\nEdits: " + shared)

	res := r.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the restatement reached its target across the merge: %s", res.Stdout)
	assert.False(t, harness.IsCodePresent(res.Events, "E210"),
		"a commit reachable by two paths is still an ancestor: %s", res.Stdout)
}

// TestCovTailTagInventoryWalksRefsShorterThanAPrefix: the tag listing is
// dispatched to package matchers through a trie over their literal prefixes,
// and a ref shorter than the prefix it shares characters with runs the walk
// off the end of the name rather than off the end of the trie. Such a ref
// belongs to no package and must reach none, while the real release tag beside
// it is still the package's baseline.
func TestCovTailTagInventoryWalksRefsShorterThanAPrefix(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	tagAt(r, "core@1.0.0", "HEAD")
	// Shorter than "core@", and sharing its first characters.
	r.Git("tag", "cor")

	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("fix(core): work on top of the baseline")

	res := r.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the release tag is the baseline and the short ref is nobody's: %s", res.Stdout)
}
