package integration

// Goal 37: step commands wired into a running release. A library space whose
// publish stage is the record itself — changelog, commit, tag, push — through
// the step commands, with a consumer gated on the provider's publish, so the
// provider's tag is on the remote before the consumer's build asks for it.
// The wiring (W228/E219, the environment scoping and the leg's own tag masked
// from the step's replan) is what makes the composition safe; this file pins
// it through the compiled binary.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

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
