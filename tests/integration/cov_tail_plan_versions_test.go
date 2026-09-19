// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the directives that decide a version, and what happens
// when two of them are in force at once.
//
// `Release-As` is the one footer that replaces a computed result outright, so
// every guard around it exists to stop a single line of a commit message from
// doing something the rest of the history contradicts: naming a version for
// several packages at once, standing while a newer directive says otherwise,
// or surviving a cancel that discarded the commit carrying it.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailExactPinNamingSeveralPackages: an exact version names one
// package, because two packages cannot both be 1.2.3 and still be versioned
// independently. Two written includes are refused by the parser, which can see
// them; a glob's breadth is invisible in the text and needs the workspace,
// which is what this case is for. The rejected pin has a unit's blast radius:
// the packages still release, at the version the window computes.
func TestCovTailExactPinNamingSeveralPackages(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "coreutils")
	r.Commit("feat(core,coreutils): bootstrap both packages")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/work.txt", "work\n")
	r.WriteFile("packages/coreutils/work.txt", "work\n")
	r.Commit("feat(core*): work in both, pinned as if it were one\n\nRelease-As: 1.2.3")

	res := r.Status()
	assert.True(t, harness.IsCodePresent(res.Events, "E154"),
		"the pin addresses more than one package: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "applies to 2 packages",
		"and the run says how many it reached")
	assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(res.Events, "core").Str("version"),
		"while the work itself still releases at its computed version: %s", res.Stdout)
}

// TestCovTailTwoReleaseAsDirectivesInOneWindow: a hold written last week and a
// resume written today are both in force until one of them is released. The
// newest wins, and the fact that there were two is reported, because a
// directive that was silently outranked is the reason a release an operator
// expected to be held went out.
func TestCovTailTwoReleaseAsDirectivesInOneWindow(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core): work worth releasing")
	r.CommitEmpty("release(core): hold it back for now\n\nRelease-As: none")
	r.CommitEmpty("release(core): let it go after all\n\nRelease-As: auto")

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W153", "core"),
		"two directives were pending: %s", res.Stdout)
	assert.False(t, harness.IsCodePresent(res.Events, "W158"),
		"the resume did lift a hold, so it is not the redundant kind: %s", res.Stdout)
	assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(res.Events, "core").Str("version"),
		"and the newest directive is the one in force: %s", res.Stdout)
}

// TestCovTailCancelClearsAHoldByDiscardingIt: a cancel is a barrier over the
// commits behind it, and a hold is a record like any other, so a cancel takes
// the hold with everything else it discards. Work written after the barrier
// then releases with nothing holding it, which is what makes a cancel the way
// to start over rather than a way to accumulate contradicting directives.
func TestCovTailCancelClearsAHoldByDiscardingIt(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/abandoned.txt", "work\n")
	r.Commit("feat(core): work that was going to be held")
	r.CommitEmpty("release(core): hold it back\n\nRelease-As: none")
	r.CommitEmpty("cancel(core): start over from here")
	r.WriteFile("packages/core/fresh.txt", "work\n")
	r.Commit("fix(core): work written after the barrier")

	res := r.StatusOK()
	line := harness.GraphLine(res.Events, "core")
	assert.Equal(t, "0.1.0 -> 0.1.1", line.Str("version"),
		"only the work after the barrier counts: %s", res.Stdout)
	assert.NotContains(t, line.Str("message"), "held",
		"and the discarded hold holds nothing: %s", res.Stdout)
}

// TestCovTailRevertLeavesTheRestOfTheChangelogAlone: a revert takes its target
// and itself out of the entry, and nothing else. The release still documents
// everything the window carries besides those two, which is the difference
// between suppressing a pair of entries and suppressing a release's notes.
func TestCovTailRevertLeavesTheRestOfTheChangelogAlone(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/regret.txt", "work\n")
	r.Commit("feat(core): a feature that turned out wrong")
	regretted := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/undo.txt", "work\n")
	r.Commit("fix(core): undo the feature\n\nReverts: " + regretted)
	r.WriteFile("packages/core/keep.txt", "work\n")
	r.Commit("feat(core): unrelated work that belongs in the entry")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")
	assert.Contains(t, entry, "unrelated work that belongs in the entry",
		"the surviving record is documented: %s", entry)
	assert.NotContains(t, entry, "a feature that turned out wrong",
		"the reverted record is not: %s", entry)
	assert.NotContains(t, entry, "undo the feature",
		"nor is the revert that removed it: %s", entry)
}
