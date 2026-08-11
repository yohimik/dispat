package writer

import (
	"fmt"
	"strings"
)

// A Gradle build script is a Groovy or Kotlin program, so (as with the Xcode
// project file) there is no grammar to re-parse a rewrite against. The writer
// recognises exactly the statement shapes the scanner recognises, refuses a
// replacement carrying any byte that could end a literal or open an
// interpolation, checks the file's brace balance is unchanged, and re-runs the
// reader over the result. Anything else is reported missing rather than
// rewritten on a guess.

// rewriteGradleBuild sets a build script's versionName and the version segment
// of the literal coordinates matching the edits.
//
// A coordinate declared under several configurations (`implementation` and
// `testImplementation` of the same library) is updated in all of them: a build
// script pinning one library to two versions is a state worth not creating.
// Coordinates named through a version catalog (`implementation libs.retrofit`)
// or built by interpolation carry no literal to replace and are missing.
func rewriteGradleBuild(path, version string, edits []Edit) (Result, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return Result{}, err
	}
	if version != "" && !isGradleWritable(version) {
		return Result{}, fmt.Errorf("%s: refusing to write %q into a Gradle literal", path, version)
	}
	wanted := make(map[string]int, len(edits))
	for i, e := range edits {
		if !isGradleWritable(e.Range) {
			return Result{}, fmt.Errorf("%s: refusing to write %q into a Gradle literal", path, e.Range)
		}
		wanted[e.Name] = i
	}

	// found records the declarations located, which decides what is missing;
	// applied records the ones a splice actually moved. Keeping them apart is
	// what lets a caller tell "already correct" from "rewritten" the way every
	// other writer here does, and indexing both by edit keeps one coordinate
	// declared under several configurations from being reported twice.
	var (
		res          Result
		scope        []string
		inComment    bool
		found        = make(map[int]bool, len(edits))
		applied      = make(map[int]bool, len(edits))
		versionFound bool
		lines        = rep.lines()
		changed      bool
	)
	for li, raw := range lines {
		var masked string
		masked, inComment = gradleMask(raw, inComment)

		switch {
		case version != "" && !versionFound && gradleScopeHas(scope, "defaultConfig"):
			start, end, ok := gradlePropertySpan(masked, "versionName")
			if !ok {
				break
			}
			versionFound = true
			if raw[start:end] == version {
				break // already the wanted text: no change
			}
			lines[li] = raw[:start] + version + raw[end:]
			res.VersionWritten = true
			changed = true
		case gradleScopeHas(scope, "dependencies") && !gradleScopeHas(scope, "buildscript"):
			name, start, end, ok := gradleCoordinateSpan(raw, masked)
			if !ok {
				break
			}
			i, want := wanted[name]
			if !want {
				break
			}
			found[i] = true
			if raw[start:end] == edits[i].Range {
				break // already the wanted text: no change, not missing
			}
			applied[i] = true
			lines[li] = raw[:start] + edits[i].Range + raw[end:]
			changed = true
		}
		scope = gradleUpdateScope(scope, raw, masked)
	}
	// Reported in edit order rather than line order, so the result does not
	// depend on where in the file a declaration happens to sit.
	for i, e := range edits {
		switch {
		case applied[i]:
			res.Applied = append(res.Applied, e)
		case !found[i]:
			res.Missing = append(res.Missing, e)
		}
	}
	if changed {
		rep.setLines(lines)
	}
	return res, rep.commit(func(out []byte) error {
		return gradleVerify(rep.text(), string(out), res.Applied, version, res.VersionWritten)
	})
}

// isGradleWritable reports text that can stand inside a build script's string
// literal without changing the file's structure: no quote to close the literal,
// no backslash to start an escape, no '$' to open an interpolation, no brace to
// disturb the nesting, no newline.
func isGradleWritable(value string) bool {
	return !strings.ContainsAny(value, "'\"\\${}\n\r")
}

// gradleVerify checks the invariants that stand in for re-parsing: the brace
// nesting is untouched, and the reader agrees every splice landed where it was
// aimed.
func gradleVerify(before, after string, applied []Edit, version string, versionWritten bool) error {
	was, now := countTokens([]byte(before)), countTokens([]byte(after))
	for i, token := range structuralTokens {
		if was[i] != now[i] {
			return fmt.Errorf("rewrite changed the %q balance (%d -> %d)", token, was[i], now[i])
		}
	}
	want := make(map[string]string, len(applied))
	for _, e := range applied {
		want[e.Name] = e.Range
	}
	var (
		scope       []string
		inComment   bool
		versionSeen = !versionWritten
	)
	for _, raw := range strings.Split(after, "\n") {
		var masked string
		masked, inComment = gradleMask(raw, inComment)
		switch {
		case !versionSeen && gradleScopeHas(scope, "defaultConfig"):
			if start, end, ok := gradlePropertySpan(masked, "versionName"); ok {
				versionSeen = true
				if raw[start:end] != version {
					return fmt.Errorf("rewrite left versionName reading %q", raw[start:end])
				}
			}
		case gradleScopeHas(scope, "dependencies") && !gradleScopeHas(scope, "buildscript"):
			name, start, end, ok := gradleCoordinateSpan(raw, masked)
			if !ok {
				break
			}
			if text, ok := want[name]; ok && raw[start:end] != text {
				return fmt.Errorf("rewrite left %s at %q, want %q", name, raw[start:end], text)
			}
			delete(want, name)
		}
		scope = gradleUpdateScope(scope, raw, masked)
	}
	if len(want) > 0 {
		return fmt.Errorf("rewrite lost %d dependency declaration(s)", len(want))
	}
	if !versionSeen {
		return fmt.Errorf("rewrite lost the versionName assignment")
	}
	return nil
}

// gradlePropertySpan measures the string literal a property assigns, in either
// dialect's spelling, `versionName "1.0"` and `versionName = "1.0"`.
func gradlePropertySpan(masked, name string) (start, end int, ok bool) {
	i := gradleSkipSpace(masked, 0)
	if !strings.HasPrefix(masked[i:], name) {
		return 0, 0, false
	}
	i += len(name)
	if i < len(masked) && isGradleNameByte(masked[i]) {
		return 0, 0, false
	}
	i = gradleSkipSpace(masked, i)
	if i < len(masked) && masked[i] == '=' {
		i = gradleSkipSpace(masked, i+1)
	}
	return gradleQuoted(masked, i)
}

// gradleCoordinateSpan reads one dependency statement's literal coordinate,
// returning its "group:artifact" name and the span its version segment
// occupies. A statement naming no literal three-part coordinate (a catalog
// accessor, a project reference, an interpolated string) reports nothing.
func gradleCoordinateSpan(line, masked string) (name string, start, end int, ok bool) {
	i := gradleSkipSpace(masked, 0)
	nameStart := i
	for i < len(masked) && isGradleNameByte(masked[i]) {
		i++
	}
	if !isGradleConfiguration(line[nameStart:i]) {
		return "", 0, 0, false
	}
	i = gradleSkipSpace(masked, i)
	if i < len(masked) && masked[i] == '(' {
		i = gradleSkipSpace(masked, i+1)
	}
	start, end, ok = gradleQuoted(masked, i)
	if !ok {
		return "", 0, 0, false
	}
	coordinate := line[start:end]
	if strings.Contains(coordinate, "$") {
		return "", 0, 0, false
	}
	parts := strings.Split(coordinate, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", 0, 0, false
	}
	// The version is the third segment; the span narrows onto it alone.
	return parts[0] + ":" + parts[1], start + len(parts[0]) + len(parts[1]) + 2, end, true
}

// isGradleConfiguration reports a dependency configuration name, matched by
// shape because build variants and source sets multiply them without limit.
// It mirrors the scanner's mapping, minus the kind: the writer updates a
// coordinate wherever it is declared.
func isGradleConfiguration(config string) bool {
	lower := strings.ToLower(config)
	switch {
	case lower == "":
		return false
	case strings.Contains(lower, "annotationprocessor"), strings.Contains(lower, "kapt"),
		strings.Contains(lower, "ksp"), strings.Contains(lower, "compileonly"):
		return true
	case strings.HasSuffix(lower, "implementation"), strings.HasSuffix(lower, "api"),
		strings.HasSuffix(lower, "runtimeonly"):
		return true
	}
	return false
}

// gradleMask blanks the contents of string literals and comments while keeping
// the line's length and its string delimiters, so brace counting and token
// scanning cannot be misled by punctuation inside a literal, and offsets stay
// valid in the original line. The returned flag carries an unterminated block
// comment on to the next line.
func gradleMask(line string, inComment bool) (string, bool) {
	out := []byte(line)
	i := 0
	for i < len(line) {
		if inComment {
			if strings.HasPrefix(line[i:], "*/") {
				out[i], out[i+1] = ' ', ' '
				i += 2
				inComment = false
				continue
			}
			out[i] = ' '
			i++
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], "//"):
			for ; i < len(line); i++ {
				out[i] = ' '
			}
		case strings.HasPrefix(line[i:], "/*"):
			out[i], out[i+1] = ' ', ' '
			i += 2
			inComment = true
		case line[i] == '\'' || line[i] == '"':
			quote := line[i]
			i++
			for i < len(line) && line[i] != quote {
				if line[i] == '\\' && i+1 < len(line) {
					out[i], out[i+1] = ' ', ' '
					i += 2
					continue
				}
				out[i] = ' '
				i++
			}
			if i < len(line) {
				i++
			}
		default:
			i++
		}
	}
	return string(out), inComment
}

// gradleUpdateScope tracks the block nesting a line leaves behind, naming each
// block after the identifier that opens it.
func gradleUpdateScope(scope []string, line, masked string) []string {
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case '{':
			scope = append(scope, gradleIdentBefore(line, masked, i))
		case '}':
			if len(scope) > 0 {
				scope = scope[:len(scope)-1]
			}
		}
	}
	return scope
}

// gradleIdentBefore names the block opening at i: the identifier preceding the
// brace, stepping over a parenthesised argument list.
func gradleIdentBefore(line, masked string, i int) string {
	i--
	for i >= 0 && (masked[i] == ' ' || masked[i] == '\t') {
		i--
	}
	if i >= 0 && masked[i] == ')' {
		depth := 0
		for ; i >= 0; i-- {
			if masked[i] == ')' {
				depth++
			}
			if masked[i] == '(' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		i--
		for i >= 0 && (masked[i] == ' ' || masked[i] == '\t') {
			i--
		}
	}
	end := i + 1
	for i >= 0 && isGradleNameByte(masked[i]) {
		i--
	}
	if i+1 >= end {
		return ""
	}
	return line[i+1 : end]
}

// gradleScopeHas reports an enclosing block of the given name.
func gradleScopeHas(scope []string, name string) bool {
	for _, s := range scope {
		if s == name {
			return true
		}
	}
	return false
}

// gradleQuoted measures the string literal at or after from in a masked line.
func gradleQuoted(masked string, from int) (start, end int, ok bool) {
	i := gradleSkipSpace(masked, from)
	if i >= len(masked) || (masked[i] != '\'' && masked[i] != '"') {
		return 0, 0, false
	}
	quote := masked[i]
	i++
	start = i
	for i < len(masked) && masked[i] != quote {
		i++
	}
	if i >= len(masked) {
		return 0, 0, false
	}
	return start, i, true
}

// gradleSkipSpace advances past horizontal whitespace.
func gradleSkipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// isGradleNameByte reports a byte that may appear in a Gradle identifier.
func isGradleNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
