package writer

import "strings"

// isDeferredValue reports a value that names something defined elsewhere
// instead of stating a version. Every build system in scope has a spelling for
// it:
//
//	$(MARKETING_VERSION)   Xcode build settings, and MSBuild properties
//	${project.version}     Maven properties, and Gradle interpolation
//	$version$              NuGet pack-time replacement tokens
//
// Writing a literal over any of these breaks the link the author put there.
// The value stops tracking whatever defined it and freezes at the number that
// happened to be current, which is worse than declining the edit, so every
// writer declines.
//
// The three spellings are checked together rather than per format. A version
// string never looks like any of them, so a format that cannot produce one
// loses nothing by testing for it.
func isDeferredValue(v string) bool {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, "$(") && strings.HasSuffix(v, ")"):
		return true
	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		return true
	case len(v) > 2 && strings.HasPrefix(v, "$") && strings.HasSuffix(v, "$"):
		return true
	}
	return false
}
