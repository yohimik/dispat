package writer

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The ProjectSettings.asset keys the writer touches, and the indent they sit
// at, spelled as the reader spells them.
const (
	unityPlayerIndent      = 2
	unityKeyBundleVersion  = "bundleVersion"
	unityKeyAndroidCode    = "AndroidBundleVersionCode"
	unityKeyBuildNumber    = "buildNumber"
	unityFieldDependencies = "dependencies"
)

// rewriteUnityPackages edits a Packages/manifest.json. Unity's package
// manifest is one flat JSON map of name to version, so this is the same
// byte-precise scalar splice package.json gets, with one field instead of
// four. The manifest declares no version of its own, so an edit is the only
// thing it can be asked to write.
//
// A dependency pinned to a git URL or a folder is written like any other:
// Unity resolves all three forms from the same field, and a caller asking for
// a version there means it.
func rewriteUnityPackages(path string, edits []Edit) (Result, error) {
	return rewriteJSON(path, "", edits, func(e Edit) string {
		if e.Kind == manifest.KindDependencies {
			return unityFieldDependencies
		}
		// Unity has one dependency field. A dev or peer edit names something
		// the format cannot express, and npmSpans reports it missing.
		return ""
	})
}

// rewriteUnityProjectSettings sets a project's bundleVersion, the marketing
// version Unity stamps into every build. AndroidBundleVersionCode and the
// per-platform counters under buildNumber are deliberately untouched: they are
// monotonic counters, and SetBuild is where one moves.
//
// The settings file declares no dependencies, so every edit is missing.
func rewriteUnityProjectSettings(path, version string, edits []Edit) (Result, error) {
	res := Result{Missing: edits}
	if version == "" {
		return res, nil
	}
	if err := unityRefuse(path, version); err != nil {
		return res, err
	}
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	before := sp.bytes()
	lines := sp.lines()
	found, changed := unitySplice(lines, func(indent int, key string) bool {
		return indent == unityPlayerIndent && key == unityKeyBundleVersion
	}, version)
	if changed == 0 {
		return res, nil
	}
	sp.setLines(lines)
	res.VersionWritten = true
	return res, sp.commit(func(out []byte) error {
		return unityVerify(before, out, func(indent int, key string) bool {
			return indent == unityPlayerIndent && key == unityKeyBundleVersion
		}, version, found)
	})
}

// setUnityBuild writes every store build counter the settings file declares:
// AndroidBundleVersionCode, and each per-platform entry under buildNumber.
//
// Every one of them, not the first. A Unity project ships to several stores
// from one settings file, and stamping the Android counter while leaving the
// iOS one behind uploads a build the store orders wrongly and says nothing
// about. It is the same reasoning that has the Xcode writer stamp every build
// configuration.
//
// Unity parses these as integers, so a non-integer is refused before the file
// is opened. A counter the project does not declare is never created.
func setUnityBuild(path, build string) (Result, error) {
	var res Result
	if !allDigits(build) {
		return res, errNotAnInteger(path, "the Unity build counter", build)
	}
	sp, err := openSplicer(path)
	if err != nil {
		return res, err
	}
	before := sp.bytes()
	lines := sp.lines()
	found, changed := unitySplice(lines, isUnityCounter, build)
	if changed == 0 {
		return res, nil
	}
	sp.setLines(lines)
	res.BuildWritten = true
	return res, sp.commit(func(out []byte) error {
		return unityVerify(before, out, isUnityCounter, build, found)
	})
}

// isUnityCounter reports a line holding a build counter: the Android one at
// the settings level, or any platform entry nested under buildNumber. The
// nesting is recognised by indent alone, which is safe because unitySplice
// only offers it lines that follow the buildNumber key.
func isUnityCounter(indent int, key string) bool {
	if indent == unityPlayerIndent {
		return key == unityKeyAndroidCode
	}
	return indent > unityPlayerIndent
}

// unitySplice rewrites the value of every entry mine accepts, in place, and
// reports how many it found and how many moved. Entries nested under
// buildNumber are offered to mine only while that key is the one in scope, so
// a deeper mapping elsewhere in the file is never mistaken for a counter.
func unitySplice(lines []string, mine func(indent int, key string) bool, value string) (found, changed int) {
	var scope unityScope
	for i, raw := range lines {
		indent, key, ok := unityEntryKey(raw)
		if !ok {
			continue
		}
		// The scope is tracked from the key alone. "buildNumber:" has nothing
		// after its colon, being the head of a nested mapping, so a walk that
		// waited for a value span would never notice the mapping had opened
		// and would leave every platform counter under it behind.
		scope.enter(indent, key)
		if !scope.holds(indent) || !mine(indent, key) {
			continue
		}
		start, end, ok := unityValueSpan(raw)
		if !ok {
			continue
		}
		found++
		if raw[start:end] == value {
			continue
		}
		lines[i] = raw[:start] + value + raw[end:]
		changed++
	}
	return found, changed
}

// unityScope tracks whether the walk is inside the buildNumber mapping, which
// is the only nesting either writer descends into. Anything deeper elsewhere
// in the file is stepped over, so a mapping whose keys look like counters (the
// aspect ratios, whose keys are spelled 16:9) is never mistaken for one.
type unityScope struct{ inBuildNumber bool }

// enter updates the scope for one entry.
func (s *unityScope) enter(indent int, key string) {
	if indent <= unityPlayerIndent {
		s.inBuildNumber = indent == unityPlayerIndent && key == unityKeyBuildNumber
	}
}

// holds reports whether an entry at this indent is one the walk may write.
func (s *unityScope) holds(indent int) bool {
	return indent <= unityPlayerIndent || s.inBuildNumber
}

// unityEntryKey reports one mapping entry's indent and key. It says nothing
// about the value, so the head of a nested mapping ("buildNumber:") is an
// entry here exactly as an assignment is; that is what lets a walk notice a
// mapping opening.
func unityEntryKey(line string) (indent int, key string, ok bool) {
	limit := unityLineLimit(line)
	if limit == 0 {
		return 0, "", false
	}
	for indent < limit && line[indent] == ' ' {
		indent++
	}
	if indent >= limit || line[indent] == '#' || line[indent] == '-' {
		return 0, "", false
	}
	colon := strings.IndexByte(line[indent:limit], ':')
	if colon < 0 {
		return 0, "", false
	}
	return indent, line[indent : indent+colon], true
}

// unityValueSpan measures the byte span of an entry's value. A line with
// nothing after its colon reports no span, so the head of a nested mapping can
// never be spliced into.
func unityValueSpan(line string) (start, end int, ok bool) {
	indent, key, ok := unityEntryKey(line)
	if !ok {
		return 0, 0, false
	}
	start, end = indent+len(key)+1, unityLineLimit(line)
	if c := unityCommentAt(line[start:end]); c >= 0 {
		end = start + c
	}
	for start < end && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	for end > start && (line[end-1] == ' ' || line[end-1] == '\t') {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

// unityLineLimit is the length of a line's content: the trailing carriage
// return dropped, and zero for the document markers nothing ever targets.
func unityLineLimit(line string) int {
	limit := len(line)
	if limit > 0 && line[limit-1] == '\r' {
		limit--
	}
	if limit == 0 || line[0] == '%' || strings.HasPrefix(line, "---") {
		return 0
	}
	return limit
}

// unityCommentAt reports where a trailing YAML comment begins, or -1. A
// comment opens at a '#' preceded by space, so a value like "Level #1"
// survives whole.
func unityCommentAt(value string) int {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// unityRefuse rejects a value that could not survive in a Unity asset file: a
// colon would read as a second key, a '#' after a space as a comment, a
// newline would split the entry, and leading or trailing space would be eaten
// by the next read and make the write look like it did not converge.
func unityRefuse(path, value string) error {
	if value != strings.TrimSpace(value) || strings.ContainsAny(value, ":#\n\r") {
		return fmt.Errorf("%s: refusing to write %q into a settings file: it could not survive as one value", path, value)
	}
	return nil
}

// unityVerify is the re-read proof this format stands on. yaml.v3 refuses the
// !u!129 tag on a perfectly valid file, so a parse here would refuse every
// write; these invariants stand in for one. The line count is unchanged,
// because every splice is inside a line. The document header is unchanged byte
// for byte, because nothing above the root mapping is ever a target. And the
// re-read finds the same entries the write found, each now reading the value
// it meant to write.
func unityVerify(before, after []byte, mine func(indent int, key string) bool, value string, found int) error {
	wasLines := strings.Split(string(before), "\n")
	nowLines := strings.Split(string(after), "\n")
	if len(wasLines) != len(nowLines) {
		return fmt.Errorf("rewrite changed the line count from %d to %d", len(wasLines), len(nowLines))
	}
	for i, line := range wasLines {
		if line != "" && (line[0] == '%' || strings.HasPrefix(line, "---")) && line != nowLines[i] {
			return fmt.Errorf("rewrite changed the document header line %q", line)
		}
	}
	seen := 0
	var scope unityScope
	for _, raw := range nowLines {
		indent, key, ok := unityEntryKey(raw)
		if !ok {
			continue
		}
		scope.enter(indent, key)
		if !scope.holds(indent) || !mine(indent, key) {
			continue
		}
		start, end, ok := unityValueSpan(raw)
		if !ok {
			continue
		}
		seen++
		if raw[start:end] != value {
			return fmt.Errorf("rewrite left %q reading %q", key, raw[start:end])
		}
	}
	if seen != found {
		return fmt.Errorf("rewrite left %d of %d entries of the edited keys", seen, found)
	}
	return nil
}
