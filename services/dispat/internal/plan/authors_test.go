// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Unit tests of the attribution the planner resolves: who a unit is by, who a
// window is by, and the two of them narrowing the same way the notes do.

func TestParseCoAuthor(t *testing.T) {
	// The trailer is free text — nothing rejects a malformed one at commit
	// time — so every shape a person actually types has to land somewhere
	// sensible rather than being read as a name with angle brackets in it.
	for name, tc := range map[string]struct {
		value string
		want  Author
		ok    bool
	}{
		"conventional":    {"Ada Lovelace <ada@example.com>", Author{"Ada Lovelace", "ada@example.com"}, true},
		"untrimmed":       {"  Ada Lovelace   <ada@example.com>  ", Author{"Ada Lovelace", "ada@example.com"}, true},
		"bare_name":       {"Ada Lovelace", Author{Name: "Ada Lovelace"}, true},
		"bare_email":      {"ada@example.com", Author{Email: "ada@example.com"}, true},
		"empty_brackets":  {"Ada Lovelace <>", Author{Name: "Ada Lovelace"}, true},
		"email_only_form": {"<ada@example.com>", Author{Email: "ada@example.com"}, true},
		"no_at_in_angles": {"Ada <localhost>", Author{"Ada", "localhost"}, true},
		"blank":           {"", Author{}, false},
		"whitespace":      {"   ", Author{}, false},
		"nothing_at_all":  {"<>", Author{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ParseCoAuthor(tc.value)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestAuthorUsername(t *testing.T) {
	// The username is the local part of the address, which is what a forge
	// account is called. The noreply form GitHub generates carries a numeric
	// id in front of it and must not lose the name after the "+".
	for name, tc := range map[string]struct {
		author Author
		want   string
	}{
		"plain":          {Author{"Ada", "ada@example.com"}, "ada"},
		"github_noreply": {Author{"Ada", "12345+ada@users.noreply.github.com"}, "12345+ada"},
		"no_at":          {Author{"Ada", "localhost"}, "localhost"},
		"no_email":       {Author{Name: "Ada Lovelace"}, "Ada Lovelace"},
		"empty":          {Author{}, ""},
		"subdomain":      {Author{"Ada", "ada@mail.example.co.uk"}, "ada"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.author.Username())
		})
	}
}

func TestAuthorKeyPrefersTheEmail(t *testing.T) {
	// One person commits under several spellings of their name far more often
	// than under several addresses, so the address is the identity when there
	// is one and the case never matters on either.
	assert.Equal(t, Author{"Ada", "ADA@example.com"}.key(), Author{"A. Lovelace", "ada@example.com"}.key())
	assert.Equal(t, Author{Name: "Ada"}.key(), Author{Name: "ADA"}.key())
	assert.NotEqual(t, Author{Name: "Ada"}.key(), Author{"Ada", "ada@example.com"}.key(),
		"an identity with an address is not the nameless one")
}

// parseUnit parses one message and returns its first valid unit.
func parseUnit(t *testing.T, msg string) *ccme.Unit {
	t.Helper()
	parser, err := ccme.NewParser(ccme.Config{})
	require.NoError(t, err)
	res, _ := parser.Parse(msg)
	require.NotNil(t, res)
	units := res.ValidUnits()
	require.NotEmpty(t, units, "message did not parse: %q", msg)
	return units[0]
}

func TestUnitAuthorsCollectsTheGitAuthorThenTheTrailers(t *testing.T) {
	c := gitx.Commit{AuthorName: "Ada Lovelace", AuthorEmail: "ada@example.com"}
	u := parseUnit(t, "feat(core): streaming\n\nBody.\n\n"+
		"Co-authored-by: Grace Hopper <grace@example.com>\n"+
		"Co-authored-by: Alan Turing <alan@example.com>\n")

	assert.Equal(t, []Author{
		{"Ada Lovelace", "ada@example.com"},
		{"Grace Hopper", "grace@example.com"},
		{"Alan Turing", "alan@example.com"},
	}, unitAuthors(c, u), "the git author leads, then the trailers in written order")
}

func TestUnitAuthorsDedupesATrailerRepeatingTheGitAuthor(t *testing.T) {
	// A squash-merge of one person's own branch produces exactly this: their
	// own address as both the author and a trailer. Rendering it twice would
	// make every squashed entry read as if two people had written it.
	c := gitx.Commit{AuthorName: "Ada Lovelace", AuthorEmail: "ada@example.com"}
	u := parseUnit(t, "feat(core): streaming\n\nBody.\n\n"+
		"Co-authored-by: A. Lovelace <ADA@example.com>\n")

	assert.Equal(t, []Author{{"Ada Lovelace", "ada@example.com"}}, unitAuthors(c, u),
		"one address is one person, whatever the name beside it says")
}

func TestUnitAuthorsAcceptsAMiscasedTrailerKey(t *testing.T) {
	// Co-authored-by is not in the §8.1 footer registry, so CanonicalKey keeps
	// whatever spelling was written. MessageLevel is computed
	// case-insensitively, so a shouted trailer reaches the collector with its
	// own casing intact and an exact string comparison would silently drop it.
	c := gitx.Commit{AuthorName: "Ada", AuthorEmail: "ada@example.com"}
	for _, key := range []string{"Co-authored-by", "Co-Authored-By", "CO-AUTHORED-BY", "co-authored-by"} {
		u := parseUnit(t, "feat(core): streaming\n\nBody.\n\n"+key+": Grace Hopper <grace@example.com>\n")
		assert.Equal(t, []Author{{"Ada", "ada@example.com"}, {"Grace Hopper", "grace@example.com"}},
			unitAuthors(c, u), "trailer key %q", key)
	}
}

func TestUnitAuthorsSkipsMalformedTrailersAndBlankIdentities(t *testing.T) {
	c := gitx.Commit{AuthorName: "Ada", AuthorEmail: "ada@example.com"}
	u := parseUnit(t, "feat(core): streaming\n\nBody.\n\nCo-authored-by: <>\n")
	assert.Equal(t, []Author{{"Ada", "ada@example.com"}}, unitAuthors(c, u),
		"a trailer naming nobody is not an author")

	// A commit with no identity at all contributes nothing rather than an
	// empty author the renderer would have to special-case.
	u2 := parseUnit(t, "feat(core): streaming\n")
	assert.Empty(t, unitAuthors(gitx.Commit{}, u2))
}

func TestUnitAuthorsIgnoresOtherMessageLevelTrailers(t *testing.T) {
	// Signed-off-by and Reviewed-by are message-level too (§4.5), and neither
	// says the person wrote the change.
	c := gitx.Commit{AuthorName: "Ada", AuthorEmail: "ada@example.com"}
	u := parseUnit(t, "feat(core): streaming\n\nBody.\n\n"+
		"Signed-off-by: Grace Hopper <grace@example.com>\n"+
		"Reviewed-by: Alan Turing <alan@example.com>\n")
	assert.Equal(t, []Author{{"Ada", "ada@example.com"}}, unitAuthors(c, u))
}

func TestDedupeAuthorsKeepsFirstOccurrenceOrder(t *testing.T) {
	// The order carries the meaning — the git author before the trailers, the
	// commit sequence across a window — so sorting would throw away the one
	// thing the list says besides who.
	in := []Author{
		{"Ada", "ada@example.com"},
		{"Grace", "grace@example.com"},
		{"A. Lovelace", "ada@example.com"},
		{Name: "Nameless"},
		{Name: "NAMELESS"},
	}
	assert.Equal(t, []Author{
		{"Ada", "ada@example.com"}, {"Grace", "grace@example.com"}, {Name: "Nameless"},
	}, dedupeAuthors(in))
}

// ---------------------------------------------------------------------------
// Compute-level: the attribution as the planner resolves it
// ---------------------------------------------------------------------------

func TestComputePopulatesUnitAuthors(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): streaming\n\nBody.\n\n" +
			"Co-authored-by: Grace Hopper <grace@example.com>\n",
			files: []string{"libs/core/a.txt"}, author: "Ada Lovelace", email: "ada@example.com"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	rel := p.Releases["core"]
	require.NotNil(t, rel)
	require.Len(t, rel.Units, 1)

	assert.Equal(t, []Author{
		{"Ada Lovelace", "ada@example.com"}, {"Grace Hopper", "grace@example.com"},
	}, rel.AuthorsFor(rel.Units[0]))
	assert.Equal(t, []Author{{"Ada Lovelace", "ada@example.com"}}, rel.WindowAuthors,
		"the window counts commits, so a co-author is not one of its entries")
}

func TestComputeWindowAuthorsIncludeCommitsWithNoValidUnit(t *testing.T) {
	// A commit whose message is not a release record still changed the
	// package, and its author still worked on the release. The unit-derived
	// attribution cannot see them, which is exactly what "all" exists for.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): streaming",
			files: []string{"libs/core/a.txt"}, author: "Ada", email: "ada@example.com"},
		commit{sha: "c2", message: "wip: not a record at all",
			files: []string{"libs/core/b.txt"}, author: "Grace", email: "grace@example.com"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	rel := p.Releases["core"]
	require.NotNil(t, rel)

	assert.Equal(t, []Author{{"Grace", "grace@example.com"}, {"Ada", "ada@example.com"}},
		rel.WindowAuthors, "newest commit first, matching the order the units are collected in")

	var fromUnits []Author
	for _, u := range rel.Units {
		fromUnits = append(fromUnits, rel.AuthorsFor(u)...)
	}
	assert.Equal(t, []Author{{"Ada", "ada@example.com"}}, dedupeAuthors(fromUnits),
		"the invalid commit's author reaches the window and not the units")
}

func TestComputeWindowAuthorsForAPackageWhoseOnlyCommitIsInvalid(t *testing.T) {
	// The package releases for another reason (a pin here), so it has an entry
	// to write and one author to write on it, and no unit to hang them off.
	git := newFakeGit(
		commit{sha: "c1", message: "chore(core): not a release record\n\nRelease-As: 2.0.0\n",
			files: []string{"libs/core/a.txt"}, author: "Ada", email: "ada@example.com"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	rel := p.Releases["core"]
	require.NotNil(t, rel)
	assert.Equal(t, []Author{{"Ada", "ada@example.com"}}, rel.WindowAuthors)
	assert.Equal(t, rel.WindowAuthors, rel.AllAuthors(),
		"a stable release is attributed to its whole window")
}

func TestComputeFreshWindowAuthorsNarrowOnAPrerelease(t *testing.T) {
	// The author-side twin of FreshUnits: beta.1 is by whoever wrote beta.1,
	// not by everyone the train has ever seen. The stable graduation widens
	// back to the whole window, which AllAuthors is what decides.
	git := newFakeGit(
		commit{sha: "c0", message: "fix(core): groundwork",
			files: []string{"libs/core/zero.txt"}, author: "Linus", email: "linus@example.com"},
		commit{sha: "c1", message: "feat(core)%beta: first beta work",
			files: []string{"libs/core/a.txt"}, author: "Ada", email: "ada@example.com"},
		commit{sha: "c2", message: "fix(core): later work",
			files: []string{"libs/core/b.txt"}, author: "Grace", email: "grace@example.com"},
		commit{sha: "c3", message: "fix(core): newest work",
			files: []string{"libs/core/c.txt"}, author: "A. Lovelace", email: "ADA@example.com"},
	).tag("core", "1.0.0", "").tag("core", "1.1.0-beta.0", "c1").
		tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	rel := p.Releases["core"]
	require.NotNil(t, rel)
	require.True(t, rel.IsPrerelease(), "next: %s", rel.Next)

	assert.Equal(t, []Author{{"A. Lovelace", "ADA@example.com"}, {"Grace", "grace@example.com"}, {"Linus", "linus@example.com"}},
		rel.WindowAuthors, "newest spelling wins for duplicate identities while the window spans the train")
	assert.Equal(t, []Author{{"A. Lovelace", "ADA@example.com"}, {"Grace", "grace@example.com"}}, rel.FreshWindowAuthors,
		"published authors are excluded unless newer fresh work names the same identity")
	assert.Equal(t, rel.FreshWindowAuthors, rel.AllAuthors(),
		"a prerelease is attributed to its own changeset alone")
}

func TestComputeSuppressedUnitAuthorStaysInTheWindow(t *testing.T) {
	// A revert takes the unit's entry out of the notes (§7.3), so the ccme
	// attribution loses it with the line. The commit still happened, so the
	// window keeps the author and "all" still credits them.
	git := newFakeGit(
		commit{sha: shaA, message: "feat(core): a bad idea",
			author: "Ada", email: "ada@example.com"},
		commit{sha: shaB, message: "revert(core): a bad idea\n\nReverts: " + shaA,
			author: "Grace", email: "grace@example.com"},
	).tag("core", "1.0.0", "").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")

	p := compute(t, git, nil)
	rel := p.Releases["core"]
	require.NotNil(t, rel)
	require.Empty(t, notesOf(rel), "§7.3 suppresses both entries")

	assert.Equal(t, []Author{{"Grace", "grace@example.com"}, {"Ada", "ada@example.com"}},
		rel.WindowAuthors, "the reverted work was still done by somebody")
	assert.Empty(t, sectionAuthorsUnderCCME(rel),
		"with no notes units left there is nobody the entry's own lines attribute")
}

// sectionAuthorsUnderCCME is what a "ccme" authors section would list: the
// union over the units the entry actually renders. The renderer's own copy
// lives in internal/changelog; this one keeps the planner's half of the claim
// testable without importing it.
func sectionAuthorsUnderCCME(rel *Release) []Author {
	var out []Author
	for _, u := range rel.NotesUnits() {
		out = append(out, rel.AuthorsFor(u)...)
	}
	return dedupeAuthors(out)
}

func TestAllAuthorsIsNilSafeOnAHandBuiltRelease(t *testing.T) {
	// Renderers read these through the accessors, and a Release built by a
	// caller rather than by Compute has neither map nor slice.
	rel := &Release{}
	assert.Nil(t, rel.AllAuthors())
	assert.Nil(t, rel.AuthorsFor(nil))
}
