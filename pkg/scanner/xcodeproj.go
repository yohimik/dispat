package scanner

import (
	"strings"
)

// The build settings the scanner reads out of an Xcode project file.
const (
	pbxKeyIdentifier  = "PRODUCT_BUNDLE_IDENTIFIER"
	pbxKeyVersion     = "MARKETING_VERSION"
	pbxKeyBuildNumber = "CURRENT_PROJECT_VERSION"
)

// parseXcodeProj reads an Xcode project file (project.pbxproj, inside an
// .xcodeproj folder). It is not a NeXTSTEP-plist parser: that grammar carries
// unquoted tokens, nameless nested containers and interleaved comments, all
// of which would be a great deal of machinery for the three build settings
// that actually matter here. Instead it scans for those settings' assignments
// directly.
//
// A setting repeats once per build configuration (Debug, Release, and whatever
// else a project defines), so the first non-empty value wins: the rule the
// .csproj parser already applies to properties repeated across PropertyGroups.
// Where configurations genuinely disagree the writer is the half that has to
// care; a reader reporting one of them is enough to place the project in the
// graph.
//
// The project file declares no dependencies. Swift Package Manager
// requirements live in Package.swift, which is executable Swift rather than a
// manifest, and is out of scope by construction.
func parseXcodeProj(rel string, data []byte) (Manifest, error) {
	settings := pbxSettings(data, pbxKeyIdentifier, pbxKeyVersion, pbxKeyBuildNumber)
	m := Manifest{
		Path:        rel,
		Ecosystem:   EcosystemXcode,
		Version:     settings[pbxKeyVersion],
		BuildNumber: settings[pbxKeyBuildNumber],
		Root:        isRoot(rel),
	}
	if id := settings[pbxKeyIdentifier]; !isBuildSettingRef(id) {
		m.Name = id
	}
	return m, nil
}

// pbxSettings collects the first value seen for each wanted build setting.
func pbxSettings(data []byte, wanted ...string) map[string]string {
	want := make(map[string]bool, len(wanted))
	for _, key := range wanted {
		want[key] = true
	}
	found := make(map[string]string, len(wanted))
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _, ok := pbxSetting(line)
		if !ok || !want[key] || value == "" {
			continue
		}
		if _, seen := found[key]; !seen {
			found[key] = value
		}
	}
	return found
}

// pbxSetting splits one `KEY = VALUE;` assignment, reporting the decoded
// value and the byte range it occupies in the line. The terminating semicolon
// is required, which is what keeps a container opener (`buildSettings = {`)
// from reading as a setting. A conditional assignment
// (`MARKETING_VERSION[sdk=iphoneos*] = 1.0;`) does not match: the bracket is
// not a valid name byte, so the whole line is skipped rather than
// misattributed.
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
