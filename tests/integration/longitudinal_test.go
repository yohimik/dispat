package integration

// Goal 38: the longitudinal fence. One repository modelled on dispat's own
// shape — a declared version group spanning two spaces, one member publishing
// through wired step commands, a caret provider outside the group, alias
// tags, a real bare remote and the GitHub recorder — released through a whole
// prerelease-train lifecycle: stable baseline, rc.0, rc.1, rc.2, graduation,
// convergence. Every step asserts the durable records (tags, every changelog
// entry, every GitHub body) and the reported plan (graph verdicts, reasons,
// ownCommits, diagnostics), because the five planner bugs that shipped before
// 1.0.0 all lived on the seam between train-wide accounting and
// fresh-changeset reporting, and only a sequence exercises that seam: a
// single release cannot tell the train's history from its changeset.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// rideLine is the entry body of a group member released only to keep the
// shared version aligned.
const rideLine = "No changes: a version bump to keep the versioning group on one version."

// longitudinalRepo is the dispat-shaped fixture: a "cli" group of core
// (packages/, publishing through wired changelog+commit steps) and app
// (services/, finalize-recorded, with alias tags), plus an independent
// provider ccme (shared/) that app consumes.
func longitudinalRepo(t *testing.T, apiURL string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts = map[string]models.Script{
		"build":   {echoBuild},
		"publish": {`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`},
		// core's records are its own publish leg's work, exactly as this
		// repository publishes itself: the wired steps hold the record to the
		// run's plan, and the exported commit keeps finalize off core's tag.
		"record": {
			bin + " changelog",
			bin + " commit --tag --push",
			`echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"`,
		},
	}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"cli": {Versioning: models.VersioningFixed}}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, VersionGroup: "cli",
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
		"svc": {Path: models.PathList{"services"}, VersionGroup: "cli",
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}},
			AliasTags: []models.AliasTagConfig{
				{Format: "cli-v{version}"},
				{Format: "cli-v{major}", Moving: true, Channels: []string{"stable"}},
			}},
		"shared": {Path: models.PathList{"shared"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}}},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "ccme"}}
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), Owner: "acme", Repo: "mono",
		APIURL: apiURL, TokenEnv: "DISPAT_IT_TOKEN",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "app")
	r.SeedPackage("shared", "ccme")
	return r
}

// spacedChangelog reads one package's changelog off the working tree.
func spacedChangelog(t *testing.T, r *harness.Repo, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path(append(parts, "CHANGELOG.md")...))
	require.NoError(t, err)
	return string(data)
}

// entryOf cuts one entry (from its "## tag (" header to the next "## " or the
// end) out of a changelog, so an assertion about rc.1's body cannot pass on
// content that actually sits in rc.0's.
func entryOf(t *testing.T, log, tag string) string {
	t.Helper()
	marker := "## " + tag + " ("
	start := strings.Index(log, marker)
	require.NotEqual(t, -1, start, "no entry for %s in:\n%s", tag, log)
	rest := log[start+len(marker):]
	if next := strings.Index(rest, "\n## "); next >= 0 {
		rest = rest[:next]
	}
	return rest
}

// ownCommitsOf is the graph line's ownCommits count.
func ownCommitsOf(e harness.Event) int {
	v, _ := e["ownCommits"].(float64)
	return int(v)
}

// assertCleanWiring fails on any of the step-wiring drift codes: the wired
// records must hold to the run's plan at every step of the train.
func assertCleanWiring(t *testing.T, res harness.RunResult) {
	t.Helper()
	assert.False(t, harness.HasCode(res.Events, "W228"), "wired record drift: %s", res.Stdout)
	assert.False(t, harness.HasCode(res.Events, "E219"), "wired record refusal: %s", res.Stdout)
	assert.False(t, harness.HasCode(res.Events, "W193"), "nothing here is a catch-up: %s", res.Stdout)
}

// TestLongitudinalGroupTrainLifecycle walks the whole sequence. The steps
// build on one another — rc.1's assertions only mean something over rc.0's
// history — so this is one test, not a table.
func TestLongitudinalGroupTrainLifecycle(t *testing.T) {
	type ghRelease struct {
		TagName    string `json:"tag_name"`
		Body       string `json:"body"`
		Prerelease bool   `json:"prerelease"`
	}
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r := longitudinalRepo(t, srv.URL)

	// --- Step 0: the stable baseline. ---
	r.Commit("feat(core,app,ccme): first release")
	res := r.ReleaseOK()
	assertCleanWiring(t, res)
	for _, tag := range []string{"core@0.1.0", "app@0.1.0", "ccme@0.1.0"} {
		require.True(t, r.HasTag(tag), "tags: %v", r.TagList())
	}
	require.True(t, r.HasTag("cli-v0.1.0"), "app's exact alias; tags: %v", r.TagList())
	require.True(t, r.HasTag("cli-v0"), "app's moving alias; tags: %v", r.TagList())
	aliasAtBaseline := r.Git("rev-list", "-n1", "cli-v0")

	// --- Step 1: core boards an rc train; the group rides with it. ---
	r.CommitEmpty("feat(core)%rc: start the train")
	res = r.ReleaseOK()
	assertCleanWiring(t, res)
	require.True(t, r.HasTag("core@0.2.0-rc.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.2.0-rc.0"), "the group shares the train; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("ccme@"), "the independent provider stays put")
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "app"),
		"the ride is explained: %s", res.Stdout)

	core := harness.GraphLine(res.Events, "core")
	assert.Equal(t, "direct", core.Str("reason"))
	assert.Equal(t, 1, ownCommitsOf(core))
	assert.Equal(t, "0.1.0 -> 0.2.0-rc.0", core.Str("version"))
	app := harness.GraphLine(res.Events, "app")
	assert.Equal(t, "fixed group versioning", app.Str("reason"))
	assert.Equal(t, 0, ownCommitsOf(app), "a rider adds nothing of its own")

	coreEntry := entryOf(t, spacedChangelog(t, r, "packages", "core"), "core@0.2.0-rc.0")
	assert.Contains(t, coreEntry, "start the train")
	appEntry := entryOf(t, spacedChangelog(t, r, "services", "app"), "app@0.2.0-rc.0")
	assert.Contains(t, appEntry, rideLine, "a rider's entry names the ride")

	// --- Step 2: the train continues; rc.1 documents only its own changeset.
	// This is the seam every shipped planner bug lived on: the plan's
	// accounting spans the train, the records and the graph must not. ---
	r.CommitEmpty("fix(core)%rc: more on the train")

	// Through the status command first — the fresh-changeset count is what
	// the operator is shown, and it was the "misleading 122" before 1.0.0.
	status := r.StatusOK()
	core = harness.GraphLine(status.Events, "core")
	assert.Equal(t, 1, ownCommitsOf(core),
		"ownCommits is the fresh changeset, not the train's history: %s", status.Stdout)
	assert.Equal(t, "direct", core.Str("reason"), "fresh own work explains rc.1")
	assert.Equal(t, "0.2.0-rc.0 -> 0.2.0-rc.1", core.Str("version"),
		"the baseline is the train's head, not the stable line")

	res = r.ReleaseOK()
	assertCleanWiring(t, res)
	require.True(t, r.HasTag("core@0.2.0-rc.1"), "one shared counter; tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.2.0-rc.1"), "tags: %v", r.TagList())

	coreLog := spacedChangelog(t, r, "packages", "core")
	rc1 := entryOf(t, coreLog, "core@0.2.0-rc.1")
	assert.Contains(t, rc1, "more on the train")
	assert.NotContains(t, rc1, "start the train",
		"rc.1 must not repeat what rc.0 already published")
	appRc1 := entryOf(t, spacedChangelog(t, r, "services", "app"), "app@0.2.0-rc.1")
	assert.Contains(t, appRc1, rideLine,
		"a ride with train history is still a ride, not an empty body")

	// --- Step 3: the caret provider moves; the blast continues the train.
	// app's cause is fresh propagation, core is the rider now, and app's
	// entry carries the dependency movement rather than the bare ride line. ---
	r.CommitEmpty("fix(ccme)^: repair underneath")
	res = r.ReleaseOK()
	assertCleanWiring(t, res)
	require.True(t, r.HasTag("ccme@0.1.1"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("core@0.2.0-rc.2"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.2.0-rc.2"), "tags: %v", r.TagList())

	app = harness.GraphLine(res.Events, "app")
	assert.Equal(t, "propagated from ccme", app.Str("reason"))
	assert.Equal(t, 0, ownCommitsOf(app))
	assert.True(t, harness.HasCodeForPackage(res.Events, "W234", "core"),
		"core rides its own group this time: %s", res.Stdout)

	appRc2 := entryOf(t, spacedChangelog(t, r, "services", "app"), "app@0.2.0-rc.2")
	assert.Contains(t, appRc2, "- ccme: 0.1.0 -> 0.1.1",
		"a ride that picks up a provider's movement has a dependencies section")
	assert.NotContains(t, appRc2, rideLine)
	coreRc2 := entryOf(t, spacedChangelog(t, r, "packages", "core"), "core@0.2.0-rc.2")
	assert.Contains(t, coreRc2, rideLine)

	// --- Step 4: graduation. The stable entry collects the whole train. ---
	r.CommitEmpty("fix(core)%rc>stable: graduate the train")
	res = r.ReleaseOK()
	assertCleanWiring(t, res)
	require.True(t, r.HasTag("core@0.2.0"), "graduated; tags: %v", r.TagList())
	require.True(t, r.HasTag("app@0.2.0"), "the whole group graduates; tags: %v", r.TagList())

	coreStable := entryOf(t, spacedChangelog(t, r, "packages", "core"), "core@0.2.0")
	assertOrderedIn(t, coreStable, "### Features", "start the train",
		"### Fixes", "graduate the train", "more on the train")
	appStable := entryOf(t, spacedChangelog(t, r, "services", "app"), "app@0.2.0")
	assert.Contains(t, appStable, "- ccme: 0.1.0 -> 0.1.1",
		"the graduation still documents what moved underneath during the train")

	require.True(t, r.HasTag("cli-v0.2.0"), "tags: %v", r.TagList())
	assert.NotEqual(t, aliasAtBaseline, r.Git("rev-list", "-n1", "cli-v0"),
		"the moving alias follows the graduation")
	assert.Equal(t, r.Git("rev-list", "-n1", "app@0.2.0"), r.Git("rev-list", "-n1", "cli-v0"))

	// --- Step 5: convergence. The records exist, so nothing releases, and
	// three prereleases of history have not bent the baselines. ---
	res = r.ReleaseOK()
	assertCleanWiring(t, res)
	assert.Equal(t, 5, r.TagCount("core@"), "converged: %v", r.TagList())
	assert.Equal(t, 5, r.TagCount("app@"))
	assert.Equal(t, 2, r.TagCount("ccme@"))
	status = r.StatusOK()
	for _, pkg := range []string{"core", "app", "ccme"} {
		line := harness.GraphLine(status.Events, pkg)
		assert.Equal(t, "unchanged", line.Str("message"), "%s after convergence: %s", pkg, status.Stdout)
	}

	// --- The GitHub side of the same sequence, asserted once over the whole
	// run: every release got a body, the prerelease flag followed the
	// channel, and the bodies carry the same windowing as the changelogs. ---
	releases := decodeAll[ghRelease](t, bodies())
	byTag := map[string]ghRelease{}
	for _, rel := range releases {
		byTag[rel.TagName] = rel
	}
	require.Len(t, releases, 12, "3 + 2 + 2 + 3 + 2 releases, every one recorded")
	for tag, rel := range byTag {
		assert.NotEmpty(t, strings.TrimSpace(rel.Body), "%s must not have an empty body", tag)
		assert.Equal(t, strings.Contains(tag, "-rc."), rel.Prerelease,
			"%s: the prerelease flag follows the version", tag)
	}
	assert.Contains(t, byTag["app@0.2.0-rc.1"].Body, rideLine)
	assert.Contains(t, byTag["app@0.2.0-rc.2"].Body, "- ccme: 0.1.0 -> 0.1.1")
	rc1Body := byTag["core@0.2.0-rc.1"].Body
	assert.Contains(t, rc1Body, "more on the train")
	assert.NotContains(t, rc1Body, "start the train")
	assertOrderedIn(t, byTag["core@0.2.0"].Body, "### Features", "start the train",
		"### Fixes", "graduate the train", "more on the train")
}
