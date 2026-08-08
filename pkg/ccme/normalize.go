package ccme

import "strings"

// utf8BOM is stripped from the front of a message before parsing (§4.1).
// It is spelled as an escape so the source file itself contains no BOM.
const utf8BOM = "\uFEFF"

// Normalize applies the input normalisation of §4.1 to a cleaned commit
// message, i.e. the output of `git log --format=%B`:
//
//  1. strip a leading UTF-8 BOM;
//  2. normalise CRLF and CR line terminators to LF;
//  3. strip trailing spaces and tabs from the end of each line, preserving
//     leading whitespace, which is significant for footer continuations;
//  4. strip trailing blank lines from the end of the message.
//
// Nothing else is altered. Normalize is idempotent, and returns the input
// string unchanged — with no allocation — when it is already normalised, which
// is the common case for messages read straight from git.
func Normalize(message string) string {
	if !needsNormalizing(message) {
		return message
	}
	return normalizeSlow(message)
}

// needsNormalizing reports whether normalizeSlow would change the message,
// letting the caller skip the rewrite entirely.
//
// It is written around strings.IndexByte rather than a byte-at-a-time loop:
// this runs over every message in a history, most of which need no work, so
// the scan itself is the cost that matters.
func needsNormalizing(m string) bool {
	if m == "" {
		return false
	}
	if strings.HasPrefix(m, utf8BOM) {
		return true
	}
	// A trailing newline means trailing blank lines; a trailing space or tab
	// means the final line needs trimming.
	switch m[len(m)-1] {
	case '\n', '\r', ' ', '\t':
		return true
	}
	if strings.IndexByte(m, '\r') >= 0 {
		return true
	}
	// Any remaining work is a line ending in a space or tab, which is exactly
	// a space or tab immediately before an LF.
	for i := 0; ; {
		j := strings.IndexByte(m[i:], '\n')
		if j < 0 {
			return false
		}
		if k := i + j; k > 0 {
			if prev := m[k-1]; prev == ' ' || prev == '\t' {
				return true
			}
			i = k + 1
		} else {
			i = 1
		}
	}
}

// normalizeSlow rewrites a message that needs it, in one pass and one
// allocation. Blank lines are buffered rather than written, so the trailing
// ones are dropped simply by never being emitted.
func normalizeSlow(m string) string {
	// Every leading BOM goes, not just the first. §4.1 says "a leading UTF-8
	// BOM", and after removing one, a second is again a leading BOM — reading
	// the rule any other way makes Normalize non-idempotent: a doubled BOM
	// normalises to a single BOM and then to the empty string, so a Result
	// whose Message field is documented as normalised would not be. Found by
	// FuzzParse via the reparse invariant.
	for strings.HasPrefix(m, utf8BOM) {
		m = m[len(utf8BOM):]
	}

	var b strings.Builder
	b.Grow(len(m))

	pending := 0 // line terminators seen but not yet emitted
	first := true

	for i := 0; ; {
		j := i
		for j < len(m) && m[j] != '\n' && m[j] != '\r' {
			j++
		}
		line := m[i:j]
		for len(line) > 0 {
			if c := line[len(line)-1]; c == ' ' || c == '\t' {
				line = line[:len(line)-1]
				continue
			}
			break
		}
		if first {
			first = false
		} else {
			pending++
		}
		if line != "" {
			for ; pending > 0; pending-- {
				b.WriteByte('\n')
			}
			b.WriteString(line)
		}

		if j >= len(m) {
			break
		}
		if m[j] == '\r' && j+1 < len(m) && m[j+1] == '\n' {
			i = j + 2
		} else {
			i = j + 1
		}
	}
	return b.String()
}

// splitLines splits a normalised message on LF. Every returned line is a
// substring of s, so no line content is copied.
func splitLines(s string) []string {
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(lines, s)
		}
		lines = append(lines, s[:i])
		s = s[i+1:]
	}
}

// splitUnits implements §20.2. A separator line is compared for byte equality
// against the configured string — no pattern matching, so "----" is not a
// separator and a Markdown rule in a body does not truncate a unit.
//
// A line consisting of a backslash followed by the separator is escaped: it
// becomes body text with the backslash removed. This is the only escape in the
// grammar (§4.2).
//
// The returned unit sources share both the line slice and the message string;
// nothing is copied unless a unit contains an escaped separator, which is the
// one case where a unit's text is not a contiguous substring of the message.
// escapedSep is the separator with its backslash escape prefix, precomputed on
// the Parser so the hot path does not rebuild the string on every message.
func splitUnits(msg string, lines []string, separator, escapedSep string) ([]unitSource, []Diagnostic) {
	var (
		out       []unitSource
		diags     []Diagnostic
		start     int // index of the current unit's first line
		startOff  int // byte offset of lines[start] within msg
		off       int // byte offset of lines[i] within msg
		hasEscape bool
	)

	flush := func(end int) {
		a, b, aOff := start, end, startOff
		for a < b && lines[a] == "" {
			aOff += len(lines[a]) + 1
			a++
		}
		for b > a && lines[b-1] == "" {
			b--
		}
		if a >= b {
			// A message ending in a separator leaves an empty region that
			// begins one line past the end, so the report is clamped to the
			// separator that produced it rather than to a line that does not
			// exist. Found by FuzzParse.
			line := start + 1
			if line > len(lines) {
				line = len(lines)
			}
			diags = append(diags, warn(CodeW001,
				Position{Line: line, Column: 1}, "empty unit discarded"))
			return
		}
		src := unitSource{msg: msg, lines: lines[a:b], offset: aOff, startLine: a + 1}
		if hasEscape {
			unescaped := make([]string, b-a)
			copy(unescaped, lines[a:b])
			for i, l := range unescaped {
				if l == escapedSep {
					unescaped[i] = l[1:]
				}
			}
			src.lines = unescaped
			src.offset = offsetDetached
		}
		out = append(out, src)
	}

	for i, l := range lines {
		if l == separator {
			flush(i)
			start = i + 1
			startOff = off + len(l) + 1
			hasEscape = false
		} else if l == escapedSep {
			hasEscape = true
		}
		off += len(l) + 1
	}
	flush(len(lines))

	return out, diags
}
