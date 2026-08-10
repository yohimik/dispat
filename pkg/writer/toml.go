package writer

import "strings"

// The TOML manifests are spliced line by line rather than re-encoded:
// go-toml's Marshal drops comments and normalises layout, and its offset-aware
// parser lives in an "unstable" package whose own documentation disclaims
// backward compatibility. A version catalog is a flat table of single-line
// entries, so a per-line splice (the shape the requirements writer already
// uses) preserves every other byte by construction. Anything that does not fit
// on one line is declined rather than guessed at.

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

// tomlInlineValueSpan measures the string literal assigned to key inside an
// inline table. The key must sit on a boundary and be followed by '=', so
// "version" never matches the "version" of a "version.ref" dotted key, which
// is exactly the distinction the catalog writer turns on.
func tomlInlineValueSpan(line string, from int, key string) (start, end int, ok bool) {
	for i := from; i < len(line); i++ {
		j := strings.Index(line[i:], key)
		if j < 0 {
			return 0, 0, false
		}
		i += j
		if i > 0 && !isTOMLKeyBoundary(line[i-1]) {
			continue
		}
		k := i + len(key)
		for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
			k++
		}
		if k >= len(line) || line[k] != '=' {
			continue
		}
		return tomlQuotedSpan(line, k+1)
	}
	return 0, 0, false
}

// isTOMLKeyBoundary reports a byte that may precede a key inside an inline
// table.
func isTOMLKeyBoundary(c byte) bool {
	return c == '{' || c == ',' || c == ' ' || c == '\t'
}

// catalogEntryValueSpan measures a plain string value assigned to key inside
// the named table. It is the sub-table lookup: where catalogEntryLine finds a
// key on an entry line, this finds one under a header of its own.
func catalogEntryValueSpan(lines []string, table, key string) (idx, start, end int, ok bool) {
	idx, afterEq, ok := catalogEntryLine(lines, table, key)
	if !ok {
		return 0, 0, 0, false
	}
	start, end, ok = tomlQuotedSpan(stripTOMLComment(lines[idx]), afterEq)
	return idx, start, end, ok
}
