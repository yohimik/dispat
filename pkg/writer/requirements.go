package writer

import (
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The line-by-line manifest shape: one "name specifier" per line, which makes
// format-preserving rewriting a per-line splice: the name spelling,
// surrounding spacing and trailing comment all survive, only the specifier
// text changes.

// isRequirementsFile is pkg/manifest's rule, shared verbatim with the
// scanner.
func isRequirementsFile(name string) bool { return manifest.IsRequirementsFile(name) }

// rewriteRequirements edits a requirements file line by line: a line whose
// requirement name matches an edit (PEP 503-normalised, so "Acme_Core"
// matches "acme-core") gets its version specifier replaced. Lines that are
// comments, pip flags, paths or URLs are never touched; edits whose name no
// line declares are Missing. Requirements files declare no own version, so
// the version argument has no target here.
func rewriteRequirements(path string, edits []Edit) (Result, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return Result{}, err
	}
	wanted := make(map[string]int, len(edits)) // normalised name -> edit index
	for i, e := range edits {
		wanted[normalizePyName(e.Name)] = i
	}

	var res Result
	found := make(map[int]bool, len(edits))
	lines := rep.lines()
	changed := false
	for li, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		name, nameEnd, specEnd, ok := requirementSpans(line)
		if !ok {
			continue
		}
		i, want := wanted[normalizePyName(name)]
		if !want {
			continue
		}
		found[i] = true
		spec := strings.TrimSpace(line[nameEnd:specEnd])
		if spec == edits[i].Range {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, edits[i])
		next := line[:nameEnd] + edits[i].Range + line[specEnd:]
		if strings.HasSuffix(raw, "\r") {
			next += "\r"
		}
		lines[li] = next
		changed = true
	}
	for i, e := range edits {
		if !found[i] {
			res.Missing = append(res.Missing, e)
		}
	}
	if changed {
		rep.setLines(lines)
	}
	return res, rep.commit(nil)
}

// requirementSpans locates, in one requirement line, the declared name and the
// byte range its version specifier occupies: [nameEnd, specEnd), where specEnd
// stops at a trailing comment. ok is false for lines that declare no
// requirement (comments, flags, paths, URLs, continuations, too odd to splice
// safely).
func requirementSpans(line string) (name string, nameEnd, specEnd int, ok bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "", strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, "-"),
		strings.Contains(trimmed, "://"), strings.HasSuffix(trimmed, `\`),
		strings.HasPrefix(trimmed, "./"), strings.HasPrefix(trimmed, "../"), strings.HasPrefix(trimmed, "/"):
		return "", 0, 0, false
	}
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	nameEnd = len(line)
	for i := start; i < len(line); i++ {
		if strings.ContainsRune("[<>=!~; @(#", rune(line[i])) || line[i] == ' ' || line[i] == '\t' {
			nameEnd = i
			break
		}
	}
	name = line[start:nameEnd]
	if name == "" {
		return "", 0, 0, false
	}
	// Extras ([cli]) belong to the name side of the splice.
	if nameEnd < len(line) && line[nameEnd] == '[' {
		if end := strings.IndexByte(line[nameEnd:], ']'); end >= 0 {
			nameEnd += end + 1
		}
	}
	specEnd = len(line)
	for i := nameEnd + 1; i < len(line); i++ {
		if line[i] == '#' && (line[i-1] == ' ' || line[i-1] == '\t') {
			specEnd = i
			break
		}
	}
	// Spacing between the specifier and the comment stays with the comment.
	for specEnd > nameEnd && (line[specEnd-1] == ' ' || line[specEnd-1] == '\t') {
		specEnd--
	}
	return name, nameEnd, specEnd, true
}

// normalizePyName applies PEP 503, mirroring the scanner: lowercase, runs of
// -, _ and . collapse to a single -.
func normalizePyName(name string) string { return manifest.NormalizePyName(name) }
