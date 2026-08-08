package scanner

import (
	"path/filepath"
	"strings"
)

// The second manifest shape: not a structured document but packages written
// line by line, one "name specifier" per line. requirements files are the
// canonical case; the line loop is kept separate from the PEP 508 parsing so
// further line formats can reuse it.

// isRequirementsFile reports a pip requirements file: a .txt whose name
// contains "requirements" (requirements.txt, requirements-dev.txt,
// dev-requirements.txt, ...).
func isRequirementsFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".txt") && strings.Contains(lower, "requirements")
}

// requirementsKind maps the file name onto the dependency field it stands
// for: a dev/test requirements file installs for contributors, not
// consumers.
func requirementsKind(name string) Kind {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "dev") || strings.Contains(lower, "test") {
		return KindDevDependencies
	}
	return KindDependencies
}

// parseRequirements reads a requirements file: each non-empty line that is
// not a comment, a pip flag (-r, -e, --hash, ...), a bare path or a URL is
// one PEP 508 requirement. The file declares no identity of its own — line
// manifests name their dependencies, never themselves.
func parseRequirements(rel string, data []byte) (Manifest, error) {
	kind := requirementsKind(filepath.Base(rel))
	m := Manifest{Path: rel, Ecosystem: EcosystemPython, Root: isRoot(rel)}
	for _, line := range requirementLines(string(data)) {
		m.Deps = append(m.Deps, pep508Dep(line, kind))
	}
	sortDeps(m.Deps)
	return m, nil
}

// requirementLines yields the requirement lines of a pip-style file:
// continuations joined, comments stripped, flags/paths/URLs dropped.
func requirementLines(text string) []string {
	var out []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		// A trailing backslash continues the requirement on the next line.
		for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, `\`) + strings.TrimRight(lines[i], "\r")
		}
		line = stripRequirementComment(line)
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "-"): // pip flags: -r, -c, -e, --hash, ...
		case strings.Contains(line, "://"): // a bare URL requirement
		case strings.HasPrefix(line, "./") || strings.HasPrefix(line, "../") || strings.HasPrefix(line, "/"):
			// a bare path requirement carries no name to match on
		default:
			out = append(out, line)
		}
	}
	return out
}

// stripRequirementComment removes a trailing " # ..." comment. Only a hash
// preceded by whitespace (or starting the line) comments; a hash inside a
// URL fragment does not.
func stripRequirementComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	for i := 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}
