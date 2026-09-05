package integration

// Area 9: repository-scoped errors (the §16 fatal bucket). These codes mean no
// correct plan exists at all, so they abort the run whatever commitErrors
// says — the cases where a partial release would be worst. Each scenario
// constructs the broken repository for real and asserts three things: the
// exit code is non-zero, the specific code reaches the JSON events, and
// nothing was released.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFatalDependencyCycle: a cyclic dependency graph has no publish order.
// The config loads (the cycle spans two edges, so no single entry is wrong);
// planning must refuse with E200 and release nothing.
func TestFatalDependencyCycle(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(markerBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "app", Provider: "core"},
		{Consumer: "core", Provider: "app"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): both exist")

	res := r.Release()
	assert.NotZero(t, res.Code, "a cyclic graph must refuse to release")
	assert.True(t, harness.HasCode(res.Events, "E200"),
		"the cycle must surface as E200, not a bare failure: %s", res.Stdout)
	assert.Empty(t, r.TagList(), "nothing may release from an unplannable repository")
	assert.Zero(t, buildRuns(r), "and no script may run")

	// status refuses too: there is no plan to show.
	assert.NotZero(t, r.Status().Code)

	// So does `dispat run`, even though it releases nothing: it still needs to
	// know which packages changed, and an unplannable repository cannot say.
	// The script name is a real one, so this is the plan refusing and not the
	// typo guard.
	run := r.RunScript("build")
	assert.NotZero(t, run.Code, "a run over an unplannable repository must refuse")
	assert.Zero(t, buildRuns(r), "and still run nothing")
}

// TestFatalDuplicateVersionTags: two reachable tags parsing to the same
// version of one package on different commits (build metadata carries no
// precedence, so "core@0.1.0" and "core@0.1.0+dup" collide) make the baseline
// ambiguous. E191, regardless of commitErrors.
func TestFatalDuplicateVersionTags(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): first release")
	r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"))

	r.CommitEmpty("fix(core): pending work")
	r.Git("tag", "-a", "core@0.1.0+dup", "-m", "duplicate version on a different commit")

	res := r.Release()
	assert.NotZero(t, res.Code)
	assert.True(t, harness.HasCode(res.Events, "E191"),
		"duplicate version tags must surface as E191: %s", res.Stdout)
	assert.Equal(t, 1, buildRuns(r), "the pending fix must not have been released")
}

// TestFatalShallowRepository: a shallow clone hides commits and tags, so
// every window computed over it is silently wrong. dispat must refuse with
// E196 instead of quietly planning from the truncated history.
func TestFatalShallowRepository(t *testing.T) {
	r := singlePackageRepo(t, markerBuild)
	r.Commit("feat(core): released work")
	r.ReleaseOK()
	r.CommitEmpty("fix(core): pending work")

	// A file:// clone honours --depth; a plain local-path clone would not.
	r.Git("clone", "-q", "--depth", "1", "file://"+r.Root, r.Path("shallow-clone"))
	res := r.CommandAt("shallow-clone", "release")
	assert.NotZero(t, res.Code, "a shallow clone must refuse to release")
	assert.True(t, harness.HasCode(res.Events, "E196"),
		"shallowness must surface as E196: %s", res.Stdout)
}
