package writer

import (
	"fmt"
	"os"
	"strings"
)

// pbxKeyVersion is the marketing version an Xcode project carries per build
// configuration.
const pbxKeyVersion = "MARKETING_VERSION"

// rewriteXcodeProj sets an Xcode project's marketing version in every build
// configuration that declares it. Writing only the first would leave Debug and
// Release disagreeing, which is a worse state than either leaving the file
// alone or updating all of it; a project deliberately holding two targets at
// different versions is the case this cannot serve, and is documented as such.
//
// The project file declares no dependencies, so every edit is missing by
// definition, and CURRENT_PROJECT_VERSION is left alone: it is a monotonic
// build counter rather than a semantic version.
//
// Unlike every other format here there is no cheap grammar to re-parse the
// result against, so three guards stand in for one: the replacement may not
// carry a byte that could close or open a token, the file's brace balance must
// be unchanged, and the locator is re-run afterwards and must find exactly the
// intended value in exactly the places it found values before.
func rewriteXcodeProj(path, version string, edits []Edit) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	if strings.ContainsAny(version, "\";{}\n\r") {
		return res, fmt.Errorf("%s: refusing to write %q into a project file: it could not survive as one token", path, version)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}

	lines := strings.Split(string(data), "\n")
	before := 0
	changed := false
	for i, line := range lines {
		key, value, span, ok := pbxSetting(line)
		if !ok || key != pbxKeyVersion {
			continue
		}
		before++
		if value == version {
			continue
		}
		lines[i] = line[:span[0]] + version + line[span[1]:]
		changed = true
	}
	if !changed {
		return res, nil
	}

	out := []byte(strings.Join(lines, "\n"))
	if err := pbxVerify(data, out, version, before); err != nil {
		return res, fmt.Errorf("%s: internal error: %w", path, err)
	}
	res.VersionWritten = true
	return res, atomicWrite(path, out)
}

// pbxVerify checks the invariants that stand in for re-parsing: the structural
// punctuation is untouched, and every assignment the locator found before
// still parses and now reads the intended version.
func pbxVerify(before, after []byte, version string, count int) error {
	for _, brace := range []byte{'{', '}', '(', ')'} {
		if b, a := countByte(before, brace), countByte(after, brace); b != a {
			return fmt.Errorf("rewrite changed the %q balance (%d -> %d)", brace, b, a)
		}
	}
	seen := 0
	for _, line := range strings.Split(string(after), "\n") {
		key, value, _, ok := pbxSetting(line)
		if !ok || key != pbxKeyVersion {
			continue
		}
		seen++
		if value != version {
			return fmt.Errorf("rewrite left %s reading %q", pbxKeyVersion, value)
		}
	}
	if seen != count {
		return fmt.Errorf("rewrite changed the number of %s assignments (%d -> %d)", pbxKeyVersion, count, seen)
	}
	return nil
}

// countByte counts one byte's occurrences.
func countByte(data []byte, c byte) int {
	n := 0
	for _, b := range data {
		if b == c {
			n++
		}
	}
	return n
}

// pbxSetting splits one `KEY = VALUE;` assignment, reporting the value and the
// byte range it occupies in the line — the span excludes the quotes of a
// quoted value, so a splice preserves the file's existing quoting style. It
// mirrors the scanner's reader exactly, including requiring the terminating
// semicolon so a container opener (`buildSettings = {`) never reads as a
// setting, and skipping a conditional assignment
// (`MARKETING_VERSION[sdk=iphoneos*] = 1.0;`) whose bracket is not a name byte.
func pbxSetting(line string) (key, value string, span [2]int, ok bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && isPBXNameByte(line[i]) {
		i++
	}
	if i == start {
		return "", "", span, false
	}
	key = line[start:i]
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != '=' {
		return "", "", span, false
	}
	i++
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return "", "", span, false
	}

	valueStart, valueEnd := i, i
	if line[i] == '"' {
		i++
		valueStart = i
		for i < len(line) && line[i] != '"' {
			if line[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(line) {
			return "", "", span, false // unterminated
		}
		valueEnd = i
		i++
	} else {
		for i < len(line) && line[i] != ';' {
			i++
		}
		valueEnd = i
		for valueEnd > valueStart && (line[valueEnd-1] == ' ' || line[valueEnd-1] == '\t') {
			valueEnd--
		}
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || line[i] != ';' {
		return "", "", span, false
	}
	return key, line[valueStart:valueEnd], [2]int{valueStart, valueEnd}, true
}

// isPBXNameByte reports a byte that may appear in a build-setting name.
func isPBXNameByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}
