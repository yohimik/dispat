package writer

import "github.com/yohimik/dispat/pkg/manifest"

// rewriteComposer edits a composer.json. Composer is JSON like npm, so this is
// the same byte-precise scalar splice under different field names: `require`
// and `require-dev` rather than objects named after the kinds themselves.
// Composer has no peer or optional dependency field, so an edit carrying one
// of those kinds names something the format cannot express and is missing.
//
// A composer.json usually declares no `version` of its own (Packagist derives
// it from the git tag, and hard-coding one is discouraged) in which case there
// is nothing to write and Result.VersionWritten stays false. Where a package
// does declare one it is rewritten like any other.
func rewriteComposer(path, version string, edits []Edit) (Result, error) {
	return rewriteJSON(path, version, edits, composerField)
}

// composerField names the object a kind lives in, or "" where Composer has no
// equivalent field.
func composerField(e Edit) string {
	switch e.Kind {
	case manifest.KindDependencies:
		return "require"
	case manifest.KindDevDependencies:
		return "require-dev"
	}
	return ""
}
