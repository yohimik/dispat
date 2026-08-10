package writer

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/yohimik/dispat/pkg/manifest"
)

// pyTarget identifies one declaration: a kind and a PEP 503-normalised name,
// so an edit naming "acme-core" finds a declaration spelled "Acme.Core".
type pyTarget struct {
	kind manifest.Kind
	name string
}

// pyLocation is where one version literal sits: a line and the span it
// occupies within it.
type pyLocation struct {
	line       int
	start, end int
}

// rewritePyproject edits a pyproject.toml: the project's own version, the PEP
// 508 entries of the [project] dependency arrays, and the entries of the
// Poetry dependency tables. Only the specifier being changed is replaced, so
// the distribution name's spelling, its extras, any environment marker, the
// quoting and every comment survive.
//
// The kinds follow the scanner exactly, because the two halves disagreeing
// about which table a kind lives in is the failure pkg/manifest exists to
// prevent: [project.dependencies] and Poetry's main group are runtime,
// [project.optional-dependencies] are optional, and PEP 735 groups and every
// non-main Poetry group are development.
//
// The own version is written to [project] when that table declares one and to
// [tool.poetry] otherwise — the same precedence the scanner reads it with. An
// entry whose value is a table of constraints rather than a scalar, or whose
// text the requirement reader cannot make sense of, is reported missing.
func rewritePyproject(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[pyTarget]int, len(edits))
	for i, e := range edits {
		kind := e.Kind
		if kind == "dependencies" {
			kind = manifest.KindDependencies
		}
		wanted[pyTarget{kind, normalizePyName(e.Name)}] = i
	}

	var (
		res            Result
		found          = make(map[int]bool, len(edits))
		seen           = make(map[int]bool, len(edits))
		lines          = strings.Split(string(data), "\n")
		changed        bool
		table          string
		arrayDepth     int
		arrayKind      manifest.Kind
		projectVersion *pyLocation
		poetryVersion  *pyLocation
	)

	for li, raw := range lines {
		body := stripTOMLComment(raw)

		// Inside a multi-line requirement array, every quoted entry is a
		// candidate until the brackets balance again.
		if arrayDepth > 0 {
			applied := pySpliceRequirements(lines, li, body, 0, arrayKind, wanted, edits, found)
			res.Applied = append(res.Applied, applied...)
			changed = changed || len(applied) > 0
			arrayDepth += pyBracketDelta(body)
			continue
		}

		if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "[") {
			table = strings.TrimSpace(strings.Trim(trimmed, "[]"))
			continue
		}
		key, afterEq, ok := tomlKeyValue(body)
		if !ok {
			continue
		}
		value := afterEq
		for value < len(body) && (body[value] == ' ' || body[value] == '\t') {
			value++
		}

		if value < len(body) && body[value] == '[' {
			kind, isDeps := pyArrayKind(table, key)
			if !isDeps {
				continue
			}
			arrayKind = kind
			arrayDepth = pyBracketDelta(body[value:])
			applied := pySpliceRequirements(lines, li, body, value, kind, wanted, edits, found)
			res.Applied = append(res.Applied, applied...)
			changed = changed || len(applied) > 0
			continue
		}

		if key == "version" {
			start, end, ok := tomlQuotedSpan(body, afterEq)
			if !ok {
				continue
			}
			switch table {
			case "project":
				if projectVersion == nil {
					projectVersion = &pyLocation{li, start, end}
				}
			case "tool.poetry":
				if poetryVersion == nil {
					poetryVersion = &pyLocation{li, start, end}
				}
			}
			continue
		}

		kind, isDeps := pyTableKind(table)
		if !isDeps {
			continue
		}
		idx, want := wanted[pyTarget{kind, normalizePyName(key)}]
		if !want {
			continue
		}
		seen[idx] = true
		start, end, ok := pyPoetryValueSpan(body, afterEq)
		if !ok {
			continue // a table of constraints, not a scalar
		}
		found[idx] = true
		if body[start:end] == edits[idx].Range {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, edits[idx])
		lines[li] = lines[li][:start] + edits[idx].Range + lines[li][end:]
		changed = true
	}

	if own := projectVersion; own != nil || poetryVersion != nil {
		if own == nil {
			own = poetryVersion
		}
		if version != "" && lines[own.line][own.start:own.end] != version {
			res.VersionWritten = true
			lines[own.line] = lines[own.line][:own.start] + version + lines[own.line][own.end:]
			changed = true
		}
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

	// The splice is span-precise, but a manifest is user data: never write
	// bytes back without proving they still parse.
	out := []byte(strings.Join(lines, "\n"))
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return res, fmt.Errorf("%s: internal error: rewrite produced invalid TOML: %w", path, err)
	}
	return res, atomicWrite(path, out)
}

// pyArrayKind reports the kind an array-valued key declares, for the tables
// holding PEP 508 requirement lists.
func pyArrayKind(table, key string) (manifest.Kind, bool) {
	switch {
	case table == "project" && key == "dependencies":
		return manifest.KindDependencies, true
	case table == "project.optional-dependencies":
		return manifest.KindOptionalDependencies, true
	case table == "dependency-groups":
		return manifest.KindDevDependencies, true
	}
	return "", false
}

// pyTableKind reports the kind a Poetry dependency table declares. Every
// non-main group installs for contributors rather than consumers.
func pyTableKind(table string) (manifest.Kind, bool) {
	switch {
	case table == "tool.poetry.dependencies":
		return manifest.KindDependencies, true
	case table == "tool.poetry.dev-dependencies":
		return manifest.KindDevDependencies, true
	case strings.HasPrefix(table, "tool.poetry.group.") && strings.HasSuffix(table, ".dependencies"):
		return manifest.KindDevDependencies, true
	}
	return "", false
}

// pyPoetryValueSpan measures a Poetry entry's constraint: the whole value when
// it is a plain string, or the `version` key of an inline table.
func pyPoetryValueSpan(body string, afterEq int) (start, end int, ok bool) {
	i := afterEq
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	if i < len(body) && body[i] == '{' {
		return tomlInlineValueSpan(body, i, "version")
	}
	return tomlQuotedSpan(body, afterEq)
}

// pySpliceRequirements replaces the version specifier of every PEP 508 entry
// on one line whose distribution matches an edit. The entry text is reused
// verbatim apart from the specifier, and patches are applied back to front so
// earlier offsets stay valid.
func pySpliceRequirements(lines []string, li int, body string, from int, kind manifest.Kind,
	wanted map[pyTarget]int, edits []Edit, found map[int]bool) []Edit {
	type patch struct {
		start, end int
		text       string
	}
	var (
		applied []Edit
		patches []patch
	)
	for i := from; i < len(body); i++ {
		quote := body[i]
		if quote != '"' && quote != '\'' {
			continue
		}
		j := i + 1
		for j < len(body) && body[j] != quote {
			if quote == '"' && body[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(body) {
			break
		}
		entry := body[i+1 : j]
		name, nameEnd, specEnd, ok := requirementSpans(entry)
		if ok {
			if idx, want := wanted[pyTarget{kind, normalizePyName(name)}]; want {
				found[idx] = true
				if entry[nameEnd:specEnd] != edits[idx].Range {
					applied = append(applied, edits[idx])
					patches = append(patches, patch{i + 1 + nameEnd, i + 1 + specEnd, edits[idx].Range})
				}
			}
		}
		i = j
	}
	for k := len(patches) - 1; k >= 0; k-- {
		p := patches[k]
		lines[li] = lines[li][:p.start] + p.text + lines[li][p.end:]
	}
	return applied
}

// pyBracketDelta counts the array brackets a line opens minus those it closes,
// ignoring anything inside a string literal.
func pyBracketDelta(body string) int {
	depth := 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; c {
		case '"', '\'':
			for i++; i < len(body) && body[i] != c; i++ {
				if c == '"' && body[i] == '\\' {
					i++
				}
			}
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return depth
}
