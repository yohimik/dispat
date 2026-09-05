package integration

// Goal 37: step commands wired into a running release. A library space whose
// publish stage is the record itself — changelog, commit, tag, push — through
// the step commands, with a consumer gated on the provider's publish, so the
// provider's tag is on the remote before the consumer's build asks for it.
// The wiring (W228/E219, the environment scoping and the leg's own tag masked
// from the step's replan) is what makes the composition safe; this file pins
// it through the compiled binary.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// stepRepo is the two-space fixture: a provider library in libs whose publish
// runs the wired steps, and a consumer in services whose build proves the
// provider's tag reached the remote mid-run.
func stepRepo(t *testing.T) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	bin, _ := harness.Build(t)
	bare := r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	cfg.Scripts = map[string]models.Script{
		"build": {echoBuild},
		// The wired steps: invoked from inside the leg, they read the run's
		// DISPAT_* environment, narrow to the leg's package and hold their
		// records to the run's own version and tag.
		"record": {bin + " changelog", bin + " commit --tag --push"},
		// The consumer's build gates on the provider's tag being fetchable:
		// this line failing is the whole reason isBuildWaitingPublish exists.
		// It also writes a tracked artifact into its own folder — the docs
		// version slice's shape — which the finalize commit must carry, so
		// the consumer's tag and the artifact land in one commit.
		"check-remote": {"git ls-remote --tags origin | grep -q \"$DISPAT_UPDATED_CORE_NEW_VERSION\" && echo remote-has-core >> ../../seen.log",
			"echo \"$DISPAT_NEW_VERSION\" > slice.txt"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"},
			IsBuildWaitingPublish: models.Bool(true),
			Flow:                  &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
		"svc": {Path: models.PathList{"services"},
			Flow: &models.SpaceFlowConfig{Build: []string{"check-remote", "build"}, Publish: []string{"build"}}},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "web")
	return r, bare
}

// TestStepsWiredIntoAPublishLeg: one run releases the graph with the
// provider's records made by its own publish leg. The provider's tag is on
// the remote before the consumer builds, the finalize phase finds the leg's
// work done and skips it, and a second run releases nothing.
func TestStepsWiredIntoAPublishLeg(t *testing.T) {
	r, _ := stepRepo(t)
	r.Commit("feat(core): the provider moves")
	r.WriteFile("services/web/w.txt", "w")
	r.Commit("feat(web): the consumer moves too")

	res := r.ReleaseOK()

	// The provider's leg made its own records: its tag exists, is on the
	// remote, and points at the leg's commit rather than the final release
	// commit (the step exported PACKAGE_CORE, so finalize skipped it, W223).
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("web@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W223"),
		"finalize finds the leg's tag already at its commit: %s", res.Stdout)
	assert.False(t, harness.HasCode(res.Events, "W228"), "no drift on the happy path")
	assert.False(t, harness.HasCode(res.Events, "E219"))

	// The consumer's build saw the provider's tag on the remote mid-run.
	seen, err := os.ReadFile(r.Path("seen.log"))
	require.NoError(t, err, "the consumer's remote check never ran")
	assert.Contains(t, string(seen), "remote-has-core")

	// The leg's commit and the finalize commit are distinct: the changelog
	// the step wrote is inside the tagged tree, not only in the final commit.
	tagged := strings.TrimSpace(r.Git("rev-list", "-1", "core@0.1.0"))
	head := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	assert.NotEqual(t, tagged, head, "the release commit sits above the leg's tagged commit")
	shown := r.Git("show", "core@0.1.0:packages/core/CHANGELOG.md")
	assert.Contains(t, shown, "core@0.1.0", "the tagged tree carries the changelog the step wrote")

	// A finalize-recorded package is the other half: no steps of its own, so
	// its tag lands on the release commit itself — one commit carrying both
	// the artifact its build wrote (the docs slice's shape) and the tag.
	webTagged := strings.TrimSpace(r.Git("rev-list", "-1", "web@0.1.0"))
	assert.Equal(t, head, webTagged, "the finalize-recorded tag points at the release commit")
	subject := strings.TrimSpace(r.Git("log", "-1", "--format=%s", "HEAD"))
	assert.Equal(t, "chore(release): web@0.1.0", subject,
		"the release commit names only the release it records; core's record is core's own commit")
	assert.Equal(t, "0.1.0", strings.TrimSpace(r.Git("show", "web@0.1.0:services/web/slice.txt")),
		"the build's tracked artifact is inside the commit the tag points at")

	// Convergence: the records exist, so a second run releases nothing.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "converged: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("web@"))
}

// TestStepsWiredSurviveAPartialRun: the crash-window shape. The provider's
// leg published — its step-made records are on the remote — but the consumer
// has not released yet; the next run must find the provider's records done
// instead of re-planning it a version it never earned, and still release the
// consumer at the version it was owed.
func TestStepsWiredSurviveAPartialRun(t *testing.T) {
	r, _ := stepRepo(t)
	r.Commit("feat(core): the provider moves")
	r.WriteFile("services/web/w.txt", "w")
	r.Commit("feat(web): the consumer moves too")

	res := r.Command("release", "-p", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	require.Equal(t, 0, r.TagCount("web@"), "the consumer waits for the next run")

	res = r.ReleaseOK()
	assert.True(t, r.HasTag("web@0.1.0"), "the consumer catches up; tags: %v", r.TagList())
	assert.Equal(t, 1, r.TagCount("core@"), "the provider is not re-released: %v", r.TagList())
	assert.False(t, harness.HasCode(res.Events, "E219"))
}

// TestStepsDuplicatedCollapseIntoSkips: a flow that lists the record steps
// twice must produce one record set. The second pass finds the entry written
// (W226) and the tag created (W223) and touches nothing — the idempotence
// that makes step-composed flows safe to over-specify.
func TestStepsDuplicatedCollapseIntoSkips(t *testing.T) {
	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	cfg.Scripts = map[string]models.Script{
		"build":  {echoBuild},
		"record": {bin + " changelog", bin + " commit --tag --push", bin + " changelog", bin + " commit --tag --push"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): begin")

	res := r.ReleaseOK()

	assert.Equal(t, 1, r.TagCount("core@"), "one tag, however many commit steps ran: %v", r.TagList())
	assert.True(t, harness.HasCode(res.Events, "W226"), "the second changelog step is a skip")
	assert.True(t, harness.HasCode(res.Events, "W223"), "the second commit step's tag is a skip")
	changelog, err := os.ReadFile(r.Path("packages/core/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(changelog), "## core@0.1.0"),
		"one entry, however many changelog steps ran:\n%s", changelog)
	assert.False(t, harness.HasCode(res.Events, "E219"))
}

// TestStepsWiredRecordTheRunsDependencies: the dependencies section of a
// wired record states the run's provider movements. The consumer's changelog
// step replans after the provider's tag has already landed; unmasked, that
// tag reads as a foreign baseline — the provider drops out of the updates as
// "already released", and a shared versioning group reports a floor one step
// past where the run put it. The wiring masks the run's tags and aligns the
// updates listing, so the record names the movement the run actually made.
func TestStepsWiredRecordTheRunsDependencies(t *testing.T) {
	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	cfg.VersionGroups = map[string]models.VersionGroupConfig{"lib": {Versioning: "fixed"}}
	cfg.Scripts = map[string]models.Script{
		"build":  {echoBuild},
		"record": {bin + " changelog", bin + " commit --tag --push"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"core": {VersionGroup: "lib"},
		"app":  {VersionGroup: "lib"},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): both move")

	res := r.ReleaseOK()

	// The group holds one version, so the provider's tag existing mid-run is
	// exactly the floor the consumer's replan must not read.
	assert.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.HasTag("app@0.1.0"), "tags: %v", r.TagList())
	changelog, err := os.ReadFile(r.Path("packages/app/CHANGELOG.md"))
	require.NoError(t, err)
	assert.Contains(t, string(changelog), "- core: 0.0.0 -> 0.1.0",
		"the consumer's entry names the provider's movement:\n%s", changelog)
	assert.NotContains(t, string(changelog), "0.1.1",
		"no version the run never released:\n%s", changelog)
	assert.False(t, harness.HasCode(res.Events, "W228"),
		"the masked replan reproduces the run, no drift to correct")
	assert.False(t, harness.HasCode(res.Events, "E219"))
}

// TestStepsGithubBeforeCommitWarns: a github step ordered before the commit
// step asks for a release of a tag nobody created yet — GitHub would invent
// the tag at the default branch head — so the misordering is W229, said
// before anything is created. The correctly ordered github step in the same
// flow fires no such warning and finds its tag in place.
func TestStepsGithubBeforeCommitWarns(t *testing.T) {
	var creates []string
	released := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/releases/tags/") {
			tag := req.URL.Path[strings.LastIndex(req.URL.Path, "/tags/")+len("/tags/"):]
			if released[tag] {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id": 1, "assets": []}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/releases") {
			var body struct {
				TagName string `json:"tag_name"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			creates = append(creates, body.TagName)
			released[body.TagName] = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	cfg.Scripts = map[string]models.Script{
		"build": {echoBuild},
		// Deliberately misordered: github before changelog and commit.
		"record": {bin + " github", bin + " changelog", bin + " commit --tag --push", bin + " github"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): begin")

	res := r.ReleaseOK()

	// The steps' own events arrive nested inside the leg's log lines, so the
	// codes are matched in the raw stream rather than as top-level events.
	assert.Contains(t, res.Stdout, "W229",
		"the github step before the commit step is a named smell")
	assert.Contains(t, res.Stdout, "W224",
		"the correctly placed second github step finds the release created and skips")
	assert.Equal(t, []string{"core@0.1.0"}, creates, "one release created, at the run's tag")
	assert.True(t, r.HasTag("core@0.1.0"))
}

// TestStepsCatchUpEntrySpansTheProvidersMovement: the record steps write a
// catch-up entry whose dependencies line spans from the consumer's last
// release, not the provider's collapsed before-and-after. The step commands
// recompute the plan (stepPlan) rather than inherit the executor's, so the
// span must survive that second computation — the docs leg of the 1.3.0
// release wrote "1.3.0 -> 1.3.0" through exactly this path.
func TestStepsCatchUpEntrySpansTheProvidersMovement(t *testing.T) {
	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	cfg.Scripts = map[string]models.Script{
		"build":  {echoBuild},
		"record": {bin + " changelog", bin + " commit --tag --push"},
		// The span reaches scripts through DISPAT_UPDATED_* too; the log
		// proves what a consumer's build actually reads in a catch-up.
		"span-log": {`echo "core: $DISPAT_UPDATED_CORE_OLD_VERSION -> $DISPAT_UPDATED_CORE_NEW_VERSION" >> ../../span.log`, echoBuild},
	}
	// Both spaces record through the wired steps, so the consumer's catch-up
	// entry is the step command's own writing.
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, IsBuildWaitingPublish: models.Bool(true),
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
		"svc": {Path: models.PathList{"services"},
			Flow: &models.SpaceFlowConfig{Build: []string{"span-log"}, Publish: []string{"record"}}},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "web")
	r.Commit("feat(core)^: both release once")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@0.0.1"), "tags: %v", r.TagList())

	r.CommitEmpty("fix(core)^: published alone; web catches up next run")
	res := r.Command("release", "-p", "core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	require.True(t, r.HasTag("core@0.1.1"), "tags: %v", r.TagList())
	require.Zero(t, r.TagCount("web@0.0.2"), "tags: %v", r.TagList())

	r.ReleaseOK()
	require.True(t, r.HasTag("web@0.0.2"), "the catch-up; tags: %v", r.TagList())
	entry := entryOf(t, spacedChangelog(t, r, "services", "web"), "web@0.0.2")
	assert.Contains(t, entry, "- core: 0.1.0 -> 0.1.1",
		"the step-written entry spans from web's last release")
	assert.NotContains(t, entry, "0.1.1 -> 0.1.1",
		"a movement line with no movement is the bug this spans away")

	spanLog, err := os.ReadFile(r.Path("span.log"))
	require.NoError(t, err)
	assert.Contains(t, string(spanLog), "core: 0.1.0 -> 0.1.1",
		"the script environment reads the same span the records carry")
}

// TestStepsAlignedRecordsKeepTheirDependencyLinks: an aligned record links its
// dependency lines to the providers' releases, exactly as the record the run
// would have written itself.
//
// A dependency line's "auto" link is built from the provider's release tag,
// which only the provider's own tagFormat can spell. The run knows it, so it
// states it in DISPAT_UPDATED_<KEY>_TAG beside the movement, and a step whose
// replan drifted picks the whole listing up from there. Without the tag the
// aligned record renders plain lines where an ordinary record links them, and
// a published record is permanent.
//
// The drift is forced the way a real one arrives: a tag the run did not create
// appears mid-run — a concurrent job publishing the provider is the plausible
// source — and the consumer's replan reads the provider as already released
// and drops it from its updates. That is the "provider dropped as already
// released" shape the alignment exists for.
func TestStepsAlignedRecordsKeepTheirDependencyLinks(t *testing.T) {
	r := harness.New(t)
	bin, _ := harness.Build(t)
	r.AddBareRemote()

	cfg := harness.BaseFile(1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
		Name: "steps-test", Email: "steps@example.com"}
	// The coordinates alone, with the recorder off: the changelog borrows them
	// to derive the "auto" link, and the assertion stays on the file.
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(false), Owner: "acme", Repo: "mono"}
	cfg.Changelog = &models.ChangelogConfig{
		EntryFormatConfig: models.EntryFormatConfig{DependencyLink: "auto"},
	}
	cfg.Scripts = map[string]models.Script{
		"build":  {echoBuild},
		"record": {bin + " changelog", bin + " commit --tag --push"},
		// The interloper, in the consumer's own publish stage so that it lands
		// after the provider's leg is done recording: a core tag the run never
		// planned, which the wiring has no reason to mask.
		"stray": {"git tag core@0.2.0"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, IsBuildWaitingPublish: models.Bool(true),
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"record"}}},
		"svc": {Path: models.PathList{"services"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"stray", "record"}}},
	}
	cfg.Dependencies = models.Dependencies{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("services", "web")

	// The consumer moves for its own reasons, so it releases whatever the
	// interloping tag does to the provider's replanned state.
	r.Commit("feat(core): the provider moves")
	r.WriteFile("services/web/w.txt", "w")
	r.Commit("feat(web): the consumer moves too")

	res := r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.HasTag("web@0.1.0"), "tags: %v", r.TagList())

	// The drift happened and was corrected: without this the scenario would
	// prove nothing, because a replan agreeing with the run keeps its own
	// updates and their locally rendered tags.
	assert.Contains(t, res.Stdout, "W228",
		"the interloping tag drifted the replan's updates, stdout:\n%s", res.Stdout)
	assert.NotContains(t, res.Stdout, "E219")

	entry := entryOf(t, spacedChangelog(t, r, "services", "web"), "web@0.1.0")
	assert.Contains(t, entry,
		"- [core](https://github.com/acme/mono/releases/tag/core@0.1.0): 0.0.0 -> 0.1.0",
		"the aligned record links the run's own provider tag:\n%s", entry)
	assert.NotContains(t, entry, "releases/tag/:",
		"and never an empty tag appended to the releases path")
}
