// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik
package integration

// Coverage scenarios: writing into a changelog somebody else started.
//
// Only the top of a file dispat wrote is ever re-rendered; everything else is
// the file's own bytes. That promise has one hard case: deciding where the
// existing entries begin. A file with a hand-written preamble and no entries
// at all is entirely preamble, and a heading inside a fenced code block is
// documentation about entries rather than an entry — mistake either and the
// new entry lands inside somebody's prose.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handWrittenChangelog is a changelog kept by hand: a title dispat did not
// write, prose, and a fenced example that looks exactly like an entry
// heading.
const handWrittenChangelog = "# Core history\n" +
	"\n" +
	"Kept by hand until now. Entries look like this:\n" +
	"\n" +
	"```md\n" +
	"## core@9.9.9 (1999-01-01)\n" +
	"```\n" +
	"\n" +
	"Thanks for reading.\n"

// TestCovChangelogOpensTheRecordUnderAHandWrittenPreamble: a file with no
// entry headings of its own is all preamble, and a heading inside a fenced
// block is not an entry. The first dispat entry goes under the whole thing,
// and the next one goes above it without disturbing the prose.
func TestCovChangelogOpensTheRecordUnderAHandWrittenPreamble(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.WriteFile("packages/core/CHANGELOG.md", handWrittenChangelog)
	r.Commit("feat(core): first feature")

	res := r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	first := changelogOf(t, r, "core")
	assertOrderedIn(t, first,
		"# Core history",
		"```md",
		"## core@9.9.9 (1999-01-01)",
		"Thanks for reading.",
		"## core@0.1.0 (",
	)
	assert.Equal(t, 1, strings.Count(first, "## core@0.1.0 ("), "one entry:\n%s", first)

	// Release the entry that was just written, then earn a second one.
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	r.WriteFile("packages/core/main.txt", "fixed\n")
	r.Commit("fix(core): repair the first feature")

	res = r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	second := changelogOf(t, r, "core")
	assertOrderedIn(t, second,
		"# Core history",
		"## core@9.9.9 (1999-01-01)",
		"Thanks for reading.",
		"## core@0.1.1 (",
		"## core@0.1.0 (",
	)
	assert.Contains(t, second, "Kept by hand until now.", "the prose survives the rewrite")
}
