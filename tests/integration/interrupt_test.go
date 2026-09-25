//go:build !windows

package integration

// Goal: interruption is an outcome, not an accident. A SIGINT mid-run shuts
// the run down gracefully — the in-flight script is killed, remaining
// packages are cancelled rather than failed, nothing is tagged for work that
// did not finish — and the next run picks the cancelled packages up at the
// exact release they were owed. One flowing scenario, per the conventions.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

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

// TestInterruptTerminatesAPublishCommand: a SIGTERM, what a CI runner sends
// when a job is cancelled, that arrives while a publish command runs stops
// that command and nothing is recorded for it. A publication that never
// reported success is not a completed publish, so the package is cancelled
// rather than published or failed, its consumer never publishes, nothing is
// tagged, and the lock the run held is given back rather than stranded. The
// next run owes both packages the same release and completes it.
func TestInterruptTerminatesAPublishCommand(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["publish"] = models.Script{r.TsmarkScript("publish.tsmark", "$DISPAT_PACKAGE", 30*time.Second)}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.Commit("feat(core,app): bootstrap both packages")
	bare := r.AddBareRemote()
	r.Git("push", "-q", "origin", "HEAD")

	proc := r.StartReleaseEnv(harness.LockEnabled)
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("publish.tsmark"))
		return err == nil && strings.Contains(string(data), "core start")
	}, 30*time.Second, 20*time.Millisecond, "core's publish never started")
	proc.Signal(syscall.SIGTERM)
	res := proc.Wait()

	assert.NotEqual(t, 0, res.Code, "a terminated run does not exit 0\nstdout:\n%s", res.Stdout)
	statuses := summaryStatuses(res.Events)
	assert.Equal(t, "cancelled", statuses["core"], "a publish that never reported success is not a publication")
	assert.Equal(t, "cancelled", statuses["app"], "and its consumer never publishes")
	data, err := os.ReadFile(r.Path("publish.tsmark"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "core end", "the publish command was stopped")
	assert.NotContains(t, string(data), "app start")
	assert.Empty(t, r.TagList(), "nothing is recorded for work that did not finish")
	assert.False(t, remoteHoldsLock(t, bare), "the lock is given back, not stranded")

	cfg.Scripts["publish"] = models.Script{r.TsmarkScript("publish.tsmark", "$DISPAT_PACKAGE", 0)}
	r.WriteConfigModel(cfg)
	r.Commit("chore: publish without the dwell")
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.True(t, r.IsTagged("app@0.1.0"), "tags: %v", r.TagList())
}

// TestInterruptKilledAfterAPublishStrandsTheLock: a run killed outright
// between a publish command's success and the tag that records it is the one
// interval a tag cannot describe. Its publish script reports success and then
// kills the dispat process that started it, so nothing of the run survives to
// clean up: the process dies of the signal, the package is uploaded and not
// tagged, and the lock stays on the remote naming its holder. The next run is
// refused by that stranded lock with E336 naming the host and process that
// took it, before anything is planned. Once the operator clears the lock, as
// the lock page's remedy says, the retry uploads again unless the publish
// script verifies what the destination already holds, and records the tag
// either way.
func TestInterruptKilledAfterAPublishStrandsTheLock(t *testing.T) {
	for name, isVerified := range map[string]bool{"a plain upload": false, "an upload that verifies first": true} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			registryPath := filepath.Join(t.TempDir(), "registry.log")
			registry := harness.ShQuote(registryPath)
			killed := harness.ShQuote(filepath.Join(t.TempDir(), "killed"))
			upload := `echo "$DISPAT_PACKAGE@$DISPAT_NEW_VERSION" >> ` + registry
			if isVerified {
				upload = `grep -qx "$DISPAT_PACKAGE@$DISPAT_NEW_VERSION" ` + registry + " 2>/dev/null || " + upload
			}
			cfg := libsConfig(markerBuild, 1)
			cfg.Scripts["publish"] = models.Script{
				upload,
				// Once, and only after the upload succeeded: the parent of the
				// shell running this line is the dispat process itself.
				"if [ ! -e " + killed + " ]; then : > " + killed + "; kill -9 $PPID; fi",
			}
			r.WriteConfigModel(cfg)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first")
			bare := r.AddBareRemote()
			r.Git("push", "-q", "origin", "HEAD")

			res := r.CommandEnv(harness.LockEnabled)
			assert.Equal(t, -1, res.Code, "the process died of the signal\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Equal(t, []string{"core@0.1.0"}, readLines(t, registryPath), "the upload happened")
			assert.Zero(t, r.TagCount("core@"), "and was never recorded")
			require.True(t, remoteHoldsLock(t, bare), "the killed run's lock is stranded on the remote")
			holder := lockHolderPID(t, bare)

			refused := r.CommandEnv(harness.LockEnabled)
			assert.Equal(t, 1, refused.Code, "stdout:\n%s\nstderr:\n%s", refused.Stdout, refused.Stderr)
			assert.True(t, harness.IsCodePresent(refused.Events, "E336"), "stdout:\n%s", refused.Stdout)
			assert.Contains(t, refused.Stdout, "pid "+holder, "the refusal names the process holding the lock")
			assert.Contains(t, refused.Stdout, "delete the tag on the remote", "and the remedy for a stranded lock")
			assert.Equal(t, 1, buildRuns(r), "the refused run built nothing")

			// The operator's act, after confirming nothing is releasing.
			r.Git("push", "-q", "origin", "--delete", lockTag)
			retry := r.CommandEnv(harness.LockEnabled)
			require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
			uploads := []string{"core@0.1.0", "core@0.1.0"}
			if isVerified {
				uploads = uploads[:1]
			}
			assert.Equal(t, uploads, readLines(t, registryPath),
				"the retry uploads again unless the publish script verifies")
			assert.True(t, r.IsTagged("core@0.1.0"), "and records the release: %v", r.TagList())
			assert.False(t, remoteHoldsLock(t, bare))
		})
	}
}

// lockHolderPID is the process id the remote's lock tag says took it.
func lockHolderPID(t *testing.T, bare string) string {
	t.Helper()
	for _, line := range strings.Split(bareGit(t, bare, "cat-file", "tag", lockTag), "\n") {
		if pid, ok := strings.CutPrefix(line, "pid "); ok {
			return pid
		}
	}
	t.Fatalf("the lock tag names no process")
	return ""
}

// readLines is a file's lines, none for a file that does not exist.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Fields(string(data))
}

// closingPhaseHooks are the run hooks of the closing phase in the order a
// release commit fires them: postAll, then the finalize brackets.
var closingPhaseHooks = []string{"postAll", "beforeCommit", "afterCommit", "postCommit", "beforePush", "afterPush"}

// closingPhaseRepo is a commit-mode release of one package that pushes to a
// bare remote and reports its outcome to a webhook. Every closing-phase hook
// leaves a marker named after itself, except the one the scenario interrupts,
// which records its start into closing.tsmark and dwells long enough for the
// signal to land inside it.
func closingPhaseRepo(t *testing.T, sink *webhookSink, interrupted string) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	cfg := webhooksConfig(echoBuild, models.WebhookConfig{URL: sink.srv.URL, Events: []string{"release.finished"}})
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	hookRefs := map[string][]string{}
	for _, hook := range closingPhaseHooks {
		cfg.Scripts[hook] = models.Script{"echo ran > " + hook + ".marker"}
		if hook == interrupted {
			cfg.Scripts[hook] = models.Script{r.TsmarkScript("closing.tsmark", hook, 30*time.Second)}
		}
		hookRefs[hook] = []string{hook}
	}
	cfg.Run = &models.RunConfig{PostAll: hookRefs["postAll"], BeforeCommit: hookRefs["beforeCommit"],
		AfterCommit: hookRefs["afterCommit"], PostCommit: hookRefs["postCommit"],
		BeforePush: hookRefs["beforePush"], AfterPush: hookRefs["afterPush"]}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	bare := r.AddBareRemote()
	r.Commit("feat(core): first release")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	return r, bare
}

// TestInterruptInTheClosingPhaseStillRecordsWhatPublished: an interrupt that
// arrives after the packages published, during postAll, during a finalize
// bracket hook or while the release push itself is on the wire, stops the
// operator's scripts and nothing else. The release commit and the tag reach
// the remote, the run exits non-zero, the closing webhook says `interrupted`,
// and no bracket hook after the interrupt starts. The held push is the proof
// that no cancelled context reaches Git: a push the interrupt killed would
// leave the remote without the release.
func TestInterruptInTheClosingPhaseStillRecordsWhatPublished(t *testing.T) {
	for name, tc := range map[string]struct {
		signal os.Signal
		// interrupted is the hook the signal lands in, or "push" for the
		// release push held by a stand-in git.
		interrupted string
		// ran are the hooks that finish before the signal; every other hook
		// but the interrupted one comes after it.
		ran []string
	}{
		"SIGINT during postAll": {signal: os.Interrupt, interrupted: "postAll"},
		"SIGINT during beforeCommit": {signal: os.Interrupt, interrupted: "beforeCommit",
			ran: []string{"postAll"}},
		"SIGTERM during beforeCommit": {signal: syscall.SIGTERM, interrupted: "beforeCommit",
			ran: []string{"postAll"}},
		"SIGINT during the release push": {signal: os.Interrupt, interrupted: "push",
			ran: []string{"postAll", "beforeCommit", "afterCommit", "postCommit", "beforePush"}},
	} {
		t.Run(name, func(t *testing.T) {
			sink := newWebhookSink(t)
			r, bare := closingPhaseRepo(t, sink, tc.interrupted)
			interrupt := func() harness.RunResult {
				if tc.interrupted == "push" {
					hold := harness.NewGitFault(t, harness.GitFault{Pattern: "*push origin HEAD", Hold: true})
					proc := r.StartReleaseEnv(hold.Env())
					require.Eventually(t, hold.IsHeld, 60*time.Second, 20*time.Millisecond, "the release push never started")
					proc.Signal(tc.signal)
					// A push the interrupt reached would be killed at once; the
					// pause gives it every chance to be before the push goes on.
					time.Sleep(time.Second)
					hold.Resume()
					return proc.Wait()
				}
				proc := r.StartRelease()
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(r.Path("closing.tsmark"))
					return err == nil && strings.Contains(string(data), tc.interrupted+" start")
				}, 60*time.Second, 20*time.Millisecond, "%s never started", tc.interrupted)
				proc.Signal(tc.signal)
				return proc.Wait()
			}
			res := interrupt()

			assert.NotEqual(t, 0, res.Code, "an interrupted run does not exit 0\nstdout:\n%s", res.Stdout)
			assert.Contains(t, bareGit(t, bare, "tag", "--list"), "core@0.1.0", "the tag reached the remote")
			assert.Equal(t, "chore(release): core@0.1.0",
				strings.TrimSpace(bareGit(t, bare, "log", "-1", "--format=%s", harness.DefaultBranch)),
				"the release commit reached the remote")
			assert.Equal(t, "interrupted", sink.find(t, "release.finished")["status"])
			if data, err := os.ReadFile(r.Path("closing.tsmark")); err == nil {
				assert.NotContains(t, string(data), tc.interrupted+" end", "the interrupted hook was stopped")
			}
			for _, hook := range closingPhaseHooks {
				marker := r.Path(hook + ".marker")
				switch {
				case hook == tc.interrupted:
				case slices.Contains(tc.ran, hook):
					assert.FileExists(t, marker, "%s ran before the interrupt", hook)
				default:
					assert.NoFileExists(t, marker, "%s would start after the interrupt", hook)
				}
			}
		})
	}
}
