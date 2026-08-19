package writer

import (
	"fmt"
	"strings"
)

// The writing half of the INI grammar six engine manifests share. It is a
// parallel of pkg/scanner/inicfg.go rather than an import of it, the way
// ruby.go is: the reader wants decoded values, and this side wants byte
// offsets on a line it is about to splice.

// iniDialect describes one INI flavour. quoted reports that string values are
// written as quoted literals, the way Godot spells them; comment is the token
// that begins a trailing comment, empty where the format has none.
type iniDialect struct {
	quoted  bool
	comment string
}

// The three dialects in use, spelled the same as the reader's.
var (
	godotDialect  = iniDialect{quoted: true, comment: ";"}
	unrealDialect = iniDialect{quoted: false, comment: ";"}
	defoldDialect = iniDialect{quoted: false, comment: ""}
)

// iniStripComment cuts a trailing comment, ignoring a comment token inside a
// quoted literal. It only truncates, so an offset into the result is still an
// offset into the line, which is what lets the span functions measure against
// the masked text and splice against the raw.
func iniStripComment(line string, d iniDialect) string {
	if d.comment == "" {
		return line
	}
	quoted := false
	for i := 0; i < len(line); i++ {
		switch {
		case quoted && line[i] == '\\':
			i++
		case line[i] == '"':
			quoted = !quoted
		case !quoted && strings.HasPrefix(line[i:], d.comment):
			return line[:i]
		}
	}
	return line
}

// iniSection reports the section a header line opens, without its brackets.
func iniSection(line string) (string, bool) {
	s := strings.TrimSpace(line)
	// Three bytes at least: a header with no name inside it names nothing.
	if len(s) < 3 || s[0] != '[' || s[len(s)-1] != ']' {
		return "", false
	}
	return s[1 : len(s)-1], true
}

// iniKey reports the key a line assigns, or false where it assigns nothing.
func iniKey(line string) (string, bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", false
	}
	key := strings.TrimSpace(line[:eq])
	return key, key != ""
}

// iniValueSpan measures the value the named key assigns on this line: the span
// of a quoted literal's content with the quotes excluded, or of a bare literal
// whole. quoted reports which of the two it found, so a splice can put the
// text back in the shape it came out of.
//
// A bare value carrying a bracket anywhere is refused. Godot writes
// PackedStringArray("4.3") for its engine features, which opens on a letter
// rather than on the bracket, and it spreads array and dictionary literals
// across several lines. A splice inside any of those leaves a file the editor
// cannot load, and no version or counter this package writes has a bracket in
// it, so refusing costs nothing worth having.
func iniValueSpan(line, key string, d iniDialect) (start, end int, quoted, ok bool) {
	masked := iniStripComment(line, d)
	eq := strings.IndexByte(masked, '=')
	if eq < 0 || strings.TrimSpace(masked[:eq]) != key {
		return 0, 0, false, false
	}
	start = eq + 1
	for start < len(masked) && (masked[start] == ' ' || masked[start] == '\t') {
		start++
	}
	end = len(masked)
	for end > start && (masked[end-1] == ' ' || masked[end-1] == '\t' || masked[end-1] == '\r') {
		end--
	}
	if end <= start {
		return 0, 0, false, false
	}
	if masked[start] == '"' {
		if end-start < 2 || masked[end-1] != '"' {
			return 0, 0, false, false
		}
		return start + 1, end - 1, true, true
	}
	if strings.ContainsAny(masked[start:end], "([{") {
		return 0, 0, false, false
	}
	return start, end, false, true
}

// iniSplice rewrites, in every section want accepts, the value of every key
// set claims. set is handed the key and the value as it reads now, and returns
// the text to write and whether the key is one it handles at all; returning
// the same text writes nothing.
//
// found counts the declarations set claimed, changed how many of them moved.
// Every occurrence of a repeated section is visited, because an
// export_presets.cfg carries one [preset.N.options] per platform and a counter
// written into only the first ships a stale one on every other store.
func iniSplice(lines []string, d iniDialect, want func(section string) bool,
	set func(key, current string, quoted bool) (string, bool)) (found, changed int) {
	section := ""
	for i, raw := range lines {
		masked := iniStripComment(raw, d)
		if s, ok := iniSection(masked); ok {
			section = s
			continue
		}
		if !want(section) {
			continue
		}
		key, ok := iniKey(masked)
		if !ok {
			continue
		}
		start, end, quoted, ok := iniValueSpan(raw, key, d)
		if !ok {
			continue
		}
		current := raw[start:end]
		next, mine := set(key, current, quoted)
		if !mine {
			continue
		}
		found++
		if next == current {
			continue
		}
		lines[i] = raw[:start] + next + raw[end:]
		changed++
	}
	return found, changed
}

// iniRefuse rejects a value that could not survive as one, before the file is
// opened: a quote or a backslash would end or escape the literal it lands in,
// a bracket could be read as a section header on the next parse, the dialect's
// comment token would swallow the rest of the line, and a newline would split
// one entry into two.
func iniRefuse(path, value string, d iniDialect) error {
	bad := "\"\\[]\n\r"
	if d.comment != "" {
		bad += d.comment
	}
	if strings.ContainsAny(value, bad) {
		return fmt.Errorf("%s: refusing to write %q into a config file: it could not survive as one value", path, value)
	}
	return nil
}

// iniShape is the structure a verify compares across a rewrite: the section
// headers in the order they appear, and how many assignments the file makes.
func iniShape(data []byte, d iniDialect) (sections []string, entries int) {
	for text := string(data); text != ""; {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		line = iniStripComment(strings.TrimSuffix(line, "\r"), d)
		if s, ok := iniSection(line); ok {
			sections = append(sections, s)
			continue
		}
		if _, ok := iniKey(line); ok {
			entries++
		}
	}
	return sections, entries
}

// iniVerify is the re-read proof these formats stand on, modelled on
// pbxVerify. There is no cheap grammar to check an INI file against, so the
// invariants stand in for one: the section headers are unchanged in count and
// in order, the file makes the same number of assignments, and every
// declaration the write claimed now reads the value it meant to write, as many
// of them as the write found.
func iniVerify(before, after []byte, d iniDialect, want func(string) bool,
	mine func(key string) bool, value string, found int) error {
	wasSections, wasEntries := iniShape(before, d)
	nowSections, nowEntries := iniShape(after, d)
	if len(wasSections) != len(nowSections) {
		return fmt.Errorf("rewrite changed the section count from %d to %d", len(wasSections), len(nowSections))
	}
	for i := range wasSections {
		if wasSections[i] != nowSections[i] {
			return fmt.Errorf("rewrite renamed section %q to %q", wasSections[i], nowSections[i])
		}
	}
	if wasEntries != nowEntries {
		return fmt.Errorf("rewrite changed the entry count from %d to %d", wasEntries, nowEntries)
	}
	seen := 0
	section := ""
	for _, raw := range strings.Split(string(after), "\n") {
		masked := iniStripComment(raw, d)
		if s, ok := iniSection(masked); ok {
			section = s
			continue
		}
		if !want(section) {
			continue
		}
		key, ok := iniKey(masked)
		if !ok || !mine(key) {
			continue
		}
		start, end, _, ok := iniValueSpan(raw, key, d)
		if !ok {
			continue
		}
		seen++
		if raw[start:end] != value {
			return fmt.Errorf("rewrite left %q reading %q", key, raw[start:end])
		}
	}
	if seen != found {
		return fmt.Errorf("rewrite left %d of %d declarations of the edited keys", seen, found)
	}
	return nil
}
