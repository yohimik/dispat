package integration

// Goal 60: the provider relation, through the binary.
//
// `isBuildWaitingPublish` used to be a boolean because it answered one
// question. It now answers two — what a consumer's version and build stage
// waits for, and whether a provider that failed outranks a consumer reason of
// its own — so the key additionally accepts an object. What is proven here is
// that the two booleans still mean exactly what they meant, that the third
// relation really does let consumers build beside their provider while their
// publications still follow it, that a provider nobody could read is still a
// provider whose failure stops the publications behind it, and that the build
// order is taken over the dependency graph rather than over the plan.
//
// The overlap claims are gated rather than slept: a consumer's build creates a
// file and the provider's build waits for it, so "these two ran at once" is
// something the run either did or deadlocked on, not something a timer
// happened to catch.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// stageRelationGateWait is a shell command that blocks until path exists,
// bounded so that a missing ordering fails the assertions below rather than
// hanging the suite until the whole run times out.
func stageRelationGateWait(path string) string {
	return fmt.Sprintf("i=0; while [ ! -f '%s' ] && [ $i -lt 400 ]; do sleep 0.05; i=$((i+1)); done", path)
}

// stageRelationRepo is the worked example the relation exists for: one
// infrastructure package and two applications deployed onto it. The
// applications' builds read nothing the infrastructure produces, their
// deployments read everything, and the relation under test is the
// infrastructure's, because what a consumer may do with a provider is a
// property of the provider.
//
// Every stage records its window in one timeline under the same label
// whichever infrastructure build ran. infraBuildScript chooses between the
// two: `infra-build` is an ordinary build, and `infra-build-gated` waits for
// the gate an application build opens, which is how the overlap claim becomes
// something the run either did or could not finish rather than something a
// timer happened to catch. The gated one deadlocks under any relation that
// holds the applications back, so only the `none` scenario asks for it.
func stageRelationRepo(t *testing.T, relation *models.StageRelation, infraBuildScript string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	gate := r.Path("app-build.gate")
	mark := func(label string, sleep time.Duration) string {
		return r.TsmarkScript("timeline.log", label, sleep)
	}
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		"infra-build":       {mark("infra-build", 300*time.Millisecond)},
		"infra-build-gated": {stageRelationGateWait(gate), mark("infra-build", 300*time.Millisecond)},
		"infra-publish":     {mark("infra-publish", 250*time.Millisecond)},
		"app-build":         {"touch '" + gate + "'", mark("$DISPAT_PACKAGE-build", 400*time.Millisecond)},
		"app-publish":       {mark("$DISPAT_PACKAGE-publish", 0)},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"infra": {Path: models.PathList{"packages/infra"},
			IsBuildWaitingPublish: relation,
			Flow: &models.SpaceFlowConfig{
				Build: []string{infraBuildScript}, Publish: []string{"infra-publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{
				Build: []string{"app-build"}, Publish: []string{"app-publish"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "backend", Provider: "infra"},
		{Consumer: "frontend", Provider: "infra"},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/infra", "infra")
	r.SeedPackage("packages/apps", "backend")
	r.SeedPackage("packages/apps", "frontend")
	r.Commit("feat(infra)^: the applications follow the infrastructure")
	return r
}

// TestStageRelationBuildsOverlapWhilePublishesFollowTheProvider is the claim
// the third relation was added for: under `none` the three builds run at once,
// and each application still deploys only after the infrastructure applied.
func TestStageRelationBuildsOverlapWhilePublishesFollowTheProvider(t *testing.T) {
	r := stageRelationRepo(t, &models.StageRelation{Build: models.StageWaitNone}, "infra-build-gated")
	r.ReleaseOK()

	tl := r.Timeline("timeline.log")
	infraBuild := harness.Find(t, tl, "infra-build")
	infraPublish := harness.Find(t, tl, "infra-publish")

	// The gate is the evidence: the infrastructure build did not begin
	// recording until the frontend build had started, so the two were in
	// flight together and the frontend plainly did not wait.
	harness.AssertOverlaps(t, infraBuild, harness.Find(t, tl, "frontend-build"))
	harness.AssertOverlaps(t, infraBuild, harness.Find(t, tl, "backend-build"))

	for _, app := range []string{"backend", "frontend"} {
		harness.AssertSequential(t, infraPublish, harness.Find(t, tl, app+"-publish"))
	}
	for _, name := range []string{"infra", "backend", "frontend"} {
		assert.Equal(t, 1, r.TagCount(name+"@"), "every package released; tags: %v", r.TagList())
	}
}

// TestStageRelationBooleansKeepTheirMeaning: the key's two original values
// still order a run exactly as they did, whether they are written as booleans
// or as the objects they are shorthand for. `false` holds a consumer's build
// behind the provider's build and lets it overlap the provider's publish;
// `true` holds it behind the publish.
func TestStageRelationBooleansKeepTheirMeaning(t *testing.T) {
	for name, relation := range map[string]*models.StageRelation{
		"false":                    models.StageRelationOf(false),
		"the object it stands for": {Build: models.StageWaitBuild},
	} {
		t.Run("a build relation written as "+name, func(t *testing.T) {
			r := stageRelationRepo(t, relation, "infra-build")
			r.ReleaseOK()

			tl := r.Timeline("timeline.log")
			infraBuild := harness.Find(t, tl, "infra-build")
			infraPublish := harness.Find(t, tl, "infra-publish")
			for _, app := range []string{"backend", "frontend"} {
				appBuild := harness.Find(t, tl, app+"-build")
				harness.AssertSequential(t, infraBuild, appBuild)
				assert.Truef(t, appBuild.Start.Before(infraPublish.End),
					"%s built at %s, after the infrastructure publish ended at %s: a build relation does not wait for a publish",
					app, appBuild.Start, infraPublish.End)
				harness.AssertSequential(t, infraPublish, harness.Find(t, tl, app+"-publish"))
			}
		})
	}

	for name, relation := range map[string]*models.StageRelation{
		"true":                     models.StageRelationOf(true),
		"the object it stands for": {Build: models.StageWaitPublish},
	} {
		t.Run("a publish relation written as "+name, func(t *testing.T) {
			r := stageRelationRepo(t, relation, "infra-build")
			r.ReleaseOK()

			tl := r.Timeline("timeline.log")
			infraPublish := harness.Find(t, tl, "infra-publish")
			for _, app := range []string{"backend", "frontend"} {
				harness.AssertSequential(t, infraPublish, harness.Find(t, tl, app+"-build"))
				harness.AssertSequential(t, infraPublish, harness.Find(t, tl, app+"-publish"))
			}
		})
	}
}

// stageRelationFailingRepo is stageRelationRepo's failure counterpart: the
// infrastructure publish fails, and the frontend carries a bump of its own, so
// the only thing deciding whether it releases is whether the relation blocks.
// Its build has already run by then, which is exactly the state `none` is
// supposed to produce and exactly the state that must still publish nothing.
func stageRelationFailingRepo(t *testing.T, relation *models.StageRelation) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		"build":        {"echo built > built.marker"},
		"publish":      {"echo publishing"},
		"fail-publish": {"exit 1"},
		"on-skip":      {"echo \"$DISPAT_BLOCKED_BY\" > '" + r.Path("skipped.log") + "'"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"infra": {Path: models.PathList{"packages/infra"},
			IsBuildWaitingPublish: relation,
			Flow: &models.SpaceFlowConfig{
				Build: []string{"build"}, Publish: []string{"fail-publish"}}},
		"apps": {Path: models.PathList{"packages/apps"},
			RevertOnFail: models.Bool(true),
			Flow: &models.SpaceFlowConfig{
				Build: []string{"build"}, Publish: []string{"publish"}, OnSkip: []string{"on-skip"}}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "frontend", Provider: "infra"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages/infra", "infra")
	r.SeedPackage("packages/apps", "frontend")
	r.Commit("feat(infra,frontend): both carry work of their own")
	return r
}

// TestStageRelationBlockingSkipsAConsumerWithItsOwnChanges: a relation that
// declares nothing is read during the build still declares that the
// publications follow, so a provider that never published leaves its consumers
// nothing to follow and skips them whatever work they carry. That is the
// default for `none`, and the one thing a configuration may relax.
func TestStageRelationBlockingSkipsAConsumerWithItsOwnChanges(t *testing.T) {
	blocked := stageRelationFailingRepo(t, &models.StageRelation{Build: models.StageWaitNone})
	res := blocked.Release()
	require.Equal(t, 1, res.Code, "the infrastructure publish fails the run\nstdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresentForPackage(res.Events, "W194", "frontend"),
		"the consumer is skipped, its own work notwithstanding: %s", res.Stdout)
	assert.Zero(t, blocked.TagCount("frontend@"),
		"nothing may publish behind a provider that never published; tags: %v", blocked.TagList())
	assert.FileExists(t, blocked.Path("skipped.log"), "the onSkip script ran for the blocked consumer")
	assert.NoFileExists(t, blocked.Path("packages", "apps", "frontend", "built.marker"),
		"revertOnFail rolled the finished build back out of the folder")

	// Relaxed, the consumer releases its own work exactly as it does under the
	// key's `false`: nothing of the provider reached its build, and its own
	// bump is a reason the failure cannot invalidate.
	released := stageRelationFailingRepo(t,
		&models.StageRelation{Build: models.StageWaitNone, IsBlocking: models.Bool(false)})
	res = released.Release()
	require.Equal(t, 1, res.Code, "the infrastructure still fails the run\nstdout:\n%s", res.Stdout)
	assert.False(t, harness.IsCodePresent(res.Events, "W194"),
		"a fresh own bump proceeds under a relation that does not block")
	assert.Equal(t, 1, released.TagCount("frontend@"), "tags: %v", released.TagList())
}

// TestStageRelationOrdersBuildsThroughAPackageThatDoesNotBuild: a consumer
// reads its providers through whatever lies between them, and what lies
// between them need not be releasing. `app` depends on `ui`, `ui` depends on
// `core`, and only `app` and `core` have work of their own, so `ui` is not in
// the plan at all; `core` must still build first, because what `app` reads of
// `ui` may be `core`'s.
func TestStageRelationOrdersBuildsThroughAPackageThatDoesNotBuild(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(3)
	cfg.Scripts = map[string]models.Script{
		"build":   {r.TsmarkScript("build.log", "$DISPAT_PACKAGE", 400*time.Millisecond)},
		"publish": {"echo publishing"},
	}
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages"}, Flow: buildPublish()},
	}
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "app", Provider: "ui"},
		{Consumer: "ui", Provider: "core"},
	}
	r.WriteConfigModel(cfg)
	for _, name := range []string{"core", "ui", "app"} {
		r.SeedPackage("packages", name)
	}
	// No caret: the bumps reach nobody, so `ui` has nothing to release and the
	// two packages that do have no edge between them inside the plan.
	r.Commit("feat(core,app): each for its own reasons")

	res := r.ReleaseOK()
	require.Zero(t, r.TagCount("ui@"), "the middle package is not in the plan; tags: %v", r.TagList())
	require.Equal(t, 1, r.TagCount("core@"), "tags: %v", r.TagList())
	require.Equal(t, 1, r.TagCount("app@"), "tags: %v\nstdout:\n%s", r.TagList(), res.Stdout)

	build := r.Timeline("build.log")
	harness.AssertSequential(t, harness.Find(t, build, "core"), harness.Find(t, build, "app"))
}

// TestStageRelationLadder: the key folds through the ordinary ladder and a
// level that states it replaces the whole relation, read out of the resolved
// debug lines of `dispat status` rather than by starting a release.
func TestStageRelationLadder(t *testing.T) {
	r := harness.New(t)
	cfg := harness.BaseFile(1)
	cfg.Scripts = map[string]models.Script{"build": {echoBuild}}
	// The repository default reaches every space that says nothing.
	cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages/libs"},
			IsBuildWaitingPublish: &models.StageRelation{Build: models.StageWaitNone},
			Flow:                  &models.SpaceFlowConfig{Build: []string{"build"}},
			Packages: map[string]models.PackageConfig{
				"utils": {IsBuildWaitingPublish: models.StageRelationOf(false)},
			}},
		"apps": {Path: models.PathList{"packages/apps"},
			Flow: &models.SpaceFlowConfig{Build: []string{"build"}}},
	}
	r.WriteConfigModel(cfg)
	for _, dir := range []string{"core", "utils", "tool"} {
		r.SeedPackage("packages/libs", dir)
	}
	r.SeedPackage("packages/apps", "app")
	// The package's own folder file is nearer than the space that holds it.
	r.WriteFile("packages/libs/tool/dispat.json",
		`{"isBuildWaitingPublish": {"build": "build", "isBlocking": true}}`)
	r.Commit("feat(core,utils,tool,app): bootstrap the ladder")

	res := r.StatusOK("--log-level", "debug")
	for name, want := range map[string]struct {
		build      string
		isBlocking bool
	}{
		"app":   {"publish", true},
		"core":  {"none", true},
		"utils": {"", false},
		"tool":  {"build", true},
	} {
		resolved := executionResolvedPackage(t, res, name)
		assert.Equal(t, want.build, resolved.Str("providerRelation"), name)
		if want.build == "" {
			continue
		}
		assert.Equal(t, want.isBlocking, resolved["providerBlocking"], name)
	}
	assert.Empty(t, r.TagList(), "status releases nothing")
}

// TestStageRelationConfigRefusals: the object's rules are load-time rules, on
// every level of the ladder that carries the key, and each refusal names the
// key so a reader can find the line that is wrong.
func TestStageRelationConfigRefusals(t *testing.T) {
	for name, written := range map[string]any{
		"an object with no build":          map[string]any{"isBlocking": true},
		"an unknown wait":                  map[string]any{"build": "published"},
		"a wait spelled in the wrong case": map[string]any{"build": "Publish"},
		"an unknown key":                   map[string]any{"build": "none", "blocking": true},
		"a publish that cannot block":      map[string]any{"build": "publish", "isBlocking": false},
		"a blocking rule that is not one":  map[string]any{"build": "none", "isBlocking": "yes"},
	} {
		for level, write := range stageRelationLevels() {
			t.Run(name+" at "+level, func(t *testing.T) {
				r := harness.New(t)
				r.SeedPackage("packages/libs", "core")
				write(r, written)
				r.Commit("feat(core): bootstrap")
				res := r.Status("--log-format", "json")
				require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Contains(t, diagnosticText(res), "isBuildWaitingPublish",
					"the refusal names the key")
				assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
			})
		}
	}
}

// stageRelationLevels writes one repository per level of the ladder that
// carries the key, each with the same relation written at that level alone.
func stageRelationLevels() map[string]func(*harness.Repo, any) {
	base := func(relation any) map[string]any {
		return map[string]any{
			"logFormat": "json",
			"scripts":   map[string]any{"build": echoBuild},
			"spaces": map[string]any{
				"libs": map[string]any{"path": "packages/libs", "flow": map[string]any{"build": "build"}},
			},
			"isBuildWaitingPublish": relation,
		}
	}
	return map[string]func(*harness.Repo, any){
		"the root file": func(r *harness.Repo, relation any) {
			r.WriteConfigRaw(base(relation))
		},
		"a space entry": func(r *harness.Repo, relation any) {
			cfg := base(nil)
			delete(cfg, "isBuildWaitingPublish")
			cfg["spaces"].(map[string]any)["libs"].(map[string]any)["isBuildWaitingPublish"] = relation
			r.WriteConfigRaw(cfg)
		},
		"a package entry": func(r *harness.Repo, relation any) {
			cfg := base(nil)
			delete(cfg, "isBuildWaitingPublish")
			cfg["packages"] = map[string]any{"core": map[string]any{"isBuildWaitingPublish": relation}}
			r.WriteConfigRaw(cfg)
		},
		"a space folder's own file": func(r *harness.Repo, relation any) {
			cfg := base(nil)
			delete(cfg, "isBuildWaitingPublish")
			r.WriteConfigRaw(cfg)
			stageRelationWriteJSON(r, "packages/libs/dispat.json", relation)
		},
		"a package folder's own file": func(r *harness.Repo, relation any) {
			cfg := base(nil)
			delete(cfg, "isBuildWaitingPublish")
			r.WriteConfigRaw(cfg)
			stageRelationWriteJSON(r, "packages/libs/core/dispat.json", relation)
		},
	}
}

// stageRelationWriteJSON writes an in-folder configuration file stating the
// key alone. The model cannot express the shapes this test is about, so the
// value is a raw one, still written through the JSON marshaller rather than as
// a hand-formatted string.
func stageRelationWriteJSON(r *harness.Repo, relPath string, relation any) {
	r.T.Helper()
	data, err := json.Marshal(map[string]any{"isBuildWaitingPublish": relation})
	require.NoError(r.T, err)
	r.WriteFile(relPath, string(data))
}
