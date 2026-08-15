package scanner

import (
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The second manifest shape: not a structured document but packages written
// line by line, one "name specifier" per line. requirements files are the
// canonical case; the line loop is kept separate from the PEP 508 parsing so
// further line formats can reuse it.

// requirementsKind maps the file name onto the dependency field it stands for:
// a dev/test requirements file installs for contributors, not consumers. Whole
// words only, "requirements-latest.txt" contains the letters of "test" but is
// not a test file.
func requirementsKind(name string) Kind {
	words := manifest.NameWords(strings.TrimSuffix(strings.ToLower(name), ".txt"))
	for _, w := range words {
		switch w {
		case "dev", "development", "test", "tests", "testing":
			return KindDevDependencies
		}
	}
	return KindDependencies
}

// parseRequirements reads a requirements file: each non-empty line that is not
// a comment, a pip flag (-r, --hash, ...), a bare path or a URL is one PEP 508
// requirement. An editable install of a local path (-e ./core) is a workspace
// edge and yields a path-only declaration named after the folder. The file
// declares no identity of its own, line manifests name their dependencies,
// never themselves.
func parseRequirements(rel string, data []byte) (Manifest, error) {
	kind := requirementsKind(filepath.Base(rel))
	m := Manifest{Path: rel, Ecosystem: EcosystemPython, Root: isRoot(rel)}
	reqs, editables := requirementLines(string(data))
	for _, line := range reqs {
		m.Deps = append(m.Deps, pep508Dep(line, kind))
	}
	for _, p := range editables {
		base := filepath.Base(strings.TrimRight(p, "/"))
		m.Deps = append(m.Deps, DeclaredDep{Name: base, Kind: kind, LocalPath: p})
	}
	sortDeps(m.Deps)
	return m, nil
}

// requirementLines yields the requirement lines of a pip-style file:
// continuations joined, per-requirement pip options (--hash=..., the
// pip-compile output) cut off, comments stripped, flags/paths/URLs dropped,
// except editable local paths (-e ./x), returned separately as workspace
// edges.
func requirementLines(text string) (reqs, editables []string) {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		// A trailing backslash continues the requirement on the next line.
		for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, `\`) + strings.TrimRight(lines[i], "\r")
		}
		line = stripRequirementComment(line)
		// Options attached to the requirement itself ("pkg==1.0 --hash=...")
		// are pip's business, not part of the PEP 508 specifier.
		if cut := strings.Index(line, " --"); cut >= 0 {
			line = line[:cut]
		}
		if cut := strings.Index(line, "\t--"); cut >= 0 {
			line = line[:cut]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "-e ") || strings.HasPrefix(line, "--editable "):
			target := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "--editable "), "-e "))
			if isLocalRequirementPath(target) {
				editables = append(editables, target)
			}
		case strings.HasPrefix(line, "-"): // other pip flags: -r, -c, ...
		case strings.Contains(line, "://"): // a bare URL requirement
		case isLocalRequirementPath(line):
			// a bare path requirement carries no name to match on
		default:
			reqs = append(reqs, line)
		}
	}
	return reqs, editables
}

// isLocalRequirementPath reports a filesystem-path requirement target.
func isLocalRequirementPath(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/") || s == "." || s == ".."
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
