// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the change-scope pattern dialect.
//
// Goal 4 owns what the ignore levels are for. What is here is how one pattern
// is read: a name with no separator reaches any depth, a folder named with a
// path reaches everything under it, a backslash makes a leading "!" a literal
// character rather than a re-inclusion, and a list of nothing but comments
// says nothing at all. Each is a rule a reader has to be able to rely on, and
// each is invisible from the outside except through what a commit releases.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// ignoreDialectRepo is a released "core" package whose ignore list is
// patterns, with the changelog off so the release leaves nothing untracked
// and the next commit's file list is exactly what the scenario wrote.
func ignoreDialectRepo(t *testing.T, patterns ...string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	cfg.Packages = map[string]models.PackageConfig{"core": {Ignore: patterns}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	require.Empty(t, r.Git("status", "--porcelain"), "the fixture release left nothing behind")
	return r
}

// assertCountsAsChange commits files and says whether the package came back
// into the plan for it. The commit is deliberately scopeless, so the only
// thing that can address the package is the files themselves.
func assertCountsAsChange(t *testing.T, r *harness.Repo, counted bool, files map[string]string) {
	t.Helper()
	for path, body := range files {
		r.WriteFile(path, body)
	}
	r.Commit("fix: an unscoped change")
	res := r.StatusOK("--log-format", "json")
	line := harness.GraphLine(res.Events, "core")
	if counted {
		assert.Contains(t, line.Str("message"), "● changed", "core should be back in the plan:\n%s", res.Stdout)
		return
	}
	assert.Equal(t, "unchanged", line.Str("message"),
		"an ignored file is not a change to the package:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "W131"), "the unit resolved to no package:\n%s", res.Stdout)
}

// TestCovIgnoreBareNameReachesAnyDepth: a pattern with no separator names the
// last segment of a path as well as the whole path, so one line covers the
// file wherever it sits.
func TestCovIgnoreBareNameReachesAnyDepth(t *testing.T) {
	r := ignoreDialectRepo(t, "NOTES.md")
	assertCountsAsChange(t, r, false, map[string]string{
		"packages/core/NOTES.md":          "top level\n",
		"packages/core/docs/api/NOTES.md": "three folders down\n",
	})
	assertCountsAsChange(t, r, true, map[string]string{
		"packages/core/docs/api/README.md": "a different name\n",
	})
}

// TestCovIgnoreNestedFolderPatternCoversEverythingUnderIt: a folder named with
// a path, rather than by its bare name, excludes that one folder and
// everything below it while its siblings keep counting.
func TestCovIgnoreNestedFolderPatternCoversEverythingUnderIt(t *testing.T) {
	r := ignoreDialectRepo(t, "docs/api/")
	assertCountsAsChange(t, r, false, map[string]string{
		"packages/core/docs/api/v1.md":          "generated\n",
		"packages/core/docs/api/nested/v2.md":   "generated deeper\n",
		"packages/core/docs/api/nested/v3.json": "generated deeper still\n",
	})
	assertCountsAsChange(t, r, true, map[string]string{
		"packages/core/docs/guide.md": "written by hand\n",
	})
}

// TestCovIgnoreEscapedBangNamesALiteralCharacter: a pattern beginning "\!"
// names a file whose name begins with "!" rather than re-including anything,
// which is the only way to write the one and mean the other.
func TestCovIgnoreEscapedBangNamesALiteralCharacter(t *testing.T) {
	r := ignoreDialectRepo(t, `\!urgent.md`)
	assertCountsAsChange(t, r, false, map[string]string{
		"packages/core/!urgent.md": "a file whose name starts with a bang\n",
	})
	assertCountsAsChange(t, r, true, map[string]string{
		"packages/core/urgent.md": "the same name without the bang\n",
	})
}

// TestCovIgnoreCommentsAloneSayNothing: a list of nothing but comments and
// blank lines compiles to no rules at all, so the level is dropped and every
// file of the package keeps counting.
func TestCovIgnoreCommentsAloneSayNothing(t *testing.T) {
	r := ignoreDialectRepo(t, "# generated files go here one day", "   ", "")
	assertCountsAsChange(t, r, true, map[string]string{
		"packages/core/docs/guide.md": "still a change\n",
	})
}
