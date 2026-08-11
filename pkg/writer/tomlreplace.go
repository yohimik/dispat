package writer

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Both TOML formats that can hold a redirect spell it the same way: a table
// keyed by package name whose value is an inline table carrying a path.
//
//	[patch.crates-io]
//	core = { path = "../core" }
//
//	[tool.uv.sources]
//	core = { path = "../core" }
//
// So one implementation serves both, differing only in the table name.
//
// This is the first writer here that inserts rather than splices. Every other
// one replaces bytes inside a span that already exists, which cannot change a
// file's structure; adding a redirect can have to write a line, or a whole
// table, that was not there. The validation is stronger to match: the result
// has to parse, and the entry has to read back as the path that was asked for.

// tomlReplace applies path redirects to a package-keyed table.
func tomlReplace(path, table string, replacements []Replacement) (ReplaceResult, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return ReplaceResult{}, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(rep.bytes(), &doc); err != nil {
		return ReplaceResult{}, fmt.Errorf("%s: %w", path, err)
	}

	var (
		res     ReplaceResult
		lines   = rep.lines()
		changed bool
	)
	for _, r := range replacements {
		idx, start, end, found := tomlEntryValueSpan(lines, table, r.Name)
		switch {
		case r.Path == "" && !found:
			res.Missing = append(res.Missing, r)
		case r.Path == "":
			lines = tomlDropEntry(lines, table, idx)
			res.Applied = append(res.Applied, r)
			changed = true
		case found:
			want := tomlPathValue(r.Path)
			if lines[idx][start:end] == want {
				break // already pointing there
			}
			lines[idx] = lines[idx][:start] + want + lines[idx][end:]
			res.Applied = append(res.Applied, r)
			changed = true
		default:
			lines = tomlInsertEntry(lines, table, r.Name, r.Path)
			res.Applied = append(res.Applied, r)
			changed = true
		}
	}
	if changed {
		rep.setLines(lines)
	}
	return res, rep.commit(func(out []byte) error {
		return tomlVerifyReplacements(out, table, res.Applied)
	})
}

// tomlPathValue renders one redirect's value.
func tomlPathValue(path string) string {
	return `{ path = "` + path + `" }`
}

// tomlEntryValueSpan locates a table entry's value: everything after the '='
// up to any trailing comment, so a splice keeps both the key's spelling and
// whatever note sits at the end of the line.
func tomlEntryValueSpan(lines []string, table, key string) (idx, start, end int, ok bool) {
	index := buildTOMLIndex(lines)
	idx, afterEq, ok := index.entry(table, key)
	if !ok {
		return 0, 0, 0, false
	}
	body := stripTOMLComment(lines[idx])
	start = afterEq
	for start < len(body) && (body[start] == ' ' || body[start] == '\t') {
		start++
	}
	end = len(body)
	for end > start && (body[end-1] == ' ' || body[end-1] == '\t' || body[end-1] == '\r') {
		end--
	}
	if end <= start {
		return 0, 0, 0, false
	}
	return idx, start, end, true
}

// tomlTableBounds finds a table's header line and the line one past its last
// entry, which is where the next header starts or the file ends.
func tomlTableBounds(lines []string, table string) (header, end int, ok bool) {
	header = -1
	for i, raw := range lines {
		trimmed := strings.TrimSpace(stripTOMLComment(raw))
		if !strings.HasPrefix(trimmed, "[") {
			continue
		}
		name := strings.TrimSpace(strings.Trim(trimmed, "[]"))
		if header >= 0 {
			return header, i, true // the next header closes the one sought
		}
		if name == table {
			header = i
		}
	}
	if header < 0 {
		return 0, 0, false
	}
	return header, len(lines), true
}

// tomlInsertEntry writes a new redirect, creating the table when the file has
// none. A new entry joins the end of the table's existing run, so the file
// keeps whatever order its author chose.
func tomlInsertEntry(lines []string, table, key, path string) []string {
	entry := key + " = " + tomlPathValue(path)
	header, end, ok := tomlTableBounds(lines, table)
	if !ok {
		// No such table. Append it, with a blank line separating it from
		// whatever came before unless the file already ends in one.
		out := append([]string{}, lines...)
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		return append(out, "", "["+table+"]", entry, "")
	}
	// Step back over the blank lines that separate this table from the next.
	at := end
	for at > header+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, entry)
	return append(out, lines[at:]...)
}

// tomlDropEntry removes one entry, and the table header with it when that
// entry was the last thing the table held. A header left behind would be
// harmless to a parser and confusing to a reader.
func tomlDropEntry(lines []string, table string, idx int) []string {
	out := make([]string, 0, len(lines))
	out = append(out, lines[:idx]...)
	out = append(out, lines[idx+1:]...)

	header, end, ok := tomlTableBounds(out, table)
	if !ok {
		return out
	}
	for i := header + 1; i < end; i++ {
		if strings.TrimSpace(stripTOMLComment(out[i])) != "" {
			return out // something is still declared here
		}
	}
	// Drop the header and the blank run it leaves, plus one separating blank
	// line above it so the file does not grow a gap.
	stop := end
	for stop > header+1 && strings.TrimSpace(out[stop-1]) == "" {
		stop--
	}
	start := header
	if start > 0 && strings.TrimSpace(out[start-1]) == "" {
		start--
	}
	return append(out[:start], out[stop:]...)
}

// tomlVerifyReplacements re-reads the written bytes and checks every applied
// redirect reads back as the path it asked for. Insertion can change a file's
// structure, so proving it parses is not by itself enough.
func tomlVerifyReplacements(out []byte, table string, applied []Replacement) error {
	var doc map[string]any
	if err := toml.Unmarshal(out, &doc); err != nil {
		return fmt.Errorf("rewrite produced invalid TOML: %w", err)
	}
	entries, _ := tomlLookupTable(doc, table)
	for _, r := range applied {
		value, declared := entries[r.Name]
		if r.Path == "" {
			if declared {
				return fmt.Errorf("rewrite left %s still redirected", r.Name)
			}
			continue
		}
		inline, _ := value.(map[string]any)
		if got, _ := inline["path"].(string); got != r.Path {
			return fmt.Errorf("rewrite left %s pointing at %q, want %q", r.Name, got, r.Path)
		}
	}
	return nil
}

// tomlLookupTable walks a dotted table name through a decoded document.
func tomlLookupTable(doc map[string]any, table string) (map[string]any, bool) {
	cur := doc
	for _, part := range strings.Split(table, ".") {
		next, ok := cur[part].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}
