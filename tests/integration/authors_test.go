// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 44: authors in release records. The attribution the entry-format
// `authors` object adds to a changelog entry and a GitHub release body: where
// it appears, who it names, which commits it counts, how the configuration
// ladder and the flags decide it, and that two packages configured differently
// keep their own.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The people these fixtures release under. Distinct addresses, because the
// address is the identity the deduplication keys on.
const (
	adaName  = "Ada Lovelace"
	adaMail  = "ada@example.com"
	graceMsg = "Grace Hopper"
	graceMl  = "grace@example.com"
	botName  = "dependabot[bot]"
	botMail  = "49699333+dependabot[bot]@users.noreply.github.com"
)

// authorsConfig is the single-package fixture these scenarios share: one
// changelog, attribution configured however the scenario needs it.
func authorsConfig(authors *models.AuthorsConfig) models.File {
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		Enabled:           models.Bool(true),
		EntryFormatConfig: models.EntryFormatConfig{Authors: authors},
	}
	return cfg
}

// TestAuthorsOffByDefault: the feature is invisible until it is asked for. A
// repository that configures nothing records exactly what it recorded before,
// even though every commit now carries an author the planner resolved.
func TestAuthorsOffByDefault(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): add streaming")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")

	assert.Contains(t, entry, "- add streaming")
	assert.NotContains(t, entry, adaName, "nothing attributes the entry")
	assert.NotContains(t, entry, "### Authors")
	assert.NotContains(t, entry, "(by ")

	// An explicit "off" says the same thing, which is what lets a package
	// defeat a broader layer without inventing a fourth spelling of absence.
	r2 := harness.New(t)
	r2.WriteConfigModel(authorsConfig(&models.AuthorsConfig{Placement: "off"}))
	r2.SeedPackage("packages", "core")
	r2.CommitAs(adaName, adaMail, "feat(core): add streaming")
	r2.ReleaseOK()
	assert.Equal(t, entry, changelogOf(t, r2, "core"),
		"an explicit off is byte for byte the default")
}

// TestAuthorsInlinePlacement: the per-line suffix names the people behind that
// one line, the git author first and the Co-authored-by trailers after, and
// each line carries only its own.
func TestAuthorsInlinePlacement(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{Placement: "inline"}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): add streaming\n\n"+
		"Co-authored-by: "+graceMsg+" <"+graceMl+">\n")
	r.WriteFile("packages/core/fix.txt", "x")
	r.CommitAs(graceMsg, graceMl, "fix(core): close leak")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")

	assert.Contains(t, entry, "- add streaming (by "+adaName+", "+graceMsg+")",
		"the git author leads, then the trailer")
	assert.Contains(t, entry, "- close leak (by "+graceMsg+")",
		"a line carries only its own authors")
	assert.NotContains(t, entry, "### Authors", "inline alone writes no section")
}

// TestAuthorsSectionPlacement: the section is one deduplicated list under its
// own heading, written into the changelog file and into the GitHub release
// body ahead of the release details, and a name that is not ASCII survives the
// whole path from git to the record.
func TestAuthorsSectionPlacement(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := authorsConfig(&models.AuthorsConfig{Placement: "section", Title: "Thanks to"})
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		EntryFormatConfig: models.EntryFormatConfig{
			Authors: &models.AuthorsConfig{Placement: "section", Title: "Thanks to"},
			Footer:  []models.EntryLine{{Line: []string{"---", "Released by dispat."}}},
		},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")

	const zoe = "Zoé Müller"
	r.CommitAs(zoe, "zoe@example.com", "feat(core): add streaming")
	r.WriteFile("packages/core/again.txt", "x")
	r.CommitAs(zoe, "zoe@example.com", "fix(core): close leak")
	r.WriteFile("packages/core/third.txt", "x")
	r.CommitAs(adaName, adaMail, "fix(core): another fix")

	r.ReleaseOK()

	entry := changelogOf(t, r, "core")
	// One entry per person, however many commits they wrote, in the order the
	// release collected its units: newest commit first.
	assert.Contains(t, entry, "### Thanks to\n\n- "+adaName+"\n- "+zoe+"\n")

	type ghRelease struct {
		Body string `json:"body"`
	}
	all := decodeAll[ghRelease](t, bodies())
	require.Len(t, all, 1)
	// The section belongs to the entry, so it comes after the sections it
	// attributes and before the footer that closes the body. The footer
	// staying last is what keeps self-update's "---" cut where it was.
	assertOrderedIn(t, all[0].Body, "### Fixes", "### Thanks to", "- "+zoe, "---",
		"Released by dispat.")
}

// TestAuthorsAllCommitsIncludeInvalid: `commits: all` reaches the whole
// window, so a commit whose message is not a release record still credits the
// person who wrote it. `ccme` counts only the commits behind the entry's own
// lines, which is the difference the two settings exist to express.
func TestAuthorsAllCommitsIncludeInvalid(t *testing.T) {
	seed := func(r *harness.Repo) {
		r.SeedPackage("packages", "core")
		r.CommitAs(adaName, adaMail, "feat(core): add streaming")
		r.WriteFile("packages/core/untracked-work.txt", "x")
		r.CommitAs(graceMsg, graceMl, "wip: not a release record at all")
	}

	ccme := harness.New(t)
	ccme.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Commits: "ccme"}))
	seed(ccme)
	ccme.ReleaseOK()
	ccmeEntry := changelogOf(t, ccme, "core")
	assert.Contains(t, ccmeEntry, "- "+adaName)
	assert.NotContains(t, ccmeEntry, graceMsg,
		"ccme names the people behind the lines above the section")

	all := harness.New(t)
	all.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Commits: "all"}))
	seed(all)
	all.ReleaseOK()
	allEntry := changelogOf(t, all, "core")
	assert.Contains(t, allEntry, "- "+adaName)
	assert.Contains(t, allEntry, "- "+graceMsg,
		"all reaches the commit that carried no record")
}

// TestAuthorsOnlyInvalidCommitsStillCredits: the package releases for a reason
// no commit message grouped — an exact pin — so there is no line to attribute
// and, under `all`, still somebody to credit.
func TestAuthorsOnlyInvalidCommitsStillCredits(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Commits: "all"}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "chore(core): groundwork\n\nRelease-As: 0.1.0\n")

	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	entry := changelogOf(t, r, "core")
	assert.Contains(t, entry, "### Authors\n\n- "+adaName+"\n",
		"an entry with no grouped lines still says who moved the repository")
}

// TestAuthorsFiltersAndFormat: include admits, exclude refuses and wins, the
// patterns reach every way of naming a person, and `format: username` writes
// the local part of the address.
func TestAuthorsFiltersAndFormat(t *testing.T) {
	seed := func(r *harness.Repo) {
		r.SeedPackage("packages", "core")
		r.CommitAs(adaName, adaMail, "feat(core): add streaming")
		r.WriteFile("packages/core/bump.txt", "x")
		r.CommitAs(botName, botMail, "fix(core): bump a dependency")
		r.WriteFile("packages/core/local.txt", "x")
		r.CommitAs("Local Only", "local-only", "fix(core): no at sign in the address")
	}

	excluded := harness.New(t)
	excluded.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Include: []string{"*"}, Exclude: []string{"*[bot]*"}}))
	seed(excluded)
	excluded.ReleaseOK()
	entry := changelogOf(t, excluded, "core")
	assert.Contains(t, entry, "- "+adaName)
	assert.Contains(t, entry, "- Local Only")
	assert.NotContains(t, entry, botName, "exclude wins over a wide-open include")

	included := harness.New(t)
	included.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Format: "username", Include: []string{"*@example.com"}}))
	seed(included)
	included.ReleaseOK()
	entry = changelogOf(t, included, "core")
	assert.Contains(t, entry, "### Authors\n\n- ada\n",
		"the username is the local part, and the email pattern admitted only Ada")
	assert.NotContains(t, entry, "Local Only")
	assert.NotContains(t, entry, botName)

	// An address with no "@" is not well formed, but it is what the commit
	// says: the username form returns it whole rather than attributing to
	// nobody.
	noAt := harness.New(t)
	noAt.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Format: "username", Include: []string{"local-only"}}))
	seed(noAt)
	noAt.ReleaseOK()
	assert.Contains(t, changelogOf(t, noAt, "core"), "- local-only")
}

// TestAuthorsLadderAndFlags: the object rides the configuration ladder like
// every other entry-format key — a package overrides its space field by field
// — and the `dispat changelog` flags beat whatever the ladder resolved.
func TestAuthorsLadderAndFlags(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{
		Enabled: models.Bool(true),
		EntryFormatConfig: models.EntryFormatConfig{Authors: &models.AuthorsConfig{
			Placement: "both", Title: "Contributors"}},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"quiet": {Changelog: &models.ChangelogConfig{
			EntryFormatConfig: models.EntryFormatConfig{
				Authors: &models.AuthorsConfig{Placement: "off"}}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "loud")
	r.SeedPackage("packages", "quiet")
	r.CommitAs(adaName, adaMail, "feat(loud,quiet): first release of both")

	r.ReleaseOK()

	loud := changelogOf(t, r, "loud")
	assert.Contains(t, loud, "(by "+adaName+")", "the root layer's both writes the suffix")
	assert.Contains(t, loud, "### Contributors", "and the section, under the inherited title")

	quiet := changelogOf(t, r, "quiet")
	assert.NotContains(t, quiet, adaName, "a nearer off defeats the broader both")
	assert.NotContains(t, quiet, "### Contributors")

	// The flags: a second window, written by the step command, overriding both
	// the placement and the title the configuration resolved.
	r.WriteFile("packages/loud/more.txt", "x")
	r.CommitAs(graceMsg, graceMl, "fix(loud): more work")
	res := r.Command("changelog", "--package", "loud",
		"--authors", "section", "--authors-format", "username", "--authors-title", "Written by")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	loud = changelogOf(t, r, "loud")
	assert.Contains(t, loud, "### Written by\n\n- grace\n", "the flags beat the configuration")
	assert.NotContains(t, loud, "- more work (by ", "--authors section turns the suffix off")
}

// TestAuthorsGitHubCommandFlags: the same six flags on `dispat github`, where
// they have to reach the release body the step command posts rather than a
// file it writes.
func TestAuthorsGitHubCommandFlags(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): add streaming")
	r.WriteFile("packages/core/bump.txt", "x")
	r.CommitAs(botName, botMail, "fix(core): bump a dependency")

	res := r.CommandEnv([]string{"DISPAT_EXPORT_GITHUB="}, "github", "--package", "core",
		"--authors", "both", "--authors-commits", "all", "--authors-title", "Written by",
		"--authors-exclude", "*[bot]*")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	type ghRelease struct {
		Body string `json:"body"`
	}
	all := decodeAll[ghRelease](t, bodies())
	require.Len(t, all, 1)
	assert.Contains(t, all[0].Body, "### Written by\n\n- "+adaName+"\n",
		"the flags reach the body the command posts")
	assert.Contains(t, all[0].Body, "- add streaming (by "+adaName+")")
	assert.NotContains(t, all[0].Body, botName, "--authors-exclude reaches the section")
	assert.NotContains(t, all[0].Body, "(by "+botName+")",
		"and the inline suffix too")
}

// TestAuthorsFlagRejectsAnUnknownValue: a flag and a config key naming the
// same setting fail in the same words, before anything is planned or written.
func TestAuthorsFlagRejectsAnUnknownValue(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first")

	res := r.Command("changelog", "--authors", "everywhere")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "want one of off, inline, section, both")
	assert.NoFileExists(t, r.Path("packages", "core", "CHANGELOG.md"),
		"the refusal precedes any writing")

	res = r.Command("github", "--authors-format", "handle")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "want one of fullname, username")
}

// TestAuthorsSeparateReleasers: two packages releasing to one GitHub
// repository under different attribution settings must each get their own
// body. They share every field a releaser is addressed by, so only the entry
// format tells their policies apart — which is the end-to-end proof that the
// authors settings reach the releaser key.
func TestAuthorsSeparateReleasers(t *testing.T) {
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	cfg.Packages = map[string]models.PackageConfig{
		"credited": {GitHub: &models.GitHubConfig{
			EntryFormatConfig: models.EntryFormatConfig{
				Authors: &models.AuthorsConfig{Placement: "section", Title: "Credits"}}}},
		"anonymous": {GitHub: &models.GitHubConfig{
			EntryFormatConfig: models.EntryFormatConfig{
				Authors: &models.AuthorsConfig{Placement: "off"}}}},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "credited")
	r.SeedPackage("packages", "anonymous")
	r.CommitAs(adaName, adaMail, "feat(credited,anonymous): first release of both")

	r.ReleaseOK()

	type ghRelease struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	byTag := map[string]string{}
	for _, rel := range decodeAll[ghRelease](t, bodies()) {
		byTag[rel.TagName] = rel.Body
	}
	require.Len(t, byTag, 2, "one release per package")

	assert.Contains(t, byTag["credited@0.1.0"], "### Credits\n\n- "+adaName+"\n")
	assert.NotContains(t, byTag["anonymous@0.1.0"], adaName,
		"the two packages must not share one releaser's format")
	assert.NotContains(t, byTag["anonymous@0.1.0"], "### Credits")
}

// TestAuthorsCorrectionsAndSuppression: attribution follows the line it
// belongs to. A restatement is attributed to whoever restated it, and a
// reverted entry takes its attribution out of the notes with it — while the
// window, which `all` reads, still knows the work was done.
func TestAuthorsCorrectionsAndSuppression(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "both", Commits: "ccme"}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): stremaing")
	typo := r.Git("rev-parse", "HEAD")

	r.WriteFile("packages/core/fix.txt", "x")
	r.CommitAs(graceMsg, graceMl, "feat(core): streaming\n\nEdits: "+typo+"\n")

	r.ReleaseOK()
	entry := changelogOf(t, r, "core")

	// §7.4.2 renders the restatement once, as the restating unit's entry. The
	// people it names are that commit's, not the corrected commit's.
	assert.Contains(t, entry, "- streaming (corrects ")
	assert.Contains(t, entry, "(by "+graceMsg+")")
	assert.NotContains(t, entry, "- stremaing", "the corrected line is gone")

	// A revert in the next window: both entries leave the notes (§7.3), so
	// ccme has nobody to name and the section is not written at all.
	r.WriteFile("packages/core/bad.txt", "x")
	r.CommitAs(adaName, adaMail, "feat(core): a bad idea")
	bad := r.Git("rev-parse", "HEAD")
	r.Remove("packages/core/bad.txt")
	r.CommitAs(graceMsg, graceMl, "revert(core): a bad idea\n\nReverts: "+bad+"\n")

	r.ReleaseOK()
	entry = changelogOf(t, r, "core")
	assert.NotContains(t, entry, "a bad idea", "§7.3 suppresses both entries")
}

// TestAuthorsPrereleaseFreshWindow: the attribution narrows exactly as the
// notes do. A prerelease credits whoever wrote its own changeset, and the
// stable graduation collecting the train credits everyone in it.
func TestAuthorsPrereleaseFreshWindow(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{
		Placement: "section", Commits: "all"}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core)%beta: first beta work")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0-beta.0"), "tags: %v", r.TagList())

	first := changelogOf(t, r, "core")
	assert.Contains(t, first, "- "+adaName)
	assert.NotContains(t, first, graceMsg)

	r.WriteFile("packages/core/more.txt", "x")
	r.CommitAs(graceMsg, graceMl, "fix(core)%beta: second beta work")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0-beta.1"), "tags: %v", r.TagList())

	second := changelogOf(t, r, "core")
	betaOne := second[:len(second)-len(first)+len("# Changelog\n\n")]
	assert.Contains(t, betaOne, graceMsg, "beta.1 credits its own changeset")
	assert.NotContains(t, betaOne, adaName,
		"and not the people beta.0 already credited")

	// The graduation documents the whole train, so it credits the whole train.
	r.CommitEmpty("chore(core)%beta>stable: graduate")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())

	stable := changelogOf(t, r, "core")
	graduation := stable[:len(stable)-len(second)+len("# Changelog\n\n")]
	assert.Contains(t, graduation, adaName, "the stable entry collects the whole window")
	assert.Contains(t, graduation, graceMsg)
}

// TestAuthorsPreviewRendersTheBlocks: `dispat preview` prints the record
// bodies a release would write, so it gains the attribution by construction.
func TestAuthorsPreviewRendersTheBlocks(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{Placement: "both"}))
	r.SeedPackage("packages", "core")
	r.CommitAs(adaName, adaMail, "feat(core): add streaming")

	res := r.Command("preview", "--changelog")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "- add streaming (by "+adaName+")")
	assert.Contains(t, res.Stdout, "### Authors")
}
