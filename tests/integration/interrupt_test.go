//go:build !windows

package integration

// Goal: interruption is an outcome, not an accident. A SIGINT mid-run shuts
// the run down gracefully — the in-flight script is killed, remaining
// packages are cancelled rather than failed, nothing is tagged for work that
// did not finish — and the next run picks the cancelled packages up at the
// exact release they were owed. One flowing scenario, per the conventions.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// summaryStatuses maps package -> status from the run's summary events.
func summaryStatuses(events []harness.Event) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.Str("message") == "summary" {
			out[e.Package()] = e.Str("status")
		}
	}
	return out
}

func TestInterruptGracefulShutdown(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig("", 1)
	// The build marks its package into the tsmark log and dwells long enough
	// for the test to interrupt it mid-flight; b waits behind a.
	cfg.Scripts["build"] = models.Script{r.TsmarkScript("build.tsmark", "$DISPAT_PACKAGE", 1500*time.Millisecond)}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "b", Provider: "a"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a,b): bootstrap both packages")

	proc := r.StartRelease()
	// Interrupt once a's build has demonstrably started.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("build.tsmark"))
		return err == nil && strings.Contains(string(data), "a start")
	}, 15*time.Second, 20*time.Millisecond, "a's build never started")
	proc.Signal(os.Interrupt)
	res := proc.Wait()

	// The run reports the interruption: non-zero exit, both packages
	// cancelled (a was killed mid-build, b never launched), nothing tagged
	// and no completed-release records for either.
	assert.NotEqual(t, 0, res.Code, "an interrupted run does not exit 0")
	statuses := summaryStatuses(res.Events)
	assert.Equal(t, "cancelled", statuses["a"], "the killed build is an interruption, not a failure")
	assert.Equal(t, "cancelled", statuses["b"], "never-launched work is cancelled, not skipped")
	assert.Equal(t, 0, r.TagCount("a@"), "no tag for a publish that never happened")
	assert.Equal(t, 0, r.TagCount("b@"))

	// Recovery is just re-running: the next run owes both packages the same
	// release and completes it.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("a@"))
	assert.Equal(t, 1, r.TagCount("b@"))
	// Raw log, not ParseTimeline: the interrupted run left a's start with no
	// end, which the timeline parser rightly refuses.
	data, err := os.ReadFile(r.Path("build.tsmark"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "b end", "the catch-up run built b to completion")
}

// TestInterruptStopsARunCommand: `dispat run` is interruptible on the same
// terms as a release. It executes the same scheduler, so a SIGINT mid-script
// stops the in-flight package, never launches the ones behind it, and makes
// the command exit non-zero rather than report a clean sweep. Nothing is
// released either way, so the only thing to get wrong here is the exit code.
func TestInterruptStopsARunCommand(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(),
		Scripts: map[string]models.Script{"mark": {r.TsmarkScript("run.tsmark", "$DISPAT_PACKAGE", 1500*time.Millisecond)}}}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "b", Provider: "a"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a,b): bootstrap both packages")

	proc := r.StartRelease("run", "mark") // raw args: `dispat run mark`
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("run.tsmark"))
		return err == nil && strings.Contains(string(data), "a start")
	}, 15*time.Second, 20*time.Millisecond, "a's script never started")
	proc.Signal(os.Interrupt)
	res := proc.Wait()

	assert.NotEqual(t, 0, res.Code, "an interrupted run does not exit 0")
	data, err := os.ReadFile(r.Path("run.tsmark"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "b start", "the package behind the interrupted one never launches")
	assert.Empty(t, r.TagList(), "dispat run releases nothing, interrupted or not")
}
