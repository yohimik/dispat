package selfupdate

// Reading a release's notes to somebody who just updated.
//
// A GitHub release body is markdown written for a web page: headings, bullets,
// fenced install commands, a rule and a row of links at the end. A terminal
// wants none of the punctuation and only the middle of it, so the body is
// parsed into the two shapes that carry the meaning — sections and the bullets
// under them — and rendered back as plain indented text.
//
// Nothing here can fail. A body that makes no sense yields no sections, and the
// command prints a link instead. An update must never turn on whether its own
// changelog parsed.

import (
	"strings"
	"unicode/utf8"
)

const (
	// maxNotesBody bounds what is parsed. A release body is a few kilobytes;
	// this is well past any of them and keeps a pathological one from being
	// walked line by line.
	maxNotesBody = 512 << 10
	// maxNotesLines bounds what is printed. Past this the reader is better
	// served by the changelog than by a wall of terminal output.
	maxNotesLines = 40
	// maxNotesLineLen bounds one of those lines. Without it a body that never
	// breaks a line would be one line as far as the count is concerned, and the
	// whole of it would reach the terminal.
	maxNotesLineLen = 200
	// notesIndent and itemIndent lay the rendered notes out under their
	// heading. Two levels, because that is all the structure there is.
	notesIndent = "  "
	itemIndent  = "    "
)

// Item is one bullet of a section, with whatever the release wrote under it.
type Item struct {
	// Text is the bullet itself, without its marker.
	Text string
	// Body is the lines that hung below the bullet, which is where a commit
	// body ends up when the changelog renders one.
	Body []string
}

// Section is one heading of a release body and the bullets beneath it. A
// section with an empty Title is the text before the first heading, which is
// where a release with nothing to group says so in a sentence.
type Section struct {
	Title string
	Items []Item
	// Text is the section's prose: lines that belong to no bullet.
	Text []string
}

// empty reports a section that would render nothing.
func (s Section) empty() bool { return len(s.Items) == 0 && len(s.Text) == 0 }

// Notes is a release body reduced to what is worth reading in a terminal.
type Notes struct {
	Sections []Section
	// Truncated says the body was longer than this package will parse, so what
	// is here is the beginning of it rather than all of it.
	Truncated bool
}

// Empty reports whether there is anything to print.
func (n Notes) Empty() bool { return len(n.Sections) == 0 }

// ParseNotes reduces a GitHub release body to its notes.
//
// Three rules do the work. Fenced code is dropped, which is what keeps a
// release's install commands out of the summary. A horizontal rule ends the
// notes, which is how the configured footer marks where the notes stop and the
// links begin. Everything else is a heading, a bullet, or prose belonging to
// whichever of those came last.
func ParseNotes(body string) Notes {
	var notes Notes
	if len(body) > maxNotesBody {
		body = body[:maxNotesBody]
		notes.Truncated = true
		// Back up to the last complete line. Cutting at a byte would leave a
		// half written bullet to be printed as though the release had meant
		// it, and could split a rune down the middle on the way.
		if nl := strings.LastIndexByte(body, '\n'); nl >= 0 {
			body = body[:nl]
		}
	}
	// GitHub stores what the API was given, and a body authored on Windows or
	// posted through a form arrives with CRLF endings that would otherwise
	// leave a carriage return on the end of every line printed.
	body = strings.ReplaceAll(body, "\r\n", "\n")

	var current Section
	var fence string // the marker that opened the block being skipped
	blank := true    // whether the previous line was blank, for the rule below

	push := func() {
		if !current.empty() {
			notes.Sections = append(notes.Sections, current)
		}
		current = Section{}
	}

	for _, raw := range strings.Split(body, "\n") {
		line := sanitise(raw)
		trimmed := strings.TrimSpace(line)

		// Inside a fence nothing is markup, including a line that would
		// otherwise close a different fence or open a section.
		if fence != "" {
			if isFence(trimmed) == fence {
				fence = ""
				// A fenced block is a block of its own, so what comes after it
				// starts fresh: a rule on the next line is a rule, whatever the
				// line before the fence happened to be.
				blank = true
			}
			continue
		}
		if marker := isFence(trimmed); marker != "" {
			fence = marker
			continue
		}
		// A rule ends the notes. It counts only after a blank line, because
		// "Title" over "---" is a heading in markdown rather than a rule, and
		// cutting there would drop the notes at their first heading.
		if blank && isRule(trimmed) {
			break
		}
		if trimmed == "" {
			blank = true
			continue
		}
		blank = false

		if title, ok := heading(trimmed); ok {
			push()
			current.Title = title
			continue
		}
		if text, ok := bullet(trimmed); ok {
			current.Items = append(current.Items, Item{Text: text})
			continue
		}
		// Prose. It belongs to the bullet above it when there is one, which is
		// how a commit body stays with the change it describes, and to the
		// section otherwise. Before the first heading it opens the untitled
		// section that carries a "No changes" line.
		if n := len(current.Items); n > 0 {
			current.Items[n-1].Body = append(current.Items[n-1].Body, trimmed)
			continue
		}
		current.Text = append(current.Text, trimmed)
	}
	push()
	return notes
}

// Render lays the notes out under a line naming the version they belong to.
// The result ends in a newline when there is one, and is empty when there is
// nothing to say, so a caller can print it unconditionally.
func (n Notes) Render(version string) string {
	if n.Empty() {
		return ""
	}
	lines := make([]string, 0, maxNotesLines+1)
	over := false
	add := func(s string) {
		if len(lines) >= maxNotesLines {
			over = true
			return
		}
		if clipped, cut := clip(s, maxNotesLineLen); cut {
			s, over = clipped, true
		}
		lines = append(lines, s)
	}
	for i, sec := range n.Sections {
		if i > 0 {
			add("")
		}
		if sec.Title != "" {
			add(notesIndent + sec.Title)
		}
		for _, text := range sec.Text {
			add(notesIndent + text)
		}
		for _, item := range sec.Items {
			add(itemIndent + "- " + item.Text)
			for _, text := range item.Body {
				add(itemIndent + "  " + text)
			}
		}
	}

	var b strings.Builder
	b.WriteString("what changed in " + version + "\n\n")
	for _, line := range lines {
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteByte('\n')
	}
	if over || n.Truncated {
		b.WriteString(notesIndent + "... the notes go on, and the changelog has all of them\n")
	}
	return b.String()
}

// clip shortens a line to at most n bytes, ending on a rune boundary so a
// multi-byte character is never cut in half, and reports whether it had to.
func clip(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimRight(s[:n], " ") + " ...", true
}

// heading reads an ATX heading, "#" through "######". The hashes have to be
// followed by a space: "#42" is a reference to an issue, not a heading.
func heading(line string) (string, bool) {
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 6 || hashes >= len(line) || line[hashes] != ' ' {
		return "", false
	}
	// A closing run of hashes is markdown's optional other half of the fence
	// and is not part of the title.
	title := strings.TrimSpace(strings.TrimRight(line[hashes+1:], "#"))
	if title == "" {
		return "", false
	}
	return title, true
}

// bullet reads a list item under any of markdown's three markers. The space
// after the marker is what separates "- item" from "-42", and what keeps the
// "---" rule from being read as an empty bullet.
//
// The line arrives with its whitespace already trimmed, so a marker followed by
// a space is always followed by something: there is no empty bullet to reject.
func bullet(line string) (string, bool) {
	if len(line) < 2 || line[1] != ' ' {
		return "", false
	}
	switch line[0] {
	case '-', '*', '+':
		return strings.TrimSpace(line[2:]), true
	}
	return "", false
}

// isFence reports the marker opening or closing a fenced block, "" for a line
// that is not one. Three or more of either character, which is markdown's rule,
// and the info string after them is ignored.
func isFence(line string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, marker) {
			return marker
		}
	}
	return ""
}

// isRule reports a thematic break: three or more of one character, nothing
// else on the line. A "- - -" spaced rule counts too, which is why the spaces
// come out before the run is measured.
func isRule(line string) bool {
	line = strings.ReplaceAll(line, " ", "")
	if len(line) < 3 {
		return false
	}
	switch line[0] {
	case '-', '*', '_':
	default:
		return false
	}
	return strings.Count(line, string(line[0])) == len(line)
}

// sanitise strips what a terminal would act on rather than print: the control
// characters, and the escape sequences they introduce.
//
// A release body is written by whoever publishes the release, so this is less a
// defence against an attacker than against a stray sequence colouring or moving
// the output a user is trying to read. Dropping the escape alone would leave
// its "[31m" tail behind as visible debris, so the whole sequence goes.
func sanitise(line string) string {
	if strings.IndexFunc(line, isControl) < 0 {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); {
		c := line[i]
		if c == escape {
			i = skipEscape(line, i)
			continue
		}
		if isControl(rune(c)) && c < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if !isControl(r) {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// escape is the byte that opens every sequence skipEscape walks past.
const escape = 0x1b

// skipEscape returns the index just past the escape sequence beginning at i.
//
// Two shapes matter. A control sequence ("ESC [") runs to a final byte in
// 0x40..0x7e, which is where a colour or a cursor move ends. An operating
// system command ("ESC ]") runs to a bell or another escape, which is how a
// window title is set. Anything else is a two byte sequence. A sequence the
// line never finishes takes the rest of the line, which is the safe reading:
// what follows it was never going to be printed as itself.
func skipEscape(line string, i int) int {
	i++ // the escape itself
	if i >= len(line) {
		return i
	}
	switch line[i] {
	case '[':
		for i++; i < len(line); i++ {
			if line[i] >= 0x40 && line[i] <= 0x7e {
				return i + 1
			}
		}
		return len(line)
	case ']':
		for i++; i < len(line); i++ {
			if line[i] == 0x07 || line[i] == escape {
				return i + 1
			}
		}
		return len(line)
	}
	return i + 1
}

// isControl reports the runes sanitise removes: the C0 set and DEL, minus the
// tab, which is ordinary whitespace inside a line.
func isControl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f
}

// Items counts the bullets across every section, which is what a log line
// saying how much was understood is about.
func (n Notes) Items() int {
	total := 0
	for _, sec := range n.Sections {
		total += len(sec.Items)
	}
	return total
}
