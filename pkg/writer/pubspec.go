package writer

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// pubspecTables maps a kind onto the top-level block its declarations live in.
// dependency_overrides is not a kind of its own (the scanner folds an override
// onto the declaration it overrides) so it is not written here.
var pubspecTables = map[manifest.Kind]string{
	manifest.KindDependencies:    "dependencies",
	manifest.KindDevDependencies: "dev_dependencies",
}

// rewritePubspec edits a pubspec.yaml line by line: the package's own
// `version`, and the constraint of each dependency matching an edit. Only the
// scalar being changed is replaced, so indentation, comments, quoting and key
// order all survive.
//
// A dependency whose value is a block rather than a scalar (a path, git or sdk
// dependency spelled across several lines) has no constraint on its own line
// to replace and is reported missing. So is a dependency in a block this
// format has no kind for.
func rewritePubspec(path, version string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	type target struct{ block, name string }
	wanted := make(map[target]int, len(edits))
	for i, e := range edits {
		kind := e.Kind
		if kind == "dependencies" {
			kind = manifest.KindDependencies
		}
		block, ok := pubspecTables[kind]
		if !ok {
			continue
		}
		wanted[target{block, e.Name}] = i
	}

	// depth is the indent of the open block's own entries, fixed by its first
	// one. Anything deeper belongs to an entry rather than to the block, and
	// must not be matched: a block dependency spells its folder as a nested
	// "path:" key, and "path" is also an ordinary package name, so a writer
	// that ignored depth would rewrite the folder into a version.
	const noBlock = -1
	var (
		res     Result
		seen    = make(map[int]bool, len(edits))
		found   = make(map[int]bool, len(edits))
		lines   = sp.lines()
		changed bool
		block   string
		depth   = noBlock
	)
	for li, raw := range lines {
		line := stripYAMLComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		// A key at column zero opens a new top-level block; anything indented
		// belongs to the one already open.
		if line[0] != ' ' && line[0] != '\t' {
			key, valueStart, ok := yamlKey(line)
			if !ok {
				block, depth = "", noBlock
				continue
			}
			block, depth = key, noBlock
			if key != "version" || version == "" {
				continue
			}
			start, end, ok := yamlScalarSpan(line, valueStart)
			if !ok {
				continue
			}
			if line[start:end] == version {
				continue
			}
			if !isYAMLWritable(version) {
				return res, fmt.Errorf("%s: refusing to write %q into a YAML scalar", path, version)
			}
			res.VersionWritten = true
			lines[li] = raw[:start] + version + raw[end:]
			changed = true
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if depth == noBlock {
			depth = indent // the block's first entry sets the level
		}
		if indent != depth {
			continue // nested inside an entry, not an entry of the block
		}
		key, valueStart, ok := yamlKey(line)
		if !ok {
			continue
		}
		i, want := wanted[target{block, key}]
		if !want {
			continue
		}
		seen[i] = true
		start, end, ok := yamlScalarSpan(line, valueStart)
		if !ok {
			continue // declared as a block (path, git, sdk): no constraint here
		}
		found[i] = true
		if line[start:end] == edits[i].Range {
			continue // already the wanted text: no change, not missing
		}
		if !isYAMLWritable(edits[i].Range) {
			return res, fmt.Errorf("%s: refusing to write %q into a YAML scalar", path, edits[i].Range)
		}
		res.Applied = append(res.Applied, edits[i])
		lines[li] = raw[:start] + edits[i].Range + raw[end:]
		changed = true
	}
	for i, e := range edits {
		switch {
		case found[i]:
		case seen[i]:
			res.Skipped = append(res.Skipped, e)
		default:
			res.Missing = append(res.Missing, e)
		}
	}
	if changed {
		sp.setLines(lines)
	}
	return res, sp.commit(nil)
}

// stripYAMLComment cuts a trailing comment, ignoring '#' inside quotes and the
// '#' of a value that is not preceded by whitespace (a fragment in a URL). It
// only ever truncates, so offsets into the result stay valid in the original.
func stripYAMLComment(line string) string {
	single, double := false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case single:
			if c == '\'' {
				single = false
			}
		case double:
			if c == '\\' {
				i++
			} else if c == '"' {
				double = false
			}
		case c == '\'':
			single = true
		case c == '"':
			double = true
		case c == '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i]
			}
		}
	}
	return line
}

// yamlKey splits a mapping entry at its colon, returning the key and the
// offset just past the separator.
func yamlKey(line string) (key string, valueStart int, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", 0, false
	}
	key = strings.TrimSpace(line[:i])
	key = strings.Trim(key, `"'`)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", 0, false
	}
	return key, i + 1, true
}

// yamlScalarSpan measures the scalar a mapping entry assigns, excluding the
// quotes of a quoted one so a splice preserves the file's quoting style. An
// entry with no value on its line opens a nested block and reports nothing.
func yamlScalarSpan(line string, from int) (start, end int, ok bool) {
	i := from
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return 0, 0, false
	}
	if q := line[i]; q == '"' || q == '\'' {
		i++
		start = i
		for i < len(line) && line[i] != q {
			i++
		}
		if i >= len(line) {
			return 0, 0, false
		}
		return start, i, true
	}
	start = i
	end = len(line)
	for end > start && (line[end-1] == ' ' || line[end-1] == '\t' || line[end-1] == '\r') {
		end--
	}
	if end == start {
		return 0, 0, false
	}
	return start, end, true
}

// isYAMLWritable reports text that can stand as a bare or quoted scalar
// without changing the document's structure.
func isYAMLWritable(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\n\r\"'#:")
}

// pubspecOverrides is the block Dart redirects through. An entry names a
// package and nests the folder under it:
//
//	dependency_overrides:
//	  core:
//	    path: ../core
const pubspecOverrides = "dependency_overrides"

// linkPubspec points packages at local folders through
// dependency_overrides, which is pub's equivalent of a go.mod replace. The
// scanner already folds these onto the declarations they override, so a
// dependency showing a LocalPath is one of these.
//
// Indentation follows the file. A pubspec written with four spaces keeps four,
// because the block's own entries decide the width rather than a constant here.
func linkPubspec(path string, links []Link) (LinkResult, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return LinkResult{}, err
	}
	var (
		res     LinkResult
		lines   = sp.lines()
		changed bool
	)
	for _, r := range links {
		entry, found := pubspecOverrideEntry(lines, r.Name)
		switch {
		case r.Path == "" && !found:
			res.Missing = append(res.Missing, r)
		case r.Path == "":
			lines = pubspecDropOverride(lines, entry)
			res.Applied = append(res.Applied, r)
			changed = true
		case found:
			if entry.path == r.Path {
				break // already pointing there
			}
			if entry.pathLine < 0 {
				// Declared as something other than a path (a git or hosted
				// override). Replacing the whole entry is the honest edit.
				lines = pubspecDropOverride(lines, entry)
				lines = pubspecInsertOverride(lines, r.Name, r.Path)
			} else {
				l := lines[entry.pathLine]
				lines[entry.pathLine] = l[:entry.pathStart] + r.Path + l[entry.pathEnd:]
			}
			res.Applied = append(res.Applied, r)
			changed = true
		default:
			lines = pubspecInsertOverride(lines, r.Name, r.Path)
			res.Applied = append(res.Applied, r)
			changed = true
		}
	}
	if changed {
		sp.setLines(lines)
	}
	return res, sp.commit(func(out []byte) error {
		return pubspecVerifyOverrides(out, res.Applied)
	})
}

// pubspecOverride is one entry of the overrides block: the line naming the
// package, the run of lines nested under it, and the path it declares.
type pubspecOverride struct {
	nameLine  int
	end       int // one past the entry's last nested line
	indent    int // the column the entry's own key sits at
	pathLine  int // -1 when the entry declares no path
	pathStart int
	pathEnd   int
	path      string
}

// pubspecOverrideEntry finds one package inside the overrides block.
func pubspecOverrideEntry(lines []string, name string) (pubspecOverride, bool) {
	start, end, ok := pubspecBlockBounds(lines, pubspecOverrides)
	if !ok {
		return pubspecOverride{pathLine: -1}, false
	}
	// The block's own entries all sit at one indent, fixed by the first of
	// them. Anything deeper belongs to an entry, and "path" is both a package
	// name and the key an entry nests its folder under, so a lookup that
	// ignored depth would find the wrong line.
	depth := pubspecEntryIndent(lines, start, end)
	for i := start; i < end; i++ {
		line := stripYAMLComment(lines[i])
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent != depth {
			continue
		}
		key, valueStart, ok := yamlKey(line)
		if !ok || key != name {
			continue
		}
		entry := pubspecOverride{nameLine: i, indent: indent, pathLine: -1, end: i + 1}
		// A path on the same line is legal YAML but not how pub writes it;
		// either way the nested form is what follows.
		if s, e, scalar := yamlScalarSpan(line, valueStart); scalar {
			entry.pathLine, entry.pathStart, entry.pathEnd = i, s, e
			entry.path = line[s:e]
			return entry, true
		}
		for j := i + 1; j < end; j++ {
			nested := stripYAMLComment(lines[j])
			if strings.TrimSpace(nested) == "" {
				entry.end = j + 1
				continue
			}
			if len(nested)-len(strings.TrimLeft(nested, " \t")) <= indent {
				break
			}
			entry.end = j + 1
			k, vs, ok := yamlKey(nested)
			if !ok || k != "path" {
				continue
			}
			if s, e, scalar := yamlScalarSpan(nested, vs); scalar {
				entry.pathLine, entry.pathStart, entry.pathEnd = j, s, e
				entry.path = nested[s:e]
			}
		}
		return entry, true
	}
	return pubspecOverride{pathLine: -1}, false
}

// pubspecEntryIndent reports the column a block's own entries sit at, taken
// from the first of them. An empty block reports the two-space default pub
// itself writes.
func pubspecEntryIndent(lines []string, start, end int) int {
	for i := start; i < end; i++ {
		line := stripYAMLComment(lines[i])
		if strings.TrimSpace(line) == "" {
			continue
		}
		return len(line) - len(strings.TrimLeft(line, " \t"))
	}
	return 2
}

// pubspecBlockBounds finds a top-level block's first and last nested lines.
func pubspecBlockBounds(lines []string, block string) (start, end int, ok bool) {
	for i, raw := range lines {
		line := stripYAMLComment(raw)
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		key, _, keyed := yamlKey(line)
		if !keyed || key != block {
			continue
		}
		end = len(lines)
		for j := i + 1; j < len(lines); j++ {
			body := stripYAMLComment(lines[j])
			if strings.TrimSpace(body) == "" {
				continue
			}
			if body[0] != ' ' && body[0] != '\t' {
				end = j
				break
			}
		}
		return i + 1, end, true
	}
	return 0, 0, false
}

// pubspecInsertOverride writes a new entry, creating the block when the file
// has none.
func pubspecInsertOverride(lines []string, name, path string) []string {
	start, end, ok := pubspecBlockBounds(lines, pubspecOverrides)
	if !ok {
		out := append([]string{}, lines...)
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		return append(out, "", pubspecOverrides+":", "  "+name+":", "    path: "+path, "")
	}
	// Match the width the block's existing entries use.
	indent := strings.Repeat(" ", pubspecEntryIndent(lines, start, end))
	at := end
	for at > start && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:at]...)
	out = append(out, indent+name+":", indent+indent+"path: "+path)
	return append(out, lines[at:]...)
}

// pubspecDropOverride removes one entry, and the block with it when that entry
// was the last thing it held.
func pubspecDropOverride(lines []string, entry pubspecOverride) []string {
	out := make([]string, 0, len(lines))
	out = append(out, lines[:entry.nameLine]...)
	out = append(out, lines[entry.end:]...)

	start, end, ok := pubspecBlockBounds(out, pubspecOverrides)
	if !ok {
		return out
	}
	for i := start; i < end; i++ {
		if strings.TrimSpace(stripYAMLComment(out[i])) != "" {
			return out
		}
	}
	stop := end
	for stop > start && strings.TrimSpace(out[stop-1]) == "" {
		stop--
	}
	return append(out[:start-1], out[stop:]...)
}

// pubspecVerifyOverrides re-reads the written bytes and checks every applied
// redirect reads back as the path it asked for.
func pubspecVerifyOverrides(out []byte, applied []Link) error {
	lines := strings.Split(string(out), "\n")
	for _, r := range applied {
		entry, found := pubspecOverrideEntry(lines, r.Name)
		if r.Path == "" {
			if found {
				return fmt.Errorf("rewrite left %s still overridden", r.Name)
			}
			continue
		}
		if !found || entry.path != r.Path {
			return fmt.Errorf("rewrite left %s pointing at %q, want %q", r.Name, entry.path, r.Path)
		}
	}
	return nil
}
