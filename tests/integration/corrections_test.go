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
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a defensive fix, not a rewrite\n")
	r.Commit("feat(core)!: rewrite internals")
	mistake := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("fix(core): rewrite internals\n\nThe change is a refactor with a defensive fix.\n\nEdits: " + mistake)
	res := r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.1"), "the major left with the record carrying it; tags: %v", r.TagList())
	assert.False(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W209"), "the target was pending: %s", res.Stdout)

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
	require.True(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.CommitEmpty("fix(core): too late to restate it\n\nEdits: " + shipped)
	res := r.ReleaseOK()

	assert.True(t, harness.HasCodeForPackage(res.Events, "W209", "core"),
		"the operator has to see that the correction did not take: %s", res.Stdout)
	assert.True(t, r.HasTag("core@1.0.1"), "the carrying unit still releases; tags: %v", r.TagList())

	// W209 is non-suppressible (§17.1), and dispat gets that by owning the
	// code: --quiet-parser hides only the codes the parser itself defines.
	r.Commit("chore(release): record the changelog")
	r.CommitEmpty("fix(core): still too late\n\nEdits: " + shipped)
	quiet := r.ReleaseOK("--quiet-parser")
	assert.True(t, harness.HasCodeForPackage(quiet.Events, "W209", "core"),
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
	original := r.Git("rev-parse", "HEAD")

	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + original)
	r.CommitEmpty("fix(core): restate it instead\n\nEdits: " + original)
	res := r.ReleaseOK()

	assert.True(t, harness.HasCodeForPackage(res.Events, "W210", "core"),
		"the superseded delete must be reported: %s", res.Stdout)
	assert.True(t, r.HasTag("core@0.1.1"), "the restatement decides the bump; tags: %v", r.TagList())
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

	assert.True(t, harness.HasCodeForPackage(res.Events, "W215", "utils"),
		"the voiding must be reported: %s", res.Stdout)
	assert.True(t, r.HasTag("utils@1.0.0"), "the original record returns; tags: %v", r.TagList())
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

	assert.True(t, r.HasTag("core@0.1.1"), "core carries the narrowed restatement; tags: %v", r.TagList())
	assert.True(t, r.HasTag("utils@1.0.0"), "utils keeps the original record; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "E213"), "narrowing is legal: %s", res.Stdout)
	r.Commit("chore(release): record the changelog")

	// The other direction: a correction naming a package its target's record
	// never claimed is refused, and the target stands.
	r.WriteFile("packages/core/main.txt", "core only\n")
	r.Commit("feat(core)!: core only")
	coreOnly := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("fix(*): restate it everywhere\n\nEdits: " + coreOnly)
	res = r.Release()

	assert.True(t, harness.HasCode(res.Events, "E213"),
		"a correction may not extend someone else's record: %s", res.Stdout)
	assert.True(t, r.HasTag("core@1.0.0"), "the target's record survives the void; tags: %v", r.TagList())
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
	assert.True(t, r.HasTag("utils@0.1.1"), "the wildcard reaches only its own scope; tags: %v", r.TagList())
	assert.False(t, harness.HasCodeForPackage(res.Events, "W209", "core"),
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
		assert.True(t, harness.HasCode(res.Events, "E210"), "events:\n%s", res.Stdout)
	})

	t.Run("a bare sha on a multi-unit commit is E211", func(t *testing.T) {
		r.WriteFile("packages/core/multi.txt", "two units\n")
		r.Commit("fix(core): one\n\n---\n\nfix(utils): two")
		multi := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(core): which one?\n\nDeletes: " + multi)
		res := r.Release()
		assert.True(t, harness.HasCode(res.Events, "E211"), "events:\n%s", res.Stdout)
	})

	t.Run("a control unit is E212", func(t *testing.T) {
		r.CommitEmpty("cancel(utils): start over")
		barrier := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(utils): correcting a barrier\n\nEdits: " + barrier)
		res := r.Release()
		assert.True(t, harness.HasCode(res.Events, "E212"), "events:\n%s", res.Stdout)
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
	assert.False(t, harness.HasCode(res.Events, "W234"), "no ride to explain: %s", res.Stdout)
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
	bad := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a bad idea\n\nReverts: " + bad)
	res := r.ReleaseOK()

	assert.True(t, r.HasTag("core@1.0.0"), "the major is still owed; tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W212", "core"),
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

	assert.True(t, harness.HasCodeForPackage(res.Events, "W213", "core"), "events:\n%s", res.Stdout)
	assert.True(t, r.HasTag("core@0.1.1"), "the revert releases as usual; tags: %v", r.TagList())
	assert.Contains(t, changelogOf(t, r, "core"), "something from elsewhere",
		"and is documented as usual")
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/utils/main.txt", "undone\n")
	r.Commit("revert(utils): something\n\nReverts: not-a-sha")
	res = r.ReleaseOK()
	assert.True(t, harness.HasCode(res.Events, "W214"), "the parser's diagnostic: %s", res.Stdout)
	assert.False(t, harness.HasCodeForPackage(res.Events, "W213", "utils"),
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

	assert.False(t, harness.HasCodeForPackage(res.Events, "W212", "core"),
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
	require.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	// The bootstrap commit carries two units, so the target needs its unit
	// address (§7.4.1) — a bare sha is E211.
	r.CommitEmpty("fix(core): too late, beta.0 shipped it\n\nEdits: " + shipped + "#1")
	res := r.ReleaseOK()

	assert.True(t, harness.HasCodeForPackage(res.Events, "W209", "core"),
		"the operator has to see the correction did not take: %s", res.Stdout)
	assert.True(t, r.HasTag("core@0.1.0-beta.1"),
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
	require.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("utils@0.1.0-beta.0"), "the group rides the train; tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "the train's only fresh cause\n")
	r.Commit("feat(core)%beta: the next step")
	cause := r.Git("rev-parse", "HEAD")
	r.CommitEmpty("chore(core): drop it\n\nDeletes: " + cause)
	res := r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "the train does not advance; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("utils@"), "no member rides a step nothing caused; tags: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "W234"), "no ride to explain: %s", res.Stdout)
}

// TestRevertPairOnATrainRendersCancelLine: a feature and its revert land
// inside one train step. Both leave the notes (§7.3) while both still count
// toward the train's target, so the prerelease releases with an entry that
// says the work cancelled out — never an empty body.
func TestRevertPairOnATrainRendersCancelLine(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("feat(core)%beta: board the train\n\n---\n\nfeat(utils): bootstrap")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/core/main.txt", "a bad idea\n")
	r.Commit("feat(core)!: a bad idea")
	bad := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a bad idea\n\nReverts: " + bad)
	res := r.ReleaseOK()

	require.True(t, r.HasTag("core@1.0.0-beta.0"),
		"the reverted major still counts toward the train's target (§7.3); tags: %v", r.TagList())
	assert.True(t, harness.HasCodeForPackage(res.Events, "W212", "core"),
		"the plan accounts for the absent entries: %s", res.Stdout)
	entry := entryOf(t, changelogOf(t, r, "core"), "core@1.0.0-beta.0")
	assert.NotContains(t, entry, "a bad idea", "neither entry is documented")
	assert.Contains(t, entry, "No changes: the pending work and its reverts cancel out.",
		"the body says why there is nothing to read")
}
