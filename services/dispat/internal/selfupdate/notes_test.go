package selfupdate

// What a release body becomes on the way to a terminal. The parser is the one
// piece of self-update that reads text somebody else wrote, so most of what is
// here is the shapes that text arrives in: fences, rules, CRLF, headings that
// are not headings, and bodies that say nothing at all.

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// theRealBody is the body dispat's own releases carry after the install lines
// moved into the footer: sections, the release block, the rule, then the
// install commands and links. Several tests read from it because the whole
// point of the parser is this exact shape.
const theRealBody = `### Features

- print a release's notes after an update
- carry the body off the listing

### Fixes

- stop a truncated listing from failing opaquely

### Release

- commit: abc123
- tag: services/dispat/v1.1.0

---

**Install this version:**

` + "```sh" + `
# Linux and macOS
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/v1.1.0/install.sh | sh
` + "```" + `

[Documentation](https://dispat.dev/) · [Container images](https://hub.docker.com/u/yohimik)
`

// TestParseNotesReadsARealReleaseBody: the shape dispat actually publishes,
// end to end. The sections survive with their bullets in order, and everything
// past the rule — the install commands, the links — is gone, which is the
// whole reason the rule leads the footer.
func TestParseNotesReadsARealReleaseBody(t *testing.T) {
	notes := ParseNotes(theRealBody)

	require.Len(t, notes.Sections, 3)
	assert.Equal(t, "Features", notes.Sections[0].Title)
	assert.Equal(t, []Item{
		{Text: "print a release's notes after an update"},
		{Text: "carry the body off the listing"},
	}, notes.Sections[0].Items)
	assert.Equal(t, "Fixes", notes.Sections[1].Title)
	assert.Equal(t, "Release", notes.Sections[2].Title)
	assert.False(t, notes.Truncated)
	assert.Equal(t, 5, notes.Items())

	rendered := notes.Render("1.1.0")
	assert.Contains(t, rendered, "what changed in 1.1.0")
	assert.NotContains(t, rendered, "curl", "the install commands are past the rule")
	assert.NotContains(t, rendered, "Install this version")
	assert.NotContains(t, rendered, "Documentation]", "so are the links")
}

// TestParseNotesShapes: one body per thing a release body does, because the
// parser's whole job is to be unsurprised by any of them.
func TestParseNotesShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		body     string
		sections []Section
	}{
		"empty body": {
			body: "",
		},
		"whitespace only": {
			body: "\n\n   \n\t\n",
		},
		"a heading with nothing under it": {
			body: "### Features\n",
		},
		"prose before the first heading opens an untitled section": {
			// What a shared-versioning ride publishes: no sections at all,
			// one sentence saying why. Dropping it would leave the reader
			// with an empty update.
			body:     "No changes: a version bump to keep the versioning group on minor.\n",
			sections: []Section{{Text: []string{"No changes: a version bump to keep the versioning group on minor."}}},
		},
		"a commit body hangs under its bullet": {
			body: "### Fixes\n\n- the header line\nthe body of the commit\nand its second line\n",
			sections: []Section{{Title: "Fixes", Items: []Item{
				{Text: "the header line", Body: []string{"the body of the commit", "and its second line"}},
			}}},
		},
		"a correction note stays on the line it annotates": {
			body: "### Features\n\n- streaming (corrects abc1234)\n",
			sections: []Section{{Title: "Features", Items: []Item{
				{Text: "streaming (corrects abc1234)"},
			}}},
		},
		"every bullet marker": {
			body: "### Features\n\n- dash\n* star\n+ plus\n",
			sections: []Section{{Title: "Features", Items: []Item{
				{Text: "dash"}, {Text: "star"}, {Text: "plus"},
			}}},
		},
		"a nested bullet is a bullet": {
			body: "### Features\n\n- outer\n  - inner\n",
			sections: []Section{{Title: "Features", Items: []Item{
				{Text: "outer"}, {Text: "inner"},
			}}},
		},
		"every heading level": {
			body: "# One\n\n- a\n\n###### Six\n\n- b\n",
			sections: []Section{
				{Title: "One", Items: []Item{{Text: "a"}}},
				{Title: "Six", Items: []Item{{Text: "b"}}},
			},
		},
		"a closing hash run is not part of the title": {
			body:     "### Features ###\n\n- a\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a heading of nothing but hashes is not a heading": {
			body:     "### ###\n\n- a\n",
			sections: []Section{{Text: []string{"### ###"}, Items: []Item{{Text: "a"}}}},
		},
		"a run of hashes with no text is not a heading": {
			body:     "###\n\n- a\n",
			sections: []Section{{Text: []string{"###"}, Items: []Item{{Text: "a"}}}},
		},
		"past six hashes is not a heading": {
			body:     "####### Seven\n\n- a\n",
			sections: []Section{{Text: []string{"####### Seven"}, Items: []Item{{Text: "a"}}}},
		},
		"an issue reference is not a heading": {
			// "#42" has no space after the hash, so it is prose.
			body:     "### Fixes\n\n- closes\n#42 was the report\n",
			sections: []Section{{Title: "Fixes", Items: []Item{{Text: "closes", Body: []string{"#42 was the report"}}}}},
		},
		"CRLF endings leave no carriage return behind": {
			body:     "### Features\r\n\r\n- streaming\r\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "streaming"}}}},
		},
		"a fenced block is skipped whole": {
			body:     "### Features\n\n- a\n\n```sh\n### Not A Heading\n- not a bullet\n```\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}, {Text: "b"}}}},
		},
		"a tilde fence is a fence too": {
			body:     "### Features\n\n~~~\n### Not A Heading\n~~~\n\n- a\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a backtick fence is not closed by tildes": {
			body:     "### Features\n\n```\n~~~\n### Still Inside\n```\n\n- a\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"an unterminated fence swallows the rest": {
			body:     "### Features\n\n- a\n\n```sh\ncurl | sh\n\n### Fixes\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a rule after a blank line ends the notes": {
			body:     "### Features\n\n- a\n\n---\n\n### Fixes\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a rule straight after a fence ends them too": {
			// The fenced block is a block of its own, so the rule below it is a
			// rule and not the underline of whatever preceded the fence.
			body:     "### Features\n\n- a\n```sh\nx\n```\n---\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"asterisk and underscore rules end them too": {
			body:     "### Features\n\n- a\n\n***\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a spaced rule is still a rule": {
			body:     "### Features\n\n- a\n\n- - -\n\n- b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}}},
		},
		"a setext underline is not a rule": {
			// "Features" over "---" is markdown's other way of writing a
			// heading. Cutting there would end the notes at their first word.
			body: "Features\n---\n\n- a\n",
			sections: []Section{{
				Text:  []string{"Features", "---"},
				Items: []Item{{Text: "a"}},
			}},
		},
		"control characters are stripped": {
			body:     "### Fe\x1b[31matures\x00\n\n- a\x07b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "ab"}}}},
		},
		"a window title sequence is stripped whole": {
			body:     "### Features\n\n- a\x1b]0;owned\x07b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "ab"}}}},
		},
		"a two byte escape is stripped": {
			body:     "### Features\n\n- a\x1bcb\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "ab"}}}},
		},
		"an unfinished escape takes the rest of its line": {
			body:     "### Features\n\n- keep\n- drop\x1b[31\n- keep too\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "keep"}, {Text: "drop"}, {Text: "keep too"}}}},
		},
		"text outside ASCII survives": {
			body:     "### Features\n\n- ünïcode · 日本語\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "ünïcode · 日本語"}}}},
		},
		"a tab survives, being ordinary whitespace": {
			body:     "### Features\n\n- a\tb\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a\tb"}}}},
		},
		"a blockquote is prose, not a bullet": {
			// Two characters in with a space between them, like a bullet, but
			// the marker is not one of the three.
			body:     "### Features\n\n> a quote\n- a\n",
			sections: []Section{{Title: "Features", Text: []string{"> a quote"}, Items: []Item{{Text: "a"}}}},
		},
		"an unfinished window title sequence takes the rest of its line": {
			body:     "### Features\n\n- keep\n- drop\x1b]0;never closed\n- keep too\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "keep"}, {Text: "drop"}, {Text: "keep too"}}}},
		},
		"a line ending on a bare escape": {
			body:     "### Features\n\n- a change\x1b\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a change"}}}},
		},
		"a bare dash is neither bullet nor rule": {
			body:     "### Features\n\n-\n- a\n",
			sections: []Section{{Title: "Features", Items: []Item{{Text: "a"}}, Text: []string{"-"}}},
		},
		"a body that is only a footer yields nothing": {
			body: "---\n\n[Documentation](https://example.invalid)\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			notes := ParseNotes(tc.body)
			if tc.sections == nil {
				assert.True(t, notes.Empty(), "nothing to print")
				assert.Empty(t, notes.Render("1.0.0"), "and nothing printed")
				return
			}
			assert.Equal(t, tc.sections, notes.Sections)
			assert.False(t, notes.Empty())
		})
	}
}

// TestParseNotesCapsWhatItReads: a body far past anything a release carries is
// read up to the cap and says so, rather than being walked in full or refused.
// The cut lands on a line boundary, so the last thing read is something the
// release actually wrote rather than half of it.
func TestParseNotesCapsWhatItReads(t *testing.T) {
	body := "### Features\n\n" + strings.Repeat("- a change\n", maxNotesBody/8)
	notes := ParseNotes(body)

	require.True(t, notes.Truncated, "the body was longer than the parser reads")
	require.NotEmpty(t, notes.Sections, "what was read is still worth printing")
	for _, item := range notes.Sections[0].Items {
		assert.Equal(t, "a change", item.Text, "every line read is a whole one")
	}
	assert.Contains(t, notes.Render("1.1.0"), "the changelog has all of them")
}

// TestNotesRenderBoundsOneLongLine: a body that never breaks a line is one
// line as far as the line count is concerned, so without a length bound the
// whole of it would reach the terminal. Both caps together are what make the
// output bounded whatever shape the body arrives in.
func TestNotesRenderBoundsOneLongLine(t *testing.T) {
	rendered := ParseNotes("- " + strings.Repeat("a", maxNotesBody+10)).Render("1.1.0")

	assert.Less(t, len(rendered), 4*maxNotesLineLen, "one long line stays one short line")
	assert.Contains(t, rendered, " ...", "and says it was cut")
	assert.Contains(t, rendered, "the changelog has all of them")
}

// TestClipEndsOnARuneBoundary: cutting a line in the middle of a multi-byte
// character would print a replacement glyph where a letter was.
func TestClipEndsOnARuneBoundary(t *testing.T) {
	clipped, cut := clip(strings.Repeat("é", 20), 11)
	assert.True(t, cut)
	assert.True(t, utf8.ValidString(clipped), "no half written rune")
	assert.Equal(t, "ééééé ...", clipped)

	whole, cut := clip("short", 200)
	assert.False(t, cut)
	assert.Equal(t, "short", whole)
}

// TestNotesRenderCapsWhatItPrints: a release with more changes than a terminal
// wants prints the beginning and points at the changelog for the rest, so a
// long release never scrolls the install lines off the screen.
func TestNotesRenderCapsWhatItPrints(t *testing.T) {
	var b strings.Builder
	b.WriteString("### Features\n\n")
	for i := 0; i < maxNotesLines*2; i++ {
		b.WriteString("- change " + strconv.Itoa(i) + "\n")
	}
	rendered := ParseNotes(b.String()).Render("1.1.0")

	assert.Contains(t, rendered, "change 0")
	assert.NotContains(t, rendered, "change "+strconv.Itoa(maxNotesLines*2-1))
	assert.Contains(t, rendered, "the changelog has all of them")
	// The heading, a blank line, the capped body and the tail: bounded, whatever
	// the release did.
	assert.LessOrEqual(t, strings.Count(rendered, "\n"), maxNotesLines+4)
}

// TestNotesRenderLaysOutTheSections: what the user actually reads. Sections at
// one indent, their bullets at the next, one blank line between them, so a
// release with three sections is scannable rather than a paragraph.
func TestNotesRenderLaysOutTheSections(t *testing.T) {
	notes := ParseNotes("### Features\n\n- streaming\nwith a body line\n\n### Fixes\n\n- a crash\n")

	assert.Equal(t, strings.Join([]string{
		"what changed in 1.1.0",
		"",
		"  Features",
		"    - streaming",
		"      with a body line",
		"",
		"  Fixes",
		"    - a crash",
		"",
	}, "\n"), notes.Render("1.1.0"))
}

// TestNotesRenderIsEmptyWhenThereIsNothingToSay: the caller prints the result
// unconditionally, so "nothing" has to be the empty string rather than a
// heading with a blank under it.
func TestNotesRenderIsEmptyWhenThereIsNothingToSay(t *testing.T) {
	assert.Empty(t, ParseNotes("").Render("1.1.0"))
	assert.Empty(t, Notes{}.Render("1.1.0"))
	assert.Zero(t, Notes{}.Items())
}
