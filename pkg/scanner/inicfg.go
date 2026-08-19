package scanner

import "strings"

// The INI family six manifests share: Godot's project file, addon manifest and
// export presets, Unreal's two config files, and Defold's game.project. They
// differ on two points only, so one grammar with a dialect value carries all
// six rather than each format hand-rolling its own line reader.
//
// What the grammar does not do is nest. Godot 3 writes multi-line array and
// dictionary literals into project.godot, and this reader walks straight past
// their inner lines rather than tracking depth. It is safe because the parsers
// read named keys out of named sections, so a stray inner line has to collide
// with both to be noticed, and because the writer refuses to splice a value
// that opens a container at all.

// iniDialect describes one INI flavour.
type iniDialect struct {
	// quoted reports that string values are written as quoted literals, the
	// way Godot spells them. Unreal and Defold leave them bare.
	quoted bool
	// comment is the token that begins a trailing comment, empty where the
	// format has none. Defold has none, and must not be given one: its
	// dependency keys are spelled dependencies#0, so treating '#' as a
	// comment would truncate the key it belongs to.
	comment string
}

// The three dialects in use.
var (
	godotDialect  = iniDialect{quoted: true, comment: ";"}
	unrealDialect = iniDialect{quoted: false, comment: ";"}
	defoldDialect = iniDialect{quoted: false, comment: ""}
)

// iniStripComment cuts a trailing comment, ignoring a comment token inside a
// quoted literal so `config/name="a;b"` keeps its name. It only ever
// truncates, so an offset into the result is still an offset into the line.
func iniStripComment(line string, d iniDialect) string {
	if d.comment == "" {
		return line
	}
	quoted := false
	for i := 0; i < len(line); i++ {
		switch {
		case quoted && line[i] == '\\':
			i++ // an escaped byte inside a literal ends nothing
		case line[i] == '"':
			quoted = !quoted
		case !quoted && strings.HasPrefix(line[i:], d.comment):
			return line[:i]
		}
	}
	return line
}

// iniSection reports the section a header line opens, without its brackets:
// "application", "preset.0.options", "/Script/EngineSettings.GeneralProjectSettings".
func iniSection(line string) (string, bool) {
	s := strings.TrimSpace(line)
	// Three bytes at least: a header with no name inside it names nothing.
	if len(s) < 3 || s[0] != '[' || s[len(s)-1] != ']' {
		return "", false
	}
	return s[1 : len(s)-1], true
}

// iniEntry splits an assignment into its key and the raw text of its value.
// Keys carry '/', '.' and '#' as ordinary bytes, which is how Godot spells
// config/version, Unreal spells its script paths and Defold spells
// dependencies#0.
//
// An Unreal array operation (+Key=, -Key=, .Key=, !Key=) keeps its prefix, so
// it reads as the distinct key it is and never stands in for a plain
// assignment of the same name.
func iniEntry(line string) (key, value string, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(line[eq+1:]), true
}

// iniUnquote decodes a value that should be a string. In a quoted dialect that
// means a literal and nothing else, so an integer, a boolean or a
// PackedStringArray(...) reports false rather than being read as text. In a
// bare dialect every non-empty value is the string it looks like.
func iniUnquote(value string, d iniDialect) (string, bool) {
	if !d.quoted {
		return value, value != ""
	}
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	inner := value[1 : len(value)-1]
	if !strings.ContainsRune(inner, '\\') {
		return inner, true
	}
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String(), true
}

// iniScan walks the file, calling visit for every assignment with the section
// it sits in. A repeated section is visited once per occurrence and never
// merged, which is what lets an export_presets.cfg carrying one
// [preset.N.options] per platform be read as the several presets it is.
// Entries above the first header get the empty section, where project.godot
// keeps config_version.
//
// The strings handed to visit share the file's memory. A caller keeping one
// past the walk clones it, or a ten-byte version number holds a two-megabyte
// Unity settings file alive for as long as the manifest does.
func iniScan(data []byte, d iniDialect, visit func(section, key, value string)) {
	section := ""
	for text := string(data); text != ""; {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		line = iniStripComment(strings.TrimSuffix(line, "\r"), d)
		if s, ok := iniSection(line); ok {
			section = s
			continue
		}
		if key, value, ok := iniEntry(line); ok {
			visit(section, key, value)
		}
	}
}

// iniString reads one key of one section as a string, cloned so it does not
// pin the file it came from. The last occurrence wins, matching how every
// engine's own loader reads these files.
func iniString(data []byte, d iniDialect, section, key string) string {
	var out string
	iniScan(data, d, func(s, k, v string) {
		if s != section || k != key {
			return
		}
		if text, ok := iniUnquote(v, d); ok {
			out = strings.Clone(text)
		}
	})
	return out
}
