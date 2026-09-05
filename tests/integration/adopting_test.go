package integration

// Area 7, continued: adopting dispat in a repository that already has a
// history and already has changelogs.
//
// The guarantee is one sentence, and everything here is a way of failing it:
// dispat never rewrites content it did not write, and never moves it. A
// changelog that predates dispat keeps its front matter at the head of the
// file where front matter has to be, keeps a title written in somebody else's
// words instead of growing a second one underneath dispat's, keeps its badge
// row, and keeps whatever line endings and byte-order mark it was checked out
// with. What a first release over an existing history renders, and what
// happens when a hand-written heading collides with the tag dispat is about to
// write, are pinned here too: both are things an operator meets on day one and
// nowhere afterwards.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// adoptShapes are the changelogs an existing repository turns out to have,
// each with the run of bytes that must still head the file afterwards. They
// are seeded into one repository, one package each, so a single release run
// answers for all of them at once.
var adoptShapes = []struct {
	pkg      string
	existing string
	// keep is the leading run that must survive at the very top of the file.
	keep string
	// titled says the file heads with the title dispat itself renders, which
	// is the one shape whose top is re-rendered rather than preserved.
	titled bool
}{{
	pkg:      "titled",
	existing: "# Changelog\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
	keep:     "# Changelog\n",
	titled:   true,
}, {
	pkg:      "foreign",
	existing: "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- something old\n",
	keep:     "# Change Log\n",
}, {
	pkg: "frontmatter",
	existing: "---\ntitle: Releases\nsidebar_position: 3\n---\n\n# Change Log\n\n" +
		"## v1.2.0 (2024-01-01)\n\n- something old\n",
	keep: "---\ntitle: Releases\nsidebar_position: 3\n---\n\n# Change Log\n",
}, {
	pkg: "badged",
	existing: "# Changelog\n\n[![build](https://img.shields.io/x)](https://example.test)\n\n" +
		"## v1.2.0 (2024-01-01)\n\n- something old\n",
	keep:   "# Changelog\n",
	titled: true,
}, {
	pkg:      "crlf",
	existing: "\ufeff# Changelog\r\n\r\n## v1.2.0 (2024-01-01)\r\n\r\n- something old\r\n",
	keep:     "\ufeff# Changelog\n",
	titled:   true,
}}

// TestRecordsAdoptedChangelogsKeepTheirContent: the whole migration guarantee
// in one run. Every shape an existing changelog comes in is seeded into its
// own package, one release is made, and each file is checked for the two
// things that matter: the bytes that predate dispat are still there, and the
// new entry is above the entries that predate it rather than above the file.
func TestRecordsAdoptedChangelogsKeepTheirContent(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))

	scopes := make([]string, 0, len(adoptShapes))
	for _, s := range adoptShapes {
		r.SeedPackage("packages", s.pkg)
		r.WriteFile("packages/"+s.pkg+"/CHANGELOG.md", s.existing)
		scopes = append(scopes, s.pkg)
	}
	r.Commit("feat(" + strings.Join(scopes, ",") + "): the first release under dispat")
	r.ReleaseOK()

	for _, s := range adoptShapes {
		t.Run(s.pkg, func(t *testing.T) {
			log := changelogOf(t, r, s.pkg)
			tag := "## " + s.pkg + "@0.1.0 ("

			assert.True(t, strings.HasPrefix(log, s.keep),
				"the file's own head must survive byte for byte:\n%q", log)
			assertOrderedIn(t, log, tag, "## v1.2.0 (", "- something old")
			assert.Equal(t, 1, strings.Count(log, "# Change"),
				"the file must not grow a second title:\n%s", log)

			if s.titled {
				// The title dispat renders is recognised and written again,
				// with the entry one blank line under it — what the writer has
				// always done. Content between the title and the first entry
				// is not rewritten either; it simply stays above the entries
				// it was already above.
				assert.True(t, strings.HasPrefix(log, s.keep+"\n"+tag), "%q", log)
			} else {
				// An unrecognised head is the file's preamble. It stays where
				// it is, dispat's own title is never written, and the entry is
				// inserted below it across the ordinary entry seam.
				assert.NotContains(t, log, "# Changelog")
				assert.True(t, strings.HasPrefix(log, s.keep+"\n\n"+tag), "%q", log)
			}
		})
	}

	// The CRLF file keeps its endings below the seam, and its byte-order mark
	// exactly once, at the very top.
	crlf := changelogOf(t, r, "crlf")
	assert.Contains(t, crlf, "## v1.2.0 (2024-01-01)\r\n\r\n- something old\r\n",
		"the old content is never re-terminated")
	assert.Equal(t, 1, strings.Count(crlf, "\ufeff"), "one mark, at the head of the file")
	assert.Equal(t, 1, strings.Count(crlf, "## crlf@0.1.0 ("),
		"a CRLF title must not defeat the match and write a second entry")
	assert.Contains(t, changelogOf(t, r, "badged"), "[![build](https://img.shields.io/x)]",
		"a badge row is preserved, wherever it ends up")
}

// TestRecordsAdoptionConverges: the second release over an adopted file goes
// where the first one did — between the preamble and the entries — and a
// re-run of the same release changes nothing at all. Adoption that only worked
// once would be a migration rather than a guarantee.
func TestRecordsAdoptionConverges(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	const preamble = "---\ntitle: Releases\n---\n\n# Change Log\n"
	r.WriteFile("packages/core/CHANGELOG.md", preamble+"\n## v1.2.0 (2024-01-01)\n\n- something old\n")

	r.Commit("feat(core): the first release under dispat")
	r.ReleaseOK()
	first := changelogOf(t, r, "core")

	// Nothing pending: the run rewrites nothing.
	r.ReleaseOK()
	assert.Equal(t, first, changelogOf(t, r, "core"), "a quiet run leaves the file alone")

	r.CommitEmpty("fix(core): close a leak")
	r.ReleaseOK()
	log := changelogOf(t, r, "core")
	assert.True(t, strings.HasPrefix(log, preamble), "the front matter is still front matter:\n%s", log)
	assertOrderedIn(t, log, "## core@0.1.1 (", "## core@0.1.0 (", "## v1.2.0 (", "- something old")
}

// TestRecordsStandaloneChangelogPreservesThePreambleToo: `dispat changelog` is
// the same writer through a different door, and a flow that writes its entries
// from a stage script must adopt a file exactly as the release recorder does.
func TestRecordsStandaloneChangelogPreservesThePreamble(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	const preamble = "---\ntitle: Releases\n---\n\n# Change Log\n"
	r.WriteFile("packages/core/CHANGELOG.md", preamble+"\n## v1.2.0 (2024-01-01)\n\n- old\n")
	r.Commit("feat(core): first feature")

	res := r.Command("changelog", "--package", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	log := changelogOf(t, r, "core")
	assert.True(t, strings.HasPrefix(log, preamble+"\n\n## core@0.1.0 ("), "%q", log)
	assert.NotContains(t, log, "# Changelog")

	// And the release that follows finds the entry and skips it, so the two
	// doors do not each write one.
	res = r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W226", "core"))
	assert.Equal(t, log, changelogOf(t, r, "core"), "the release rewrote nothing")
}

// TestRecordsFirstReleaseCoversTheWholeHistory: the first release of a package
// that has never been released has no baseline to start from, so its window is
// the whole history and its entry documents every conventional commit in it.
// That is current behavior rather than a preference: a repository adopting
// dispat mid-life gets one large first entry, and the way to a smaller one is
// a baseline tag under the configured tagFormat before the first run.
func TestRecordsFirstReleaseCoversTheWholeHistory(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): the oldest feature")
	r.CommitEmpty("fix(core): an old fix")
	r.CommitEmpty("chore(core): something that releases nothing")
	r.CommitEmpty("feat(core): the newest feature")

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	entry := entryOf(t, changelogOf(t, r, "core"), "core@0.1.0")
	assertOrderedIn(t, entry,
		"### Features", "- the newest feature", "- the oldest feature",
		"### Fixes", "- an old fix")
	assert.NotContains(t, entry, "something that releases nothing",
		"a type that bumps nothing reaches no section")

	// The other half: with a baseline tag under the package's tag format, the
	// window starts there and only what came after it is documented.
	fresh := singlePackageRepo(t, echoBuild)
	fresh.Commit("feat(core): history nobody wants in the entry")
	fresh.Git("tag", "-a", "core@1.0.0", "-m", "the baseline of the adoption")
	fresh.CommitEmpty("fix(core): the first change under dispat")
	fresh.ReleaseOK()

	entry = entryOf(t, changelogOf(t, fresh, "core"), "core@1.0.1")
	assert.Contains(t, entry, "- the first change under dispat")
	assert.NotContains(t, entry, "history nobody wants",
		"a baseline tag is what cuts the first window")
}

// TestRecordsHandWrittenHeadingCollidesWithTheTag: the sharp edge of adopting
// a hand-written changelog under a tagFormat that matches how its headings
// were written. dispat recognises an entry by its heading, so a heading
// somebody wrote by hand for the version this release happens to be is read as
// an entry that already exists — the release goes through, the file is left
// alone, and the skip is reported under W226 rather than silently.
//
// It is a skip rather than an overwrite on purpose: the alternative is
// rewriting a line a person wrote, and the two cases are indistinguishable
// from inside the file.
func TestRecordsHandWrittenHeadingCollidesWithTheTag(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: models.PathList{"packages"}, Flow: buildPublish(), TagFormat: "v{version}",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	// A changelog whose headings were written in exactly the shape the tag
	// format produces, including the version this run is about to release.
	const existing = "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- written by hand\n"
	r.WriteFile("packages/core/CHANGELOG.md", existing)
	r.Commit("feat(core): the first release under dispat\n\nRelease-As: 1.2.0")

	res := r.ReleaseOK()

	assert.True(t, harness.HasCodeForPackage(res.Events, "W226", "core"),
		"the collision is reported under its own code, events:\n%s", res.Stdout)
	assert.True(t, r.HasTag("v1.2.0"), "the release itself goes through; tags: %v", r.TagList())
	assert.Equal(t, existing, changelogOf(t, r, "core"),
		"a heading somebody wrote by hand is never rewritten")

	// The next release is not affected: only the colliding version was ever
	// ambiguous.
	r.CommitEmpty("fix(core): the next one records normally")
	r.ReleaseOK()
	log := changelogOf(t, r, "core")
	assert.True(t, strings.HasPrefix(log, "# Change Log\n\n\n## v1.2.1 ("), "%q", log)
	assertOrderedIn(t, log, "## v1.2.1 (", "## v1.2.0 (", "- written by hand")
}

// TestRecordsAdoptedChangelogKeepsItsMode: the rewrite replaces the whole
// file, so a changelog checked in with permissions of its own keeps them —
// which matters most on the file dispat did not create.
func TestRecordsAdoptedChangelogKeepsItsMode(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.WriteFile("packages/core/CHANGELOG.md", "# Change Log\n\n## v1.2.0 (2024-01-01)\n\n- old\n")
	require.NoError(t, os.Chmod(r.Path("packages", "core", "CHANGELOG.md"), 0o600))
	r.Commit("feat(core): first release")

	r.ReleaseOK()

	info, err := os.Stat(r.Path("packages", "core", "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
