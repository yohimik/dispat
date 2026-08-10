package writer

import (
	"fmt"
	"os"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// pubspecTables maps a kind onto the top-level block its declarations live in.
// dependency_overrides is not a kind of its own — the scanner folds an override
// onto the declaration it overrides — so it is not written here.
var pubspecTables = map[manifest.Kind]string{
	manifest.KindDependencies:    "dependencies",
	manifest.KindDevDependencies: "dev_dependencies",
}

// rewritePubspec edits a pubspec.yaml line by line: the package's own
// `version`, and the constraint of each dependency matching an edit. Only the
// scalar being changed is replaced, so indentation, comments, quoting and key
// order all survive.
//
// A dependency whose value is a block rather than a scalar — a path, git or
// sdk dependency spelled across several lines — has no constraint on its own
// line to replace and is reported missing. So is a dependency in a block this
// format has no kind for.
func rewritePubspec(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
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
		lines   = strings.Split(string(data), "\n")
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
	if !changed {
		return res, nil
	}
	return res, atomicWrite(path, []byte(strings.Join(lines, "\n")))
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
