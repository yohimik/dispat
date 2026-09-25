// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Goal 31: corrections and reverted changelogs. A release record is written in
// a commit message, and a commit message cannot be rewritten once it is
// pushed. The `Edits` and `Deletes` footers correct such a record by naming it:
// `Edits` restates it, `Deletes` discards it, and both reach only work no
// package has released yet.
//
// The claims exercised here are the ones a release depends on: the corrected
// record decides the version and the changelog, a correction of released work
// is a visible no-op, the newest correction of a target wins, a correction can
// narrow a record but never widen it, and a correction of a correction undoes
// it. The last group covers `Reverts`, which takes a reverted entry and its
// revert out of the changelog while both still count toward the bump.
package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// correctionsRepo is a two-package repository with no dependency edge, which
// is what most of these scenarios want: one package to correct and one to
// prove the correction did not reach further than it claimed.
func correctionsRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	return r
}

// changelogOf returns a package's changelog, or "" when none was written.
func changelogOf(t *testing.T, r *harness.Repo, pkg string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path("packages", pkg, "CHANGELOG.md"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(data)
}

// TestCorrectionEditRestatesTheRecordBeforeRelease: the specification's worked
// example, end to end. A commit classified as a breaking feature by mistake is
// restated as a fix before anything ships, so the package releases a patch and
// the changelog carries the restatement rather than the mistake. A second run
// converges.
func TestCorrectionEditRestatesTheRecordBeforeRelease(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a defensive fix, not a rewrite\n")
	r.Commit("feat(core)!: rewrite internals")
	mistake := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): rewrite internals\n\nThe change is a refactor with a defensive fix.\n\nEdits: " + mistake)
	res := r.ReleaseOK()

	assert.True(t, r.IsTagged("core@0.1.1"), "the major left with the record carrying it; tags: %v", r.TagList())
	assert.False(t, r.IsTagged("core@1.0.0"), "tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "W209"), "the target was pending: %s", res.Stdout)

	log := changelogOf(t, r, "core")
	assert.Contains(t, log, "The change is a refactor", "the restatement is the entry")
	assert.Contains(t, log, "(corrects ", "and it says what it replaces")
	assert.NotContains(t, log, "### Breaking Changes", "the mistake is not documented")

	r.Commit("chore(release): record the changelog")
	r.ReleaseOK()
	assert.Equal(t, 2, r.TagCount("core@"), "a corrected plan converges; tags: %v", r.TagList())
}

// TestCorrectionAfterReleaseIsAVisibleNoop: a correction reaches only
// undischarged work. Once the target has shipped, the record is published
// history: the correction is a no-op, W209 says so where an operator will see
// it, and the carrying unit still releases on its own account.
func TestCorrectionAfterReleaseIsAVisibleNoop(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "shipped\n")
	r.Commit("feat(core)!: rewrite internals")
	shipped := r.Git("rev-parse", "HEAD")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@1.0.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.CommitEmpty("fix(core): too late to restate it\n\nEdits: " + shipped)
	res := r.ReleaseOK()

	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W209", "core"),
		"the operator has to see that the correction did not take: %s", res.Stdout)
	assert.True(t, r.IsTagged("core@1.0.1"), "the carrying unit still releases; tags: %v", r.TagList())

	// W209 is non-suppressible (§17.1), and dispat gets that by owning the
	// code: --quiet-parser hides only the codes the parser itself defines.
	r.Commit("chore(release): record the changelog")
	r.CommitEmpty("fix(core): still too late\n\nEdits: " + shipped)
	quiet := r.ReleaseOK("--quiet-parser")
	assert.True(t, harness.IsCodePresentForPackage(quiet.Events, "W209", "core"),
		"--quiet-parser must not be able to hide it: %s", quiet.Stdout)
}

// TestCorrectionPrecedenceAndVoiding: the two rules that decide a pile of
// corrections. Two corrections of one target resolve newest-first with W210 on
// the loser; a correction of a correction voids it with W215, which is how a
// mistaken correction is undone.
func TestCorrectionPrecedenceAndVoiding(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "the original\n")
	r.Commit("feat(core)!: the original")
	// Abbreviated, the way a person copies a sha out of a log: both
	// corrections resolve it to the same record.
	original := r.Git("rev-parse", "HEAD")[:9]

	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + original)
	r.CommitEmpty("fix(core): restate it instead\n\nEdits: " + original)
	res := r.ReleaseOK()

	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W210", "core"),
		"the superseded delete must be reported: %s", res.Stdout)
	assert.True(t, r.IsTagged("core@0.1.1"), "the restatement decides the bump; tags: %v", r.TagList())
	assert.Contains(t, changelogOf(t, r, "core"), "restate it instead")
	r.Commit("chore(release): record the changelog")

	// Now undo a correction. The delete discards the restatement's own record,
	// which voids the restatement, which means it never discarded the original.
	r.WriteFile("packages/utils/main.txt", "the original\n")
	r.Commit("feat(utils)!: the original")
	target := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("fix(utils): restate it\n\nEdits: " + target)
	restatement := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("chore(utils): that correction was wrong\n\nDeletes: " + restatement)
	res = r.ReleaseOK()

	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W215", "utils"),
		"the voiding must be reported: %s", res.Stdout)
	assert.True(t, r.IsTagged("utils@1.0.0"), "the original record returns; tags: %v", r.TagList())
}

// TestCorrectionScopeIsContainedNotCombined: a correction may narrow a record
// and never widen it. Narrowing corrects a record scoped (*) for one package
// while it stands for the others; widening is E213 and voids the unit.
func TestCorrectionScopeIsContainedNotCombined(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "shared work\n")
	r.WriteFile("packages/utils/main.txt", "shared work\n")
	r.Commit("feat(*)!: one record for both")
	shared := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): smaller than that, for core\n\nEdits: " + shared)
	res := r.ReleaseOK()

	assert.True(t, r.IsTagged("core@0.1.1"), "core carries the narrowed restatement; tags: %v", r.TagList())
	assert.True(t, r.IsTagged("utils@1.0.0"), "utils keeps the original record; tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "E213"), "narrowing is legal: %s", res.Stdout)
	r.Commit("chore(release): record the changelog")

	// The other direction: a correction naming a package its target's record
	// never claimed is refused, and the target stands.
	r.WriteFile("packages/core/main.txt", "core only\n")
	r.Commit("feat(core)!: core only")
	coreOnly := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("fix(*): restate it everywhere\n\nEdits: " + coreOnly)
	res = r.Release()

	assert.True(t, harness.IsCodePresent(res.Events, "E213"),
		"a correction may not extend someone else's record: %s", res.Stdout)
	assert.True(t, r.IsTagged("core@1.0.0"), "the target's record survives the void; tags: %v", r.TagList())
}

// TestCorrectionWildcardClearsAScope: `Deletes: *` discards every pending
// record for the packages it names and nothing beyond them. Paired with a type
// that bumps nothing, it is how a scope's whole ledger is started over without
// a cancel barrier.
func TestCorrectionWildcardClearsAScope(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "invented\n")
	r.Commit("feat(core)!: an invented bump")
	r.WriteFile("packages/core/other.txt", "invented too\n")
	r.Commit("feat(core): another invented bump")
	r.WriteFile("packages/utils/main.txt", "real work\n")
	r.Commit("fix(utils): real work")

	r.CommitEmpty("chore(core): start the ledger over\n\nDeletes: *")
	res := r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "core has nothing left to release; tags: %v", r.TagList())
	assert.True(t, r.IsTagged("utils@0.1.1"), "the wildcard reaches only its own scope; tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W209", "core"),
		"the wildcard did discard something: %s", res.Stdout)
}

// TestCorrectionTargetsMustResolve: the three unit-scoped errors. Each voids
// its own unit and leaves every other record alone, so a mistyped correction
// costs one unit rather than a release.
func TestCorrectionTargetsMustResolve(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	t.Run("a commit that is not an earlier one is E210", func(t *testing.T) {
		r.CommitEmpty("fix(core): correcting the future\n\nEdits: 1234567abcdef")
		res := r.Release()
		assert.True(t, harness.IsCodePresent(res.Events, "E210"), "events:\n%s", res.Stdout)
	})

	t.Run("a bare sha on a multi-unit commit is E211", func(t *testing.T) {
		r.WriteFile("packages/core/multi.txt", "two units\n")
		r.Commit("fix(core): one\n\n---\n\nfix(utils): two")
		multi := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(core): which one?\n\nDeletes: " + multi)
		res := r.Release()
		assert.True(t, harness.IsCodePresent(res.Events, "E211"), "events:\n%s", res.Stdout)
	})

	t.Run("a control unit is E212", func(t *testing.T) {
		r.CommitEmpty("cancel(utils): start over")
		barrier := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(utils): correcting a barrier\n\nEdits: " + barrier)
		res := r.Release()
		assert.True(t, harness.IsCodePresent(res.Events, "E212"), "events:\n%s", res.Stdout)
	})

	t.Run("a sha resolving to nothing is E210 once per correction naming it", func(t *testing.T) {
		// A sha copied out of another clone is asked about once and
		// remembered as unresolvable, and each correction naming it reports it.
		r := releasedCorrectionsRepo(t)
		missing := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		r.CommitEmpty("fix(core): restate something absent\n\nEdits: " + missing)
		r.CommitEmpty("fix(utils): restate the very same absent thing\n\nDeletes: " + missing)

		res := r.Status()
		assert.Equal(t, 2, countCode(res.Events, "E210"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "deadbeef", "and the target is named back")
	})

	t.Run("a commit that is not a proper ancestor is E210", func(t *testing.T) {
		// A sha on a branch that was never merged into this one names a
		// record this branch's plan does not contain, and is refused rather
		// than reached across.
		r := releasedCorrectionsRepo(t)
		r.Git("checkout", "-q", "-b", "sidetrack")
		r.WriteFile("packages/core/branch.txt", "work\n")
		r.Commit("feat(core)!: work that only the branch has")
		elsewhere := r.Git("rev-parse", "HEAD")
		r.Git("checkout", "-q", harness.DefaultBranch)
		r.WriteFile("packages/core/main.txt", "work\n")
		r.Commit("feat(core): work this branch does have")
		r.CommitEmpty("fix(core): restate what another branch carries\n\nEdits: " + elsewhere)

		res := r.Status()
		assert.True(t, harness.IsCodePresent(res.Events, "E210"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "is not a proper ancestor of this commit")
	})
}

// TestCorrectionDiscardsWhatTheRecordPropagated: a deleted record takes its
// propagated contributions with it. Without the correction the caret carries
// the provider's feature into its consumer; with it, the consumer has no
// reason to release at all.
func TestCorrectionDiscardsWhatTheRecordPropagated(t *testing.T) {
	r := linkedRepo(t, "core", "web", echoBuild)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(web): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "propagating\n")
	r.Commit("feat(core)^: a feature its consumer picks up")
	propagated := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + propagated)
	r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "the record went; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("web@"), "and so did what it propagated; tags: %v", r.TagList())
}

// TestCorrectionRidesAVersioningGroupOnlyWhenARecordSurvives: a versioning
// group releases as one, so discarding the record that was the group's only
// cause has to stop the whole group rather than leave the members riding a
// version nothing asked for.
func TestCorrectionRidesAVersioningGroupOnlyWhenARecordSurvives(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Versioning: "fixed"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "the group's only cause\n")
	r.Commit("feat(core): the group's only cause")
	cause := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + cause)
	res := r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("utils@"), "no member rides a version nothing caused; tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "W234"), "no ride to explain: %s", res.Stdout)
}

// TestRevertTakesBothEntriesOutOfTheChangelog: the revert trap and its
// changelog half. The bump keeps the reverted commit's major, because
// consumers may already have seen it; the changelog loses both entries,
// because the release contains neither the change nor its removal. A second
// run converges.
func TestRevertTakesBothEntriesOutOfTheChangelog(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a bad idea\n")
	r.Commit("feat(core)!: a bad idea")
	// Written abbreviated, the way a person copies a sha out of a log, and
	// named twice, which is resolved once.
	bad := r.Git("rev-parse", "HEAD")[:9]
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a bad idea\n\nReverts: " + bad)
	r.CommitEmpty("chore(core): note the undo once more\n\nReverts: " + bad)

	traced := r.StatusOK("--log-level", "trace")
	assert.Contains(t, traced.Stdout, "reverted entries suppressed",
		"the suppression is traceable: %s", traced.Stdout)
	res := r.ReleaseOK()

	assert.True(t, r.IsTagged("core@1.0.0"), "the major is still owed; tags: %v", r.TagList())
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W212", "core"),
		"the plan accounts for the absent entries: %s", res.Stdout)

	log := changelogOf(t, r, "core")
	assert.NotContains(t, log, "a bad idea", "neither entry is documented")
	assert.NotContains(t, log, "### Breaking Changes")

	r.Commit("chore(release): record the changelog")
	r.ReleaseOK()
	assert.Equal(t, 2, r.TagCount("core@"), "the run converges; tags: %v", r.TagList())
}

// TestRevertWithAnUnreachableTargetStaysInformational: a well-formed sha that
// names no reachable commit is W213 and changes nothing about the release; a
// value that is not a sha at all is the parser's W214, and dispat does not
// report the same mistake twice.
func TestRevertWithAnUnreachableTargetStaysInformational(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "undone\n")
	r.Commit("revert(core): something from elsewhere\n\nReverts: 1234567abcdef")
	res := r.ReleaseOK()

	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W213", "core"), "events:\n%s", res.Stdout)
	assert.True(t, r.IsTagged("core@0.1.1"), "the revert releases as usual; tags: %v", r.TagList())
	assert.Contains(t, changelogOf(t, r, "core"), "something from elsewhere",
		"and is documented as usual")
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/utils/main.txt", "undone\n")
	r.Commit("revert(utils): something\n\nReverts: not-a-sha")
	r.CommitEmpty("revert(utils): something shorter than any sha\n\nReverts: abc")
	res = r.ReleaseOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W214"), "the parser's diagnostic: %s", res.Stdout)
	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W213", "utils"),
		"one mistake, one code: %s", res.Stdout)
}

// TestRevertSuppressionIsVoidedByACorrection: discarding a revert's record
// voids its changelog suppression, so the entry it hid comes back. This is the
// §7.4 rule applied to §7.3, and the shape an operator reaches for after
// reverting the wrong commit.
func TestRevertSuppressionIsVoidedByACorrectionThroughTheBinary(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a good idea after all\n")
	r.Commit("feat(core): a good idea after all")
	good := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a good idea after all\n\nReverts: " + good)
	revert := r.Git("rev-parse", "HEAD")

	r.WriteFile("packages/core/main.txt", "a good idea after all\n")
	r.Commit("chore(core): the revert was the mistake\n\nDeletes: " + revert)
	res := r.ReleaseOK()

	assert.False(t, harness.IsCodePresentForPackage(res.Events, "W212", "core"),
		"there is no suppression left to report: %s", res.Stdout)
	assert.Contains(t, changelogOf(t, r, "core"), "a good idea after all",
		"the entry the revert hid is back")
}

// ---------------------------------------------------------------------------
// Corrections meeting a prerelease train: a correction reaches only pending
// work, and on a train "pending" means "not yet shipped by any prerelease" —
// the seam none of the stable-line scenarios above can exercise.
// ---------------------------------------------------------------------------

// TestCorrectionEditOfPublishedTrainWorkIsANoOp: a unit an earlier prerelease
// of the same train already shipped is published history even while the train
// is still open. Editing it is the same visible no-op as editing stable
// history (W209), and it does not re-admit the shipped work: with no fresh
// cause, the train does not advance.
func TestCorrectionEditOfPublishedTrainWorkIsANoOp(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core)%beta: board the train\n\n---\n\nfeat(utils): bootstrap")
	shipped := r.Git("rev-parse", "HEAD")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	// The bootstrap commit carries two units, so the target needs its unit
	// address (§7.4.1) — a bare sha is E211.
	r.CommitEmpty("fix(core): too late, beta.0 shipped it\n\nEdits: " + shipped + "#1")
	res := r.ReleaseOK()

	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W209", "core"),
		"the operator has to see the correction did not take: %s", res.Stdout)
	assert.True(t, r.IsTagged("core@0.1.0-beta.1"),
		"the carrying fix still releases on its own account; tags: %v", r.TagList())
	log := changelogOf(t, r, "core")
	assert.NotContains(t, entryOf(t, log, "core@0.1.0-beta.1"), "board the train",
		"the shipped record is not re-rendered by the correction")
}

// TestCorrectionDeleteStopsATrainAdvance: a versioning group rides only while
// a record survives, mid-train as much as on the stable line. Discarding the
// only fresh cause leaves the train exactly where the last prerelease put it:
// no member releases, no ride, no counter movement.
func TestCorrectionDeleteStopsATrainAdvance(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish(), Versioning: "fixed"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core)%beta: board the train\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	require.True(t, r.IsTagged("utils@0.1.0-beta.0"), "the group rides the train; tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "the train's only fresh cause\n")
	r.Commit("feat(core)%beta: the next step")
	cause := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + cause)
	res := r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "the train does not advance; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("utils@"), "no member rides a step nothing caused; tags: %v", r.TagList())
	assert.False(t, harness.IsCodePresent(res.Events, "W234"), "no ride to explain: %s", res.Stdout)
}

// TestRevertPairOnATrainRendersCancelLine: a feature and its revert land
// inside one train step. Both leave the notes (§7.3) while both still count
// toward the train's target, so the prerelease releases with an entry that
// says the work cancelled out — never an empty body.
func TestRevertPairOnATrainRendersCancelLine(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core)%beta: board the train\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a bad idea\n")
	r.Commit("feat(core)!: a bad idea")
	bad := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a bad idea\n\nReverts: " + bad)
	res := r.ReleaseOK()

	require.True(t, r.IsTagged("core@1.0.0-beta.0"),
		"the reverted major still counts toward the train's target (§7.3); tags: %v", r.TagList())
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W212", "core"),
		"the plan accounts for the absent entries: %s", res.Stdout)
	entry := entryOf(t, changelogOf(t, r, "core"), "core@1.0.0-beta.0")
	assert.NotContains(t, entry, "a bad idea", "neither entry is documented")
	assert.Contains(t, entry, "No changes: the pending work and its reverts cancel out.",
		"the body says why there is nothing to read")
}

// TestRevertLeavesTheRestOfTheChangelogAlone: a revert takes its target
// and itself out of the entry, and nothing else. The release still documents
// everything the window carries besides those two, which is the difference
// between suppressing a pair of entries and suppressing a release's notes.
func TestRevertLeavesTheRestOfTheChangelogAlone(t *testing.T) {
	r := releasedCorrectionsRepo(t)
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

// TestCorrectionWildcardDeleteSkipsWhatIsAlreadyClaimed: `Deletes: *` discards
// every pending record its commit descends from, with two deliberate
// exceptions. A record a newer, narrower correction already claimed stays with
// that correction, because the newest correction of a record wins and a
// wildcard behind it is not an older claim on the same thing. And control
// units are left alone throughout: they carry no record to discard, and taking
// them would erase the very directives the later phases run on, which is why
// the channel directive here still moves its package.
func TestCorrectionWildcardDeleteSkipsWhatIsAlreadyClaimed(t *testing.T) {
	r := releasedCorrectionsRepo(t)

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

// TestCorrectionUnitSelectorNamesOneRecordInsideACommit: a commit carrying
// several records is corrected one record at a time, which is what the "#n"
// selector is for. The valid selector reaches the record it names and nothing
// beside it, which is visible here as the bump: restating the breaking third
// record as a fix leaves the two patch records around it untouched. A selector
// past the end of the commit is an error rather than a silent no-op, because
// the author plainly meant a record and there is none.
func TestCorrectionUnitSelectorNamesOneRecordInsideACommit(t *testing.T) {
	t.Run("a selector naming the last record", func(t *testing.T) {
		r := releasedCorrectionsRepo(t)
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
		r := releasedCorrectionsRepo(t)
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

// TestCorrectionRestatementThatChangesNothingIsReported: an `Edits` that restates
// its target as the same type, marker and description leaves the record
// exactly as it was. That is almost always a mistake — the author edited the
// footer and forgot the subject line — so it is reported even though applying
// it is harmless.
func TestCorrectionRestatementThatChangesNothingIsReported(t *testing.T) {
	r := releasedCorrectionsRepo(t)
	r.WriteFile("packages/core/streaming.txt", "work\n")
	r.Commit("feat(core): add streaming")
	target := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("feat(core): add streaming\n\nEdits: " + target)

	res := r.StatusOK()
	assert.True(t, harness.IsCodePresent(res.Events, "W211"),
		"the restatement says the same thing as its target: %s", res.Stdout)
}

// TestRevertOfPublishedOrForeignWorkLeavesTheChangelogAlone: `Reverts` is informational, so a
// target it cannot take out of the changelog resolves to "leave the changelog
// alone" and never to an error. A target on the other side of a release is
// published history and cannot be taken out of it; a target whose records
// belong to another package leaves this one's changelog untouched.
func TestRevertOfPublishedOrForeignWorkLeavesTheChangelogAlone(t *testing.T) {
	r := releasedCorrectionsRepo(t)
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

// TestCorrectionTwoInOneCommit: a commit may carry several records,
// and a correction is a record like any other, so one commit can correct two
// different things at once. Within a commit the later unit is the newer one,
// which is the order every other precedence rule uses and the one this has to
// agree with.
func TestCorrectionTwoInOneCommit(t *testing.T) {
	r := releasedCorrectionsRepo(t)
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

// TestCorrectionWildcardEditStandsInForEverythingItClaimed: `Edits: *` is a
// restatement rather than a discard, so the carrying unit stands in for every
// record the wildcard reached — one entry in place of all of them, under the
// type the correction itself was written as.
func TestCorrectionWildcardEditStandsInForEverythingItClaimed(t *testing.T) {
	r := releasedCorrectionsRepo(t)
	r.WriteFile("packages/core/first.txt", "work\n")
	r.Commit("feat(core)!: the first thing that went in")
	r.WriteFile("packages/core/second.txt", "work\n")
	r.Commit("feat(core): the second thing that went in")

	r.CommitEmpty("fix(core): one restatement standing in for both\n\nEdits: *")

	res := r.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(res.Events, "core").Str("version"),
		"the restatement replaces every record it claimed: %s", res.Stdout)
}

// TestCorrectionOfACommitThatCarriesNoRecord: a commit whose message is
// not a release record at all is still in the window, and naming it as a
// target is not an error — there is simply nothing there to correct, which is
// the same no-op a correction of released work is.
func TestCorrectionOfACommitThatCarriesNoRecord(t *testing.T) {
	r := releasedCorrectionsRepo(t)
	r.WriteFile("packages/core/plain.txt", "work\n")
	r.Commit("this message is not a record at all")
	plain := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): restate what was never a record\n\nEdits: " + plain)

	res := r.Status()
	assert.True(t, harness.IsCodePresent(res.Events, "W209"),
		"the target carries no record to correct: %s", res.Stdout)
}

// TestCorrectionDiagnosticsNameWhatTheyCouldNotReach: a correction whose
// targets all left the pending window addresses no package at all, so there is
// no package to report it against — and reporting it anyway, against nothing,
// is the whole point of the no-op diagnostic being the one a package may not
// suppress. A correction that does reach its target says which targets it
// resolved, where a reader looking for that can find it.
func TestCorrectionDiagnosticsNameWhatTheyCouldNotReach(t *testing.T) {
	t.Run("a correction that reaches no package", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		r.ReleaseOK()
		shipped := r.Git("rev-parse", "HEAD")
		require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		r.Commit("chore(release): record the changelog")

		// No scope set of its own, and the only target is history: nothing
		// tells the correction which packages it was ever about.
		r.WriteFile("packages/core/more.txt", "work of its own\n")
		r.Commit("feat(core): work that does release")
		r.CommitEmpty("fix: restate what already shipped\n\nEdits: " + shipped)

		res := r.ReleaseOK()
		require.True(t, harness.IsCodePresent(res.Events, "W209"), "stdout:\n%s", res.Stdout)
		var reported harness.Event
		for _, e := range res.Events {
			if e.Code() == "W209" {
				reported = e
				break
			}
		}
		assert.Empty(t, reported.Package(), "there is no package the correction reached")
		assert.Contains(t, reported.Str("message"), shipped[:7],
			"and the target it looked for is named back")
		assert.True(t, r.IsTagged("core@0.2.0"),
			"the carrying commit's own work still releases; tags: %v", r.TagList())
	})

	t.Run("a correction naming a commit nobody has", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		// A well-formed object id that resolves to nothing: a sha copied out
		// of another clone, or one whose commit was never pushed here.
		r.CommitEmpty("fix(core): restate something\n\nEdits: " +
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

		res := r.Status()
		assert.True(t, harness.IsCodePresent(res.Events, "E210"),
			"the unresolvable target is reported rather than silently dropped: %s", res.Stdout)
		assert.Contains(t, res.Stdout, "deadbeef", "and the target is named back")
	})

	t.Run("a resolved correction names its targets", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		r.ReleaseOK()
		r.Commit("chore(release): record the changelog")

		r.WriteFile("packages/core/main.txt", "a defensive fix, not a rewrite\n")
		r.Commit("feat(core)!: rewrite internals")
		mistake := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(core): rewrite internals\n\nEdits: " + mistake)

		res := r.StatusOK("--log-level", "trace")
		assert.Contains(t, res.Stdout, "correction resolved")
		assert.Contains(t, res.Stdout, mistake, "the target is named as it was written")
	})
}

// TestCorrectionReachesATargetAcrossAMerge: a merge gives the commit graph two
// paths to the same commit. The correction below names a commit both paths
// reach, and it is still an ancestor: the restatement decides the bump, with
// no E210, exactly as it would on a linear history.
func TestCorrectionReachesATargetAcrossAMerge(t *testing.T) {
	r := releasedCorrectionsRepo(t)
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

// releasedCorrectionsRepo is a two-package repository with one release already
// behind it, which is what every correction scenario needs: a pending window
// that starts after a real baseline rather than at the beginning of history.
func releasedCorrectionsRepo(t *testing.T) *harness.Repo {
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
