//go:build !windows

package integration

// Goal: what an interrupted run stops doing, and what it leaves behind. The
// interruption scenarios next door assert the release outcome; these assert
// the resources. A cancelled run must stop launching the commands still ahead
// of it, must not leave its temporary files on disk, and must not disturb a
// second run working in the same repository.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// tempResidue lists the working files dispat creates under a temporary
// directory. They are all named for the tool, so anything matching that is
// left over from a run that did not clean up after itself.
func tempResidue(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "dispat-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestCancelStopsTheRemainingCommandsOfAWarnOnlySequence: the announce frame
// runs after the release is out, so none of its commands can fail the
// package — which is exactly why an interrupted run has to stop launching
// them itself. Nothing downstream would report that every remaining command
// died at once, so the run would spend the interruption starting processes
// and warning about them.
func TestCancelStopsTheRemainingCommandsOfAWarnOnlySequence(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	flow := buildPublish()
	// Three announce commands: the first dwells long enough to be interrupted
	// inside, the two behind it only leave a mark.
	flow.Announce = []string{"announceFirst", "announceSecond", "announceThird"}
	marks := harness.ShQuote(r.Path("announce.tsmark"))
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: flow,
		Scripts: map[string]models.Script{
			"announceFirst":  {r.TsmarkScript("announce.tsmark", "first", 2500*time.Millisecond)},
			"announceSecond": {"echo second >> " + marks},
			"announceThird":  {"echo third >> " + marks},
		}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.Commit("feat(a): bootstrap the package")

	proc := r.StartRelease()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("announce.tsmark"))
		return err == nil && strings.Contains(string(data), "first start")
	}, 20*time.Second, 20*time.Millisecond, "the announce stage never started")
	proc.Signal(os.Interrupt)
	res := proc.Wait()

	data, err := os.ReadFile(r.Path("announce.tsmark"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "second",
		"the announce command behind the interrupted one still executed")
	assert.NotContains(t, string(data), "third")
	// A command launched into a cancelled context dies at once, so the marks
	// alone cannot tell "never started" from "started and killed". The run's
	// own report can: one warning for the command the interruption killed,
	// and none for the two that were never attempted.
	failures := strings.Count(res.Stdout, "announce script failed (not fatal)")
	assert.LessOrEqual(t, failures, 1,
		"the commands behind the interrupted one were launched and reported as failures")
	// The package published before the announce frame began, and nothing in
	// that frame can take a published release back.
	assert.Equal(t, 1, r.TagCount("a@"), "the release was out before the interruption")
	assert.NotEqual(t, 0, res.Code, "an interrupted run does not exit 0")
}

// TestCancelledRunsLeaveNoTemporaryFiles: each hook and stage sequence stages
// its script outputs through a temporary file. Repeated interruptions are the
// ordinary shape of a busy CI agent, so none of those files may survive the
// run that made them.
func TestCancelledRunsLeaveNoTemporaryFiles(t *testing.T) {
	r := harness.New(t)
	temp := filepath.Join(r.Root, "..", "tmp-residue")
	require.NoError(t, os.MkdirAll(temp, 0o755))
	cfg := libsConfig(r.TsmarkScript("build.tsmark", "$DISPAT_PACKAGE", 1500*time.Millisecond), 1)
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.Commit("feat(a): bootstrap the package")

	for attempt := range 3 {
		proc := r.StartReleaseEnv([]string{"TMPDIR=" + temp})
		require.Eventually(t, func() bool {
			data, err := os.ReadFile(r.Path("build.tsmark"))
			return err == nil && strings.Count(string(data), "a start") > attempt
		}, 20*time.Second, 20*time.Millisecond, "the build never started")
		proc.Signal(os.Interrupt)
		res := proc.Wait()
		require.NotEqual(t, 0, res.Code, "an interrupted run does not exit 0")
	}

	assert.Empty(t, tempResidue(t, temp),
		"interrupted runs left their working files behind")
}

// TestCancelDoesNotDisturbAConcurrentRun: two invocations covering the same
// repository at once, one interrupted. The interruption belongs to its own
// process — it kills that run's script tree and nothing else — so the run
// beside it finishes normally and reports a clean sweep.
func TestCancelDoesNotDisturbAConcurrentRun(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{Path: models.PathList{"packages"}, Flow: buildPublish(),
		Scripts: map[string]models.Script{
			"slow":  {r.TsmarkScript("slow.tsmark", "$DISPAT_PACKAGE", 3000*time.Millisecond)},
			"quick": {r.TsmarkScript("quick.tsmark", "$DISPAT_PACKAGE", 200*time.Millisecond)},
		}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.Commit("feat(a): bootstrap the package")

	interrupted := r.StartCommandEnv(nil, "run", "slow")
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("slow.tsmark"))
		return err == nil && strings.Contains(string(data), "a start")
	}, 20*time.Second, 20*time.Millisecond, "the slow script never started")

	// The second run works the whole time the first one is being interrupted.
	done := make(chan harness.RunResult, 1)
	go func() { done <- r.RunScript("quick") }()
	interrupted.Signal(os.Interrupt)
	first := interrupted.Wait()

	assert.NotEqual(t, 0, first.Code, "the interrupted run does not exit 0")
	select {
	case second := <-done:
		assert.Equal(t, 0, second.Code,
			"the concurrent run was disturbed by its neighbour's interruption: %s", second.Stderr)
	case <-time.After(60 * time.Second):
		t.Fatal("the concurrent run never finished")
	}
	data, err := os.ReadFile(r.Path("quick.tsmark"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "a end", "the concurrent run completed its script")
}
