package writer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// The TOML manifests are spliced line by line rather than re-encoded:
// go-toml's Marshal drops comments and normalises layout, and its offset-aware
// parser lives in an "unstable" package whose own documentation disclaims
// backward compatibility. A version catalog is a flat table of single-line
// entries, so a per-line splice (the shape the requirements writer already
// uses) preserves every other byte by construction. Anything that does not fit
// on one line is declined rather than guessed at.

// verifyTOML is the TOML formats' proof that a rewrite still parses.
func verifyTOML(out []byte) error {
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return fmt.Errorf("rewrite produced invalid TOML: %w", err)
	}
	return nil
}

// tomlLineState tracks multiline string bodies across physical lines.
type tomlLineState struct{ multiline byte }

// maskStringContents replaces multiline string bytes with spaces so apparent
// declarations inside them are ignored while all source offsets stay intact.
func (s *tomlLineState) maskStringContents(raw string) string {
	start := 0
	var masked []byte
	if s.multiline != 0 {
		close := findTOMLTripleClose(raw, 0, s.multiline)
		if close < 0 {
			return ""
		}
		s.multiline = 0
		masked = []byte(raw)
		for i := 0; i < close+3; i++ {
			masked[i] = ' '
		}
		start = close + 3
	}
	// Most manifest lines cannot open a multiline string. Keep their existing
	// single-line scanner path and pay for the extra state only when a triple
	// delimiter is actually present.
	if masked == nil && !strings.Contains(raw, `"""`) && !strings.Contains(raw, "'''") {
		return stripTOMLComment(raw)
	}
	var quoted byte
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if quoted != 0 {
			if quoted == '"' && c == '\\' {
				i++
			} else if c == quoted {
				quoted = 0
			}
			continue
		}
		if c == '#' {
			if masked != nil {
				return string(masked[:i])
			}
			return raw[:i]
		}
		if c != '"' && c != '\'' {
			continue
		}
		if i+2 < len(raw) && raw[i+1] == c && raw[i+2] == c {
			if masked == nil {
				masked = []byte(raw)
			}
			close := findTOMLTripleClose(raw, i+3, c)
			end := len(raw)
			if close < 0 {
				s.multiline = c
			} else {
				end = close + 3
			}
			for j := i; j < end; j++ {
				masked[j] = ' '
			}
			if close < 0 {
				return string(masked)
			}
			i = end - 1
			continue
		}
		quoted = c
	}
	if masked != nil {
		return string(masked)
	}
	return raw
}

func findTOMLTripleClose(raw string, from int, quote byte) int {
	for i := from; i < len(raw); {
		if quote == '"' && raw[i] == '\\' {
			i += 2
			continue
		}
		if raw[i] != quote {
			i++
			continue
		}
		end := i
		for end < len(raw) && raw[end] == quote {
			end++
		}
		if end-i >= 3 {
			// TOML uses the last three of a four- or five-quote run as
			// the delimiter; the preceding quotes belong to the value.
			return end - 3
		}
		i = end
	}
	return -1
}

// stripTOMLComment cuts a trailing comment, ignoring '#' inside strings. It
// only ever truncates, so offsets into the result stay valid in the original.
func stripTOMLComment(line string) string {
	basic, literal := false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case basic:
			if c == '\\' {
				i++
			} else if c == '"' {
				basic = false
			}
		case literal:
			if c == '\'' {
				literal = false
			}
		case c == '"':
			basic = true
		case c == '\'':
			literal = true
		case c == '#':
			return line[:i]
		}
	}
	return line
}

// tomlKeyValue splits one entry line at its top-level '=', returning the
// (unquoted) key and the offset just past the separator.
func tomlKeyValue(line string) (key string, afterEq int, ok bool) {
	basic, literal := false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case basic:
			if c == '\\' {
				i++
			} else if c == '"' {
				basic = false
			}
		case literal:
			if c == '\'' {
				literal = false
			}
		case c == '"':
			basic = true
		case c == '\'':
			literal = true
		case c == '=':
			key = strings.Trim(strings.TrimSpace(line[:i]), `"'`)
			return key, i + 1, key != ""
		}
	}
	return "", 0, false
}

// tomlQuotedSpan measures the string literal starting at or after from,
// returning the span its content occupies. A multi-line literal is refused:
// its content does not live on this line, so a per-line splice cannot reach it.
func tomlQuotedSpan(line string, from int) (start, end int, ok bool) {
	i := from
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return 0, 0, false
	}
	quote := line[i]
	if quote != '"' && quote != '\'' {
		return 0, 0, false
	}
	if strings.HasPrefix(line[i:], `"""`) || strings.HasPrefix(line[i:], "'''") {
		return 0, 0, false
	}
	i++
	start = i
	for i < len(line) {
		if quote == '"' && line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == quote {
			return start, i, true
		}
		i++
	}
	return 0, 0, false
}

// tomlInlineValueSpan measures a top-level string member of an inline table.
// Quoted values and nested maps can contain text resembling another member;
// only the actual key at the outer brace depth may be rewritten.
func tomlInlineValueSpan(line string, from int, key string) (start, end int, ok bool) {
	if from >= len(line) || line[from] != '{' {
		return 0, 0, false
	}
	var state tomlLineState
	structure := state.maskStringContents(line)
	brace, array, memberStart := 0, 0, from+1
	var quote byte
	for i := from; i < len(structure); i++ {
		c := structure[i]
		if quote != 0 {
			if quote == '"' && c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '{':
			brace++
		case '}':
			brace--
			if brace == 0 {
				return 0, 0, false
			}
		case '[':
			array++
		case ']':
			array--
		case ',':
			if brace == 1 && array == 0 {
				memberStart = i + 1
			}
		case '=':
			if brace == 1 && array == 0 {
				member := strings.TrimSpace(structure[memberStart:i])
				if member == key {
					return tomlQuotedSpan(line, i+1)
				}
				parts, valid := parseTOMLDottedKeyParts(member)
				if valid && len(parts) == 1 && parts[0] == key {
					return tomlQuotedSpan(line, i+1)
				}
			}
		}
	}
	return 0, 0, false
}

// parseTOMLDottedKeyParts decodes the authored segments of a TOML key or header.
// Quoted dots remain inside one segment, unlike unquoted path separators.
func parseTOMLDottedKeyParts(raw string) ([]string, bool) {
	var parts []string
	for raw = strings.TrimSpace(raw); raw != ""; {
		var part string
		switch raw[0] {
		case '"':
			i := 1
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					break
				}
				i++
			}
			if i >= len(raw) {
				return nil, false
			}
			var err error
			part, err = strconv.Unquote(raw[:i+1])
			if err != nil {
				return nil, false
			}
			raw = raw[i+1:]
		case '\'':
			i := strings.IndexByte(raw[1:], '\'')
			if i < 0 {
				return nil, false
			}
			part = raw[1 : i+1]
			raw = raw[i+2:]
		default:
			i := strings.IndexByte(raw, '.')
			if i < 0 {
				i = len(raw)
			}
			part = strings.TrimSpace(raw[:i])
			raw = raw[i:]
		}
		if part == "" {
			return nil, false
		}
		parts = append(parts, part)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return parts, true
		}
		if raw[0] != '.' {
			return nil, false
		}
		raw = strings.TrimSpace(raw[1:])
	}
	return nil, false
}

// catalogEntryValueSpan measures a plain string value assigned to key inside
// the named table. It is the sub-table lookup: where the index finds a key on
// an entry line, this finds one under a header of its own.
func catalogEntryValueSpan(index tomlIndex, lines []string, table, key string) (idx, start, end int, ok bool) {
	idx, afterEq, ok := index.entry(table, key)
	if !ok {
		return 0, 0, 0, false
	}
	start, end, ok = tomlQuotedSpan(stripTOMLComment(lines[idx]), afterEq)
	return idx, start, end, ok
}

// tomlEntry is where one key inside one table sits.
type tomlEntry struct {
	line    int
	afterEq int
}

// tomlIndex maps table and key onto the line declaring it. Building it once
// per rewrite turns what was a scan of the whole file for every edit into one
// scan plus a map lookup each, which matters on a version catalog with
// hundreds of entries and as many edits.
type tomlIndex map[string]tomlEntry

// buildTOMLIndex walks the lines once, recording the first declaration of each
// key. The first wins, matching what a repeated scan from the top would find.
func buildTOMLIndex(lines []string) tomlIndex {
	index := make(tomlIndex, len(lines))
	table := ""
	var state tomlLineState
	for i, raw := range lines {
		body := state.maskStringContents(raw)
		if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "[") {
			table = strings.TrimSpace(strings.Trim(trimmed, "[]"))
			continue
		}
		key, eq, ok := tomlKeyValue(body)
		if !ok {
			continue
		}
		id := table + "\x00" + key
		if _, taken := index[id]; !taken {
			index[id] = tomlEntry{line: i, afterEq: eq}
		}
	}
	return index
}

// entry looks one key up inside one table.
func (t tomlIndex) entry(table, key string) (line, afterEq int, ok bool) {
	e, ok := t[table+"\x00"+key]
	return e.line, e.afterEq, ok
}
