package ccme

import "testing"

// TestCorrectionFooterParsing covers the parsing vectors of Appendix B.11:
// the Edits and Deletes value grammar of §7.4.1, and where the footers may
// appear.
func TestCorrectionFooterParsing(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	t.Run("valid forms", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			message string
			edits   []CorrectionTarget
			deletes []CorrectionTarget
		}{
			{
				name:    "sha target",
				message: "fix(core): x\n\nEdits: 4f2a1c9",
				edits:   []CorrectionTarget{{SHA: "4f2a1c9", Raw: "4f2a1c9"}},
			},
			{
				name:    "unit selector",
				message: "fix(core): x\n\nEdits: 4f2a1c9#2",
				edits:   []CorrectionTarget{{SHA: "4f2a1c9", UnitSelector: 2, Raw: "4f2a1c9#2"}},
			},
			{
				name:    "wildcard restatement",
				message: "feat(core): x\n\nEdits: *",
				edits:   []CorrectionTarget{{All: true, Raw: "*"}},
			},
			{
				name:    "wildcard deletion on a none-bump type",
				message: "chore(core): x\n\nDeletes: *",
				deletes: []CorrectionTarget{{All: true, Raw: "*"}},
			},
			{
				name:    "breaking restatement",
				message: "fix(core)!: x\n\nEdits: 4f2a1c9",
				edits:   []CorrectionTarget{{SHA: "4f2a1c9", Raw: "4f2a1c9"}},
			},
			{
				name:    "one unit, two corrections",
				message: "fix(core): x\n\nEdits: 4f2a1c9\nDeletes: abcdef0",
				edits:   []CorrectionTarget{{SHA: "4f2a1c9", Raw: "4f2a1c9"}},
				deletes: []CorrectionTarget{{SHA: "abcdef0", Raw: "abcdef0"}},
			},
			{
				name:    "full-length sha",
				message: "fix(core): x\n\nDeletes: 4f2a1c9d8e0b1a2c3d4e5f60718293a4b5c6d7e8",
				deletes: []CorrectionTarget{{
					SHA: "4f2a1c9d8e0b1a2c3d4e5f60718293a4b5c6d7e8",
					Raw: "4f2a1c9d8e0b1a2c3d4e5f60718293a4b5c6d7e8",
				}},
			},
		} {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				res, err := p.Parse(tc.message)
				if err != nil {
					t.Fatalf("%v (codes: %s)", err, codesOf(res))
				}
				d := res.Units[0].Directives
				if got, want := len(d.Edits), len(tc.edits); got != want {
					t.Fatalf("Edits = %v, want %v", d.Edits, tc.edits)
				}
				for i := range tc.edits {
					if d.Edits[i] != tc.edits[i] {
						t.Errorf("Edits[%d] = %+v, want %+v", i, d.Edits[i], tc.edits[i])
					}
				}
				if got, want := len(d.Deletes), len(tc.deletes); got != want {
					t.Fatalf("Deletes = %v, want %v", d.Deletes, tc.deletes)
				}
				for i := range tc.deletes {
					if d.Deletes[i] != tc.deletes[i] {
						t.Errorf("Deletes[%d] = %+v, want %+v", i, d.Deletes[i], tc.deletes[i])
					}
				}
			})
		}
	})

	t.Run("malformed values are E151", func(t *testing.T) {
		t.Parallel()
		for _, value := range []string{
			"XYZ",       // not a sha
			"4F2A1C9",   // lowercase hexadecimal only
			"4f2a1",     // shorter than 7 characters
			"4f2a1c9#0", // selectors are 1-based
			"4f2a1c9#02",
			"4f2a1c9#",
			"4f2a1c9#two",
			"*#1", // the wildcard takes no selector
		} {
			res, err := p.Parse("fix(core): x\n\nEdits: " + value)
			if err == nil || firstError(res) != CodeE151 {
				t.Errorf("Edits: %q = %v (%s), want E151", value, err, codesOf(res))
			}
		}
	})

	t.Run("correction footers on control units", func(t *testing.T) {
		t.Parallel()
		// §7.4.1: E173 on a release unit, and cancel's own E171 covers it.
		res, err := p.Parse("release(core)%stable: x\n\nEdits: 4f2a1c9")
		if err == nil || firstError(res) != CodeE173 {
			t.Errorf("release + Edits = %v (%s), want E173", err, codesOf(res))
		}
		res, err = p.Parse("cancel(core): x\n\nDeletes: *")
		if err == nil || firstError(res) != CodeE171 {
			t.Errorf("cancel + Deletes = %v (%s), want E171", err, codesOf(res))
		}
	})

	t.Run("carrying unit stays ordinary", func(t *testing.T) {
		t.Parallel()
		// The correction unit keeps its own classification: type, breaking
		// marker, bump (§7.4).
		res, err := p.Parse("fix(core)!: rewrite internals\n\nEdits: 4f2a1c9")
		if err != nil {
			t.Fatalf("%v (codes: %s)", err, codesOf(res))
		}
		u := res.Units[0]
		if u.Bump != BumpMajor || !u.Breaking {
			t.Errorf("Bump = %s, Breaking = %v; the carrying unit must classify normally", u.Bump, u.Breaking)
		}
	})
}

// TestRevertsShapeValidation covers §7.3: a Reverts value that is not a
// commit sha is W214 and stays informational; the unit is otherwise intact.
func TestRevertsShapeValidation(t *testing.T) {
	t.Parallel()

	p := DefaultParser()

	res, err := p.Parse("revert(core): undo the reader\n\nReverts: not-a-sha")
	if err != nil {
		t.Fatalf("W214 must not invalidate the unit: %v", err)
	}
	if !hasCode(res, CodeW214) {
		t.Errorf("codes = %s, want W214", codesOf(res))
	}
	u := res.Units[0]
	if len(u.Directives.Reverts) != 1 || u.Directives.Reverts[0] != "not-a-sha" {
		t.Errorf("Reverts = %v; the raw value must still be recorded", u.Directives.Reverts)
	}
	if u.Bump != BumpPatch {
		t.Errorf("Bump = %s; the revert releases and bumps normally", u.Bump)
	}

	res, err = p.Parse("revert(core): undo the reader\n\nReverts: 4f2a1c9")
	if err != nil || hasCode(res, CodeW214) {
		t.Errorf("a well-formed sha must not warn: %v (%s)", err, codesOf(res))
	}
}
