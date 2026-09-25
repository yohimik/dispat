// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the long tail of `Edits`, `Deletes` and `Reverts`.
//
// A correction names a record written in a commit message that can no longer
// be rewritten, so every shape a person may write it in has to resolve to
// something definite: a wildcard, an abbreviated sha, a unit selector inside a
// commit carrying several records, or a sha that names nothing this history
// can reach. The scenarios below take each of those shapes through the binary
// and assert on what the run said about them, since a correction that quietly
// did nothing is indistinguishable from one that worked.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailReleasedRepo is a two-package repository with one release already
// behind it, which is what every correction scenario needs: a pending window
// that starts after a real baseline rather than at the beginning of history.
func covTailReleasedRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")
	return r
}

// countCode returns how many events carry the given diagnostic code — the
// form a scenario needs when the claim is about how many times something was
// reported rather than whether it was.
func countCode(events []harness.Event, code string) int {
	n := 0
	for _, e := range events {
		if e.Code() == code {
			n++
		}
	}
	return n
}

// TestCovTailWildcardDeleteSkipsWhatIsAlreadyClaimed: `Deletes: *` discards
// every pending record its commit descends from, with two deliberate
// exceptions. A record a newer, narrower correction already claimed stays with
// that correction, because the newest correction of a record wins and a
// wildcard behind it is not an older claim on the same thing. And control
// units are left alone throughout: they carry no record to discard, and taking
// them would erase the very directives the later phases run on, which is why
// the channel directive here still moves its package.
func TestCovTailWildcardDeleteSkipsWhatIsAlreadyClaimed(t *testing.T) {
	r := covTailReleasedRepo(t)

	r.WriteFile("packages/core/feature.txt", "work\n")
	r.Commit("feat(core): work that would have released")
	named := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("release(core)%beta: a control unit inside the same window")
	r.WriteFile("packages/utils/fix.txt", "work\n")
	r.Commit("fix(utils): work that would have released too")

	r.CommitEmpty("chore: discard every pending record\n\nDeletes: *")
	r.CommitEmpty("fix(core): discard one record by name\n\nDeletes: " + named)

	res := r.StatusOK()
	assert.False(t, harness.IsCodePresent(res.Events, "W215"),
		"neither correction voided the other: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "utils").Str("message"),
		"the record only the wildcard reached is discarded too: %s", res.Stdout)
	core := harness.GraphLine(res.Events, "core")
	assert.Equal(t, "patch", core.Str("bump"),
		"the discarded feature leaves only the correcting commit's own record: %s", res.Stdout)
	assert.Equal(t, "stable -> beta", core.Str("channel"),
		"while the control unit neither correction could claim still applies: %s", res.Stdout)

	traced := r.StatusOK("--log-level", "trace")
	assert.Contains(t, traced.Stdout, "record discarded",
		"and each claim is traceable one by one: %s", traced.Stdout)
}

// TestCovTailUnitSelectorNamesOneRecordInsideACommit: a commit carrying
// several records is corrected one record at a time, which is what the "#n"
// selector is for. The valid selector reaches the record it names and nothing
// beside it, which is visible here as the bump: restating the breaking third
// record as a fix leaves the two patch records around it untouched. A selector
// past the end of the commit is an error rather than a silent no-op, because
// the author plainly meant a record and there is none.
func TestCovTailUnitSelectorNamesOneRecordInsideACommit(t *testing.T) {
	t.Run("a selector naming the last record", func(t *testing.T) {
		r := covTailReleasedRepo(t)
		r.WriteFile("packages/core/three.txt", "work\n")
		r.Commit("fix(core): the first record\n\n---\n\n" +
			"perf(core): the second record\n\n---\n\n" +
			"feat(core)!: the third record")
		target := r.Git("rev-parse", "HEAD")

		r.CommitEmpty("fix(core): restate the third record\n\nEdits: " + target + "#3")

		res := r.StatusOK()
		line := harness.GraphLine(res.Events, "core")
		assert.Equal(t, "0.1.0 -> 0.1.1", line.Str("version"),
			"only the record the selector named was replaced: %s", res.Stdout)
		assert.Equal(t, float64(1), line["corrected"],
			"and the release records that it stands in for one target: %s", res.Stdout)
	})

	t.Run("a selector past the end of the commit", func(t *testing.T) {
		r := covTailReleasedRepo(t)
		r.WriteFile("packages/core/two.txt", "work\n")
		r.Commit("feat(core): the first record\n\n---\n\nfix(core): the second record")
		target := r.Git("rev-parse", "HEAD")

		r.CommitEmpty("fix(core): restate a record that is not there\n\nEdits: " + target + "#5")

		res := r.Status()
		assert.True(t, harness.IsCodePresent(res.Events, "E211"),
			"the selector is out of range: %s", res.Stdout)
		assert.Contains(t, res.Stdout, "carries 2 unit(s)",
			"and the run says how many records the commit does carry")
	})
}

// TestCovTailRestatementThatChangesNothingIsReported: an `Edits` that restates
// its target as the same type, marker and description leaves the record
// exactly as it was. That is almost always a mistake — the author edited the
// footer and forgot the subject line — so it is reported even though applying
// it is harmless.
func TestCovTailRestatementThatChangesNothingIsReported(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/streaming.txt", "work\n")
	r.Commit("feat(core): add streaming")
	target := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("feat(core): add streaming\n\nEdits: " + target)

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W211"),
		"the restatement says the same thing as its target: %s", res.Stdout)
}

// TestCovTailRevertsFooterDegradedForms: `Reverts` is informational, so a
// target it cannot take out of the changelog resolves to "leave the changelog
// alone" and never to an error. A target on the other side of a release is
// published history and cannot be taken out of it; a target whose records
// belong to another package leaves this one's changelog untouched.
func TestCovTailRevertsFooterDegradedForms(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/utils/theirs.txt", "work\n")
	r.Commit("feat(utils): work that belongs to the other package")
	theirs := r.Git("rev-parse", "HEAD")
	released := r.Git("rev-list", "--max-parents=0", "HEAD")

	r.WriteFile("packages/core/mine.txt", "work\n")
	r.Commit("feat(core): work of this package")
	r.CommitEmpty("fix(core): a revert of something already published\n\nReverts: " + released)
	r.CommitEmpty("fix(core): a revert of another package's record\n\nReverts: " + theirs)

	res := r.StatusOK()
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W212", "core"),
		"neither suppressed an entry of this package: %s", res.Stdout)
	assert.Equal(t, "0.1.0 -> 0.2.0", harness.GraphLine(res.Events, "core").Str("version"),
		"and both still count toward the bump: %s", res.Stdout)
}

// TestCovTailTwoCorrectionsInOneCommit: a commit may carry several records,
// and a correction is a record like any other, so one commit can correct two
// different things at once. Within a commit the later unit is the newer one,
// which is the order every other precedence rule uses and the one this has to
// agree with.
func TestCovTailTwoCorrectionsInOneCommit(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/mistake.txt", "work\n")
	r.Commit("feat(core)!: a breaking change nobody wanted")
	breaking := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/utils/mistake.txt", "work\n")
	r.Commit("feat(utils): work that should never have been recorded")
	unwanted := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): restate it as a fix\n\nEdits: " + breaking + "\n\n---\n\n" +
		"chore(utils): discard the other record entirely\n\nDeletes: " + unwanted)

	res := r.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the restatement decides this package's bump: %s", res.Stdout)
	assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "utils").Str("message"),
		"and the discarded record releases nothing at all: %s", res.Stdout)
}

// TestCovTailWildcardEditStandsInForEverythingItClaimed: `Edits: *` is a
// restatement rather than a discard, so the carrying unit stands in for every
// record the wildcard reached — one entry in place of all of them, under the
// type the correction itself was written as.
func TestCovTailWildcardEditStandsInForEverythingItClaimed(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/first.txt", "work\n")
	r.Commit("feat(core)!: the first thing that went in")
	r.WriteFile("packages/core/second.txt", "work\n")
	r.Commit("feat(core): the second thing that went in")

	r.CommitEmpty("fix(core): one restatement standing in for both\n\nEdits: *")

	res := r.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the restatement replaces every record it claimed: %s", res.Stdout)
}

// TestCovTailCorrectionOfACommitThatCarriesNoRecord: a commit whose message is
// not a release record at all is still in the window, and naming it as a
// target is not an error — there is simply nothing there to correct, which is
// the same no-op a correction of released work is.
func TestCovTailCorrectionOfACommitThatCarriesNoRecord(t *testing.T) {
	r := covTailReleasedRepo(t)
	r.WriteFile("packages/core/plain.txt", "work\n")
	r.Commit("this message is not a record at all")
	plain := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): restate what was never a record\n\nEdits: " + plain)

	res := r.Status()
	assert.True(t, harness.IsCodePresent(res.Events, "W209"),
		"the target carries no record to correct: %s", res.Stdout)
}
