package plan

import (
	"bytes"
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
)

// ---------------------------------------------------------------------------
// §7.4 / §13.4b — corrections
// ---------------------------------------------------------------------------
//
// A correction names its target by sha, so these histories use hex commit ids:
// the footer grammar accepts 7 to 64 lowercase hexadecimal characters and
// nothing else, which the "c1" ids of the other suites are not.

const (
	shaA = "aaaaaa1"
	shaB = "bbbbbb2"
	shaC = "ccccc03"
	shaD = "ddddd04"
	shaE = "eeeee05"
)

// unitsOf renders a release's surviving units as "type: description", which is
// what a correction changes about a package's ledger.
func unitsOf(rel *Release) []string {
	out := make([]string, 0, len(rel.Units))
	for _, u := range rel.Units {
		out = append(out, u.Header.Type+": "+u.Header.Description)
	}
	return out
}

func TestCorrectionEditsRestatesTheRecord(t *testing.T) {
	// The specification's own worked example (§7.4, vector 112). A commit was
	// classified as a breaking feature by mistake; the correction discards that
	// record and stands in its place, so the package releases a patch rather
	// than a major and the changelog carries one entry, the restatement.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: rewrite internals"},
		commit{sha: shaB, message: "fix(core): rewrite internals\n\nEdits: " + shaA},
	).tag("core", "1.4.2", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, ccme.BumpPatch, core.Bump, "the major left the window with the record carrying it")
	assertVersion(t, v(1, 4, 3), core.Next)
	assert.Equal(t, []string{"fix: rewrite internals"}, unitsOf(core))
	assert.False(t, hasCode(p, CodeCorrectionNoop), "the target was pending: %v", codes(p))
}

func TestCorrectionOfReleasedWorkIsANoop(t *testing.T) {
	// §7.4.2: a correction reaches only undischarged work. Once the package has
	// released the target the record is published history, so the correction is
	// a no-op and W209 says so. The carrying unit still contributes normally,
	// which is the half of case 111 that is easy to get wrong.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: rewrite internals"},
		commit{sha: shaB, message: "fix(core): restate it\n\nEdits: " + shaA},
	).tag("core", "2.0.0", shaA).tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.True(t, hasCode(p, CodeCorrectionNoop), "the correction must report that it did not take: %v", codes(p))
	assert.Equal(t, ccme.BumpPatch, core.Bump, "the carrying unit still contributes its own record")
	assertVersion(t, v(2, 0, 1), core.Next)
}

func TestCorrectionNoopIsNotAParserDiagnostic(t *testing.T) {
	// W209 is non-suppressible (§17.1). dispat gets that for free by owning the
	// code rather than lifting it from the parser: --quiet-parser hides only
	// codes ccme itself defines. This pins the property rather than the
	// mechanism, so moving the code into the parser's registry fails here.
	assert.False(t, ccme.IsDiagnosticCode(CodeCorrectionNoop),
		"W209 must stay dispat's own, or --quiet-parser could hide it")
}

func TestCorrectionNewestOfATargetWins(t *testing.T) {
	// Vector 116: a delete, then a newer edit of the same target. The
	// restatement is in force and the superseded delete reports W210.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: rewrite internals"},
		commit{sha: shaB, message: "chore(core): drop it\n\nDeletes: " + shaA},
		commit{sha: shaC, message: "fix(core): restate it\n\nEdits: " + shaA},
	).tag("core", "1.4.2", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.True(t, hasCode(p, CodeCorrectionSuperseded), "the older delete must report W210: %v", codes(p))
	assert.Equal(t, ccme.BumpPatch, core.Bump, "the restatement decides the bump")
	assert.Contains(t, unitsOf(core), "fix: restate it")
	assert.NotContains(t, unitsOf(core), "feat: rewrite internals")
}

func TestCorrectionMayNotWidenItsTargetsScope(t *testing.T) {
	// Vector 113: a correction may narrow a record, never widen it. The unit
	// contributes nothing at all, so the target's record stands untouched.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core): a feature"},
		commit{sha: shaB, message: "fix(*): restate it everywhere\n\nEdits: " + shaA},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeCorrectionWidens), "widening must be refused: %v", codes(p))
	assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump, "the target's record survives")
	assert.Equal(t, []string{"feat: a feature"}, unitsOf(p.Releases["core"]))
	assert.False(t, p.Releases["utils"].Changed(), "a voided correction contributes nothing")
}

func TestCorrectionNarrowsAWildcardScopedRecord(t *testing.T) {
	// Vectors 126 and 127: partial corrections. A record scoped (*) is
	// discarded for one package and stands for the others, and the same in the
	// restating form.
	t.Run("partial delete", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: shaA, message: "feat(*): a feature"},
			commit{sha: shaB, message: "chore(core): drop it here\n\nDeletes: " + shaA},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

		p := compute(t, git, nil)

		assert.False(t, p.Releases["core"].Changed(), "core's record went, and chore bumps nothing")
		assert.Equal(t, ccme.BumpMinor, p.Releases["utils"].Bump, "the record stands elsewhere")
		assert.Equal(t, ccme.BumpMinor, p.Releases["app"].Bump)
	})

	t.Run("partial edit", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: shaA, message: "feat(*): a feature"},
			commit{sha: shaB, message: "fix(core): smaller than that\n\nEdits: " + shaA},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

		p := compute(t, git, nil)

		assert.Equal(t, ccme.BumpPatch, p.Releases["core"].Bump)
		assert.Equal(t, []string{"fix: smaller than that"}, unitsOf(p.Releases["core"]))
		assert.Equal(t, ccme.BumpMinor, p.Releases["utils"].Bump)
		assert.Equal(t, []string{"feat: a feature"}, unitsOf(p.Releases["utils"]))
	})
}

func TestCorrectionWildcardClearsItsScopeOnly(t *testing.T) {
	// Vector 115, the pure-deletion form: `chore` maps to none, so the scope's
	// pending records go and nothing replaces them. Packages outside the
	// correction's scope-set are untouched.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core): a feature"},
		commit{sha: shaB, message: "feat(utils): another"},
		commit{sha: shaC, message: "chore(core): restate the changeset\n\nDeletes: *"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["core"].Changed(), "every pending record for core is discarded")
	assert.Equal(t, ccme.BumpMinor, p.Releases["utils"].Bump, "the wildcard reaches only its own scope")
}

func TestCorrectionWildcardLeavesControlUnitsAlone(t *testing.T) {
	// Two rules meeting (§7.4.2, §15.7 case 119). The wildcard never discards a
	// control unit: naming one directly is E212, and clearing the barriers here
	// would let a correction undo a cancel. The cancel therefore still covers
	// the earlier feature, which leaves the wildcard nothing to act on (W209),
	// and work after the barrier is untouched.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: cancelled work"},
		commit{sha: shaB, message: "cancel(core): drop it"},
		commit{sha: shaC, message: "chore(core): clear the rest\n\nDeletes: *"},
		commit{sha: shaD, message: "fix(core): lands after the barrier"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, ccme.BumpPatch, core.Bump, "the cancel still holds and the later fix still counts")
	assert.Equal(t, []string{"fix: lands after the barrier"}, unitsOf(core))
	assert.True(t, hasCode(p, CodeCorrectionNoop), "the wildcard found nothing pending: %v", codes(p))
}

func TestCorrectionOfACancelledTargetIsANoop(t *testing.T) {
	// Case 119 from the other side: a named target a barrier already covers.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: cancelled work"},
		commit{sha: shaB, message: "cancel(core): drop it"},
		commit{sha: shaC, message: "fix(core): restate it\n\nEdits: " + shaA},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeCorrectionNoop), "a cancelled record cannot be corrected: %v", codes(p))
	assert.Equal(t, ccme.BumpPatch, p.Releases["core"].Bump, "the carrying unit still contributes")
}

func TestCorrectionScopelessUnitInheritsItsTargetsPackages(t *testing.T) {
	// Vector 112. A correction commit is typically empty, so §6.2's
	// file-derived fallback would resolve it to nothing; §7.4.2 replaces that
	// with the union of the targets' packages. The unit is not inert either,
	// so W131 must stay quiet.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(utils): a feature", files: []string{"libs/utils/a.go"}},
		commit{sha: shaB, message: "chore: drop it\n\nDeletes: " + shaA},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.False(t, p.Releases["utils"].Changed(), "the record was found through the target's scope")
	assert.False(t, hasCode(p, CodeInertUnit), "a correction is not inert for lacking a scope-set: %v", codes(p))
}

func TestCorrectionDiscardsPropagatedContributions(t *testing.T) {
	// Vector 123: the propagated contributions fall with the record. Without
	// the correction the caret carries core's feature into app.
	history := []commit{
		{sha: shaA, message: "feat(core)^: a feature"},
		{sha: shaB, message: "chore(core): drop it\n\nDeletes: " + shaA},
	}
	base := func() *fakeGit {
		return newFakeGit(history...).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	}

	p := compute(t, base(), nil)
	assert.False(t, p.Releases["app"].Changed(), "the propagation went with the record")

	uncorrected := compute(t, newFakeGit(history[0]).
		tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", ""), nil)
	assert.True(t, uncorrected.Releases["app"].Changed(), "the same history without the correction does propagate")
}

func TestCorrectionVoidChains(t *testing.T) {
	// §7.4.2: a correction whose own record has been discarded is void, and one
	// rule settles every nesting. Each case here is a row of §15.7.
	tags := func(g *fakeGit) *fakeGit {
		return g.tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	}

	t.Run("delete of an edit un-edits", func(t *testing.T) { // 128
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core)!: the original"},
			commit{sha: shaB, message: "fix(core): the restatement\n\nEdits: " + shaA},
			commit{sha: shaC, message: "chore(core): undo the correction\n\nDeletes: " + shaB},
		)), nil)

		assert.True(t, hasCode(p, CodeCorrectionVoid), "the voiding must be reported: %v", codes(p))
		assert.Equal(t, ccme.BumpMajor, p.Releases["core"].Bump, "the original record returns")
		assert.Equal(t, []string{"feat: the original"}, unitsOf(p.Releases["core"]))
	})

	t.Run("delete of a delete restores", func(t *testing.T) { // 129
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core)!: the original"},
			commit{sha: shaB, message: "chore(core): drop it\n\nDeletes: " + shaA},
			commit{sha: shaC, message: "chore(core): undo that\n\nDeletes: " + shaB},
		)), nil)

		assert.Equal(t, ccme.BumpMajor, p.Releases["core"].Bump, "the twice-negated record returns")
	})

	t.Run("edit of a correction replaces it and voids it", func(t *testing.T) { // 130
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core)!: the original"},
			commit{sha: shaB, message: "fix(core): the restatement\n\nEdits: " + shaA},
			commit{sha: shaC, message: "chore(core): replace the correction\n\nEdits: " + shaB},
		)), nil)

		core := p.Releases["core"]
		assert.Equal(t, ccme.BumpMajor, core.Bump, "the correction's target returns, not its restatement")
		assert.Contains(t, unitsOf(core), "feat: the original")
		assert.NotContains(t, unitsOf(core), "fix: the restatement")
	})

	t.Run("a chain of any depth settles in one pass", func(t *testing.T) {
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core)!: the original"},
			commit{sha: shaB, message: "chore(core): drop it\n\nDeletes: " + shaA},
			commit{sha: shaC, message: "chore(core): drop that\n\nDeletes: " + shaB},
			commit{sha: shaD, message: "chore(core): and that\n\nDeletes: " + shaC},
		)), nil)

		// D voids C, so C never voids B, so B's delete of A stands.
		assert.False(t, p.Releases["core"].Changed(), "the innermost delete is back in force")
	})
}

func TestCorrectionVoidIsPerPackage(t *testing.T) {
	// Case 132: a partial delete of a correction's own record voids it for the
	// deleted packages only; it still applies for the rest.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(*)!: the original"},
		commit{sha: shaB, message: "fix(*): the restatement\n\nEdits: " + shaA},
		commit{sha: shaC, message: "chore(core): undo it for core\n\nDeletes: " + shaB},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, ccme.BumpMajor, p.Releases["core"].Bump, "core keeps the original")
	assert.Equal(t, ccme.BumpPatch, p.Releases["utils"].Bump, "utils keeps the restatement")
	assert.Equal(t, []string{"fix: the restatement"}, unitsOf(p.Releases["utils"]))
}

func TestCorrectionTargetErrors(t *testing.T) {
	tags := func(g *fakeGit) *fakeGit {
		return g.tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	}

	t.Run("its own commit is E210", func(t *testing.T) { // 117
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "fix(core): x\n\nEdits: " + shaA},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionUnknownTarget), "codes: %v", codes(p))
		assert.False(t, p.Releases["core"].Changed(), "a voided unit contributes nothing")
	})

	t.Run("a descendant is E210", func(t *testing.T) { // 117
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "fix(core): x\n\nEdits: " + shaB},
			commit{sha: shaB, message: "feat(core): later"},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionUnknownTarget), "codes: %v", codes(p))
	})

	t.Run("an unknown sha is E210", func(t *testing.T) {
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core): a feature"},
			commit{sha: shaB, message: "fix(core): x\n\nEdits: fedcba9"},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionUnknownTarget), "codes: %v", codes(p))
	})

	t.Run("a control unit is E212", func(t *testing.T) { // 118
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "cancel(core): stop"},
			commit{sha: shaB, message: "fix(core): x\n\nEdits: " + shaA},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionControlTarget), "codes: %v", codes(p))
	})

	t.Run("a bare sha on a multi-unit commit is E211", func(t *testing.T) { // 114
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core): one\n\n---\n\nfix(utils): two"},
			commit{sha: shaB, message: "fix(core): x\n\nEdits: " + shaA},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionBadSelector), "codes: %v", codes(p))
	})

	t.Run("a selector out of range is E211", func(t *testing.T) {
		p := compute(t, tags(newFakeGit(
			commit{sha: shaA, message: "feat(core): one"},
			commit{sha: shaB, message: "fix(core): x\n\nEdits: " + shaA + "#3"},
		)), nil)
		assert.True(t, hasCode(p, CodeCorrectionBadSelector), "codes: %v", codes(p))
	})
}

func TestCorrectionSelectorNamesAUnitOfAMultiUnitCommit(t *testing.T) {
	// The positive of the E211 pair: "#2" names the second unit of the message,
	// counted in the message rather than among the units that parsed.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: one\n\n---\n\nfeat(utils)!: two"},
		commit{sha: shaB, message: "chore(utils): drop the second\n\nDeletes: " + shaA + "#2"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.Equal(t, ccme.BumpMajor, p.Releases["core"].Bump, "the first unit is untouched")
	assert.False(t, p.Releases["utils"].Changed(), "the second unit's record went")
}

func TestCorrectionResolvesAnAbbreviatedSHA(t *testing.T) {
	// Targets are 7 to 64 hex characters, so most are abbreviations. Resolving
	// one is git's job, and an abbreviation naming two commits resolves to
	// neither.
	long1 := "abcdef0111111111111111111111111111111111"
	long2 := "abcdef0222222222222222222222222222222222"

	t.Run("an unambiguous abbreviation resolves", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: long1, message: "feat(core)!: a feature"},
			commit{sha: shaB, message: "chore(core): drop it\n\nDeletes: abcdef0111111"},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

		p := compute(t, git, nil)
		assert.False(t, p.Releases["core"].Changed(), "the abbreviation found the record")
	})

	t.Run("an ambiguous abbreviation is E210", func(t *testing.T) {
		git := newFakeGit(
			commit{sha: long1, message: "feat(core): one"},
			commit{sha: long2, message: "feat(core): two"},
			commit{sha: shaC, message: "chore(core): drop it\n\nDeletes: abcdef0"},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

		p := compute(t, git, nil)
		assert.True(t, hasCode(p, CodeCorrectionUnknownTarget), "codes: %v", codes(p))
		assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump, "both records stand")
	})

	t.Run("without the resolver capability the prefix scan answers", func(t *testing.T) {
		// A Git implementation that cannot resolve a revision still has to
		// support corrections; the pass matches the abbreviation against the
		// commits it examined instead.
		inner := newFakeGit(
			commit{sha: long1, message: "feat(core)!: a feature"},
			commit{sha: shaB, message: "chore(core): drop it\n\nDeletes: abcdef0111111"},
		).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

		p := compute(t, &plainGit{inner: inner}, nil)
		assert.False(t, p.Releases["core"].Changed(), "the prefix scan found the record")
	})
}

func TestCorrectionIdenticalRestatementIsReported(t *testing.T) {
	// Vector 120: applied, and W211 reports that it changes nothing.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core): a feature"},
		commit{sha: shaB, message: "feat(core): a feature\n\nEdits: " + shaA},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeCorrectionIdentical), "codes: %v", codes(p))
	assert.Equal(t, ccme.BumpMinor, p.Releases["core"].Bump)
	assert.Equal(t, 1, len(p.Releases["core"].Units), "one record, not two")
}

func TestCorrectionCarriesSeveralTargets(t *testing.T) {
	// Vector 121: both targets are discarded and both are restated by the one
	// carrying record, and the two footers mix freely (§7.4.1).
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: first"},
		commit{sha: shaB, message: "feat(core): second"},
		commit{sha: shaC, message: "fix(core): both of those\n\nEdits: " + shaA + "\nDeletes: " + shaB},
	).tag("core", "1.4.2", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	assert.Equal(t, ccme.BumpPatch, core.Bump)
	assert.Equal(t, []string{"fix: both of those"}, unitsOf(core))
}

func TestCorrectionOfPrereleasedWorkIsANoop(t *testing.T) {
	// Discharge is not only "left the window". A prerelease train publishes its
	// commits while the window still spans them, and published is published:
	// correcting one is a no-op (§13.4a).
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core)!: shipped in the beta"},
		commit{sha: shaB, message: "fix(core): restate it\n\nEdits: " + shaA},
	).tag("core", "1.0.0", "").tag("core", "2.0.0-beta.0", shaA).
		tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)

	assert.True(t, hasCode(p, CodeCorrectionNoop), "codes: %v", codes(p))
	assert.Equal(t, ccme.BumpMajor, p.Releases["core"].Bump, "the published record still counts")
}

func TestCorrectionsAreTraced(t *testing.T) {
	// A wrong plan is the hardest thing to debug about a release, and a
	// correction is invisible in the output: the record it discarded simply is
	// not there. The trace is where "which record went, and why" is answerable.
	var buf bytes.Buffer
	pkgs, deps := testPackages()
	_, err := Compute(context.Background(), newFakeGit(
		commit{sha: shaA, message: "feat(core)!: the original"},
		commit{sha: shaB, message: "fix(core): the restatement\n\nEdits: " + shaA},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", ""),
		Options{
			Packages: pkgs, Dependencies: deps, Root: "/r",
			Log: zerolog.New(&buf).Level(zerolog.TraceLevel),
		})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "plan: corrections applied", "the phase summary")
	assert.Contains(t, out, "plan: correction resolved", "the correction and its targets")
	assert.Contains(t, out, "plan: record discarded", "the record that went, per package")
	assert.Contains(t, out, shortKey(shaA), "the trace names the target")
}

func TestCorrectionsAreIdempotent(t *testing.T) {
	// §17.2: the same repository plans the same way every time. Corrections
	// mutate the tuple stream, so a pass that leaked state across a computation
	// would show up here.
	history := []commit{
		{sha: shaA, message: "feat(*)!: the original"},
		{sha: shaB, message: "fix(core): the restatement\n\nEdits: " + shaA},
		{sha: shaC, message: "chore(utils): clear utils\n\nDeletes: *"},
		{sha: shaD, message: "chore(core): undo the restatement\n\nDeletes: " + shaB},
		{sha: shaE, message: "fix(app): unrelated"},
	}
	build := func() *fakeGit {
		return newFakeGit(history...).
			tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	}

	first := compute(t, build(), nil)
	second := compute(t, build(), nil)

	require.Equal(t, codes(first), codes(second), "the same history must raise the same diagnostics in the same order")
	for _, name := range []string{"core", "utils", "app"} {
		assert.Equal(t, first.Releases[name].Next.String(), second.Releases[name].Next.String(), name)
		assert.Equal(t, unitsOf(first.Releases[name]), unitsOf(second.Releases[name]), name)
	}
}
