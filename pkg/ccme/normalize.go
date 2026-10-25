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
// Nothing else is altered. Normalize is idempotent. The two ends are trimmed by
// slicing, so a message whose lines are already clean, which is every message
// read straight from git, comes back as a substring of the input with no
// allocation.
func Normalize(message string) string {
	trimmed := trimMessageEdges(message)
	if !isLineRewriteNeeded(trimmed) {
		return trimmed
	}
	return normalizeSlow(trimmed)
}

// trimMessageEdges strips every leading BOM and the trailing whitespace and
// blank lines. normalizeSlow drops a tail of spaces, tabs and line terminators
// in full, so trimming it first changes nothing that normalizeSlow returns.
func trimMessageEdges(m string) string {
	for strings.HasPrefix(m, utf8BOM) {
		m = m[len(utf8BOM):]
	}
	end := len(m)
	for end > 0 && isTrailingWhitespace(m[end-1]) {
		end--
	}
	return m[:end]
}

func isTrailingWhitespace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// isLineRewriteNeeded reports whether normalizeSlow would change a message
// whose edges trimMessageEdges has already trimmed: what is left to rewrite is
// a CR, or a line ending in a space or tab.
//
// It is written around strings.IndexByte rather than a byte-at-a-time loop:
// this runs over every message in a history, most of which need no work, so
// the scan itself is the cost that matters.
func isLineRewriteNeeded(m string) bool {
	if strings.IndexByte(m, '\r') >= 0 {
		return true
	}
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
	// BOM", and after removing one, a second is again a leading BOM; reading
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

// splitLines splits a normalised message on LF into lines, whose spare
// capacity is used when it suffices. Every returned line is a substring of s,
// so no line content is copied.
func splitLines(s string, lines []string) []string {
	if n := strings.Count(s, "\n") + 1; n > cap(lines) {
		lines = make([]string, 0, n)
	}
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return append(lines, s)
		}
		lines = append(lines, s[:i])
		s = s[i+1:]
	}
}

// splitUnits implements §20.2, appending each unit's source to sources. A
// separator line is compared for byte equality against the configured string;
// no pattern matching happens, so "----" is not a separator and a Markdown rule
// in a body does not truncate a unit.
//
// A line consisting of a backslash followed by the separator is escaped: it
// becomes body text with the backslash removed. This is the only escape in the
// grammar (§4.2).
//
// A unit's text is a substring of msg; nothing is copied unless the unit
// contains an escaped separator, which is the one case where its text is not
// a contiguous substring of the message.
func (p *Parser) splitUnits(msg string, lines []string, sources []unitSource) ([]unitSource, []Diagnostic) {
	var (
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
		src := unitSource{index: len(sources), raw: msg[aOff : aOff+lineSpan(lines[a:b])], first: a, end: b}
		if hasEscape {
			src.unescaped = unescapeSeparators(lines[a:b], p.escapedSep)
			src.raw = strings.Join(src.unescaped, "\n")
		}
		sources = append(sources, src)
	}

	for i, l := range lines {
		if l == p.cfg.Separator {
			flush(i)
			start = i + 1
			startOff = off + len(l) + 1
			hasEscape = false
		} else if l == p.escapedSep {
			hasEscape = true
		}
		off += len(l) + 1
	}
	flush(len(lines))

	return sources, diags
}

// unescapeSeparators copies lines with every escaped separator line unescaped.
func unescapeSeparators(lines []string, escapedSep string) []string {
	unescaped := make([]string, len(lines))
	copy(unescaped, lines)
	for i, l := range unescaped {
		if l == escapedSep {
			unescaped[i] = l[1:]
		}
	}
	return unescaped
}
