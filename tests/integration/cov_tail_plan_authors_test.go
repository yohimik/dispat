// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the identities a commit message hands the attribution.
//
// `Co-authored-by` is free text. No specification governs what a person types
// after the colon, and the trailer is written by hand, by a merge queue, by a
// forge's "co-authored by" button and by a squash of somebody else's branch.
// Every degenerate form therefore has to resolve to either an identity or to
// nothing at all, silently: a malformed trailer is a typo in a message that
// has already been written, and no diagnostic could now be acted on.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailCoAuthorTrailersInEveryShape: the conventional "Name <email>"
// beside the four degenerate ones. A bare name and a bare address are both
// accepted — inventing nothing beats attributing to nobody — while a pair of
// empty angle brackets carries neither and is dropped. The identity with no
// address is rendered from its name even under the username format, which
// takes the local part of an address there is none of, so that the list never
// renders an empty author.
func TestCovTailCoAuthorTrailersInEveryShape(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Format: "username",
	}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): add streaming\n\n"+
		"Co-authored-by: Grace Hopper\n"+
		"Co-authored-by: alan@example.com\n"+
		"Co-authored-by: <>\n"+
		"Co-authored-by: "+adaName+" <"+adaMail+">\n")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")

	assert.Contains(t, entry, "### Authors")
	assert.Contains(t, entry, "ada", "the git author, as the local part of the address")
	assert.Contains(t, entry, "alan", "the bare address, as its local part")
	assert.Contains(t, entry, graceMsg,
		"and the bare name whole, since there is no address to take a part of")
	assert.Equal(t, 1, strings.Count(entry, "ada"),
		"the trailer naming the git author again is deduplicated away: %s", entry)
	assert.NotContains(t, entry, "<>", "the empty identity is dropped rather than rendered")
}

// TestCovTailAuthorIdentityWithNoAddress: git accepts a commit whose author
// has a name and no address at all, and the attribution has to survive it —
// both in what it renders and in how it decides two commits are by the same
// person, which is the address whenever there is one and the name otherwise.
func TestCovTailAuthorIdentityWithNoAddress(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Format: "username", Commits: "all",
	}))
	r.SeedPackage("packages", "core")
	r.CommitAs("Nameless Contributor", "", "feat(core): work by somebody with no address")
	r.WriteFile("packages/core/second.txt", "work\n")
	r.CommitAs("Nameless Contributor", "", "fix(core): more work by the same person")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")

	assert.Contains(t, entry, "Nameless Contributor",
		"the identity renders from its name: %s", entry)
	assert.Equal(t, 1, strings.Count(entry, "Nameless Contributor"),
		"and two commits by that name are one person: %s", entry)
}
