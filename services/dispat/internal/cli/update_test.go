package cli

// The update notice, from the controller's side: when the check is started at
// all, what stops it, and the one rule that matters — that no command ever
// waits for it.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// TestMain keeps the whole package off the network. dispat's own releases are
// what the check asks about, so a unit test running a version-stamped binary
// would otherwise call api.github.com; every test that wants the check on
// turns it back on for itself with t.Setenv.
func TestMain(m *testing.M) {
	os.Setenv(updateCheckEnv, "0")
	os.Exit(m.Run())
}

// newFlagSet is the whole flag surface, parsed but unrun, which is what the
// helpers reading flags need to be handed.
func newFlagSet() (*pflag.FlagSet, *options) {
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	return fs, declareFlags(fs)
}

// releasesHandler answers the listing with one release at the given version.
func releasesHandler(t *testing.T, version string, hits *int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			*hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "services/dispat/v" + version, "draft": false, "prerelease": false,
			"assets": []map[string]any{},
		}})
	}
}

// updateRepo writes a config file and a git marker, which is the least a
// command needs before the controller will load a configuration at all.
func updateRepo(t *testing.T, cfg models.File) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), data, 0o644))
	return root
}

// TestNoticePrintsOnlyAnAnswerAlreadyInHand: the read is what enforces "never
// wait" — an answer that is there is printed, and one that is not is dropped
// without a moment's pause. Both are exercised here rather than through a
// command, because through a command which one happens is a race, and a test
// that asserts on a race asserts nothing.
func TestNoticePrintsOnlyAnAnswerAlreadyInHand(t *testing.T) {
	answered := func() notice {
		ch := make(chan selfupdate.Result, 1)
		ch <- selfupdate.Result{
			Current: mustVersion(t, "1.0.0"), Latest: mustVersion(t, "9.9.9"),
			Origin: selfupdate.OriginRelease,
		}
		return notice{ch: ch}
	}

	var out bytes.Buffer
	answered().print(&out)
	assert.Contains(t, out.String(), "a newer stable release is available: 9.9.9")
	assert.Contains(t, out.String(), `run "dispat self-update" to install it`)

	// --version asks the question outright, so it says so either way.
	out.Reset()
	n := answered()
	n.status = true
	n.print(&out)
	assert.Contains(t, out.String(), "9.9.9")

	out.Reset()
	current := make(chan selfupdate.Result, 1)
	current <- selfupdate.Result{Current: mustVersion(t, "9.9.9"), Latest: mustVersion(t, "9.9.9")}
	notice{ch: current, status: true}.print(&out)
	assert.Contains(t, out.String(), "this is the latest stable release")

	// Nothing to read: no output, and above all no waiting.
	out.Reset()
	done := make(chan struct{})
	go func() {
		notice{}.print(&out)                                 // never started
		notice{ch: make(chan selfupdate.Result)}.print(&out) // started, still running
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("printing waited for an answer that had not come")
	}
	assert.Empty(t, out.String())
}

func mustVersion(t *testing.T, s string) ccme.Version {
	t.Helper()
	v, err := ccme.ParseVersion(s)
	require.NoError(t, err)
	return v
}

// TestNoCommandWaitsForTheUpdateCheck: the one rule the whole design rests
// on. A server that never answers must cost nothing, on --version and on a
// command that loads a configuration alike.
func TestNoCommandWaitsForTheUpdateCheck(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-block:
		case <-req.Context().Done():
		}
	}))
	defer srv.Close()
	t.Setenv(updateCheckEnv, "1")
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.0.0"

	root := updateRepo(t, models.File{})
	for name, args := range map[string][]string{
		"--version": {"--version", "--api-url", srv.URL},
		"status":    {"status", "--root", root, "--api-url", srv.URL},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			done := make(chan struct{})
			go func() {
				Run(args, &stdout, &stderr)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the command waited for the update check")
			}
			assert.NotContains(t, stdout.String(), "newer stable release")
		})
	}
}

// TestUpdateCheckIsSwitchedOffBeforeItIsMade: the config option and the
// environment variable are both refusals to ask, not refusals to print. A
// request that went out anyway would still be a request to GitHub on every
// single run of a repository that said no.
func TestUpdateCheckIsSwitchedOffBeforeItIsMade(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.0.0"

	for name, tc := range map[string]struct {
		cfg models.File
		env string
	}{
		"updateCheck: false": {cfg: models.File{UpdateCheck: models.Bool(false)}, env: "1"},
		"the environment":    {cfg: models.File{}, env: "0"},
		"json output":        {cfg: models.File{LogFormat: "json"}, env: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(releasesHandler(t, "9.9.9", &hits))
			defer srv.Close()
			t.Setenv(updateCheckEnv, tc.env)

			var stdout, stderr bytes.Buffer
			Run([]string{"status", "--root", updateRepo(t, tc.cfg), "--api-url", srv.URL}, &stdout, &stderr)
			assert.Zero(t, hits, "nothing was asked")
			assert.NotContains(t, stdout.String(), "newer stable release")
		})
	}
}

// TestUpdateCheckStaysAwayFromALocalBuild: "dev" compares to nothing, so a
// developer's own build never mentions updates and never calls anywhere.
func TestUpdateCheckStaysAwayFromALocalBuild(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(releasesHandler(t, "9.9.9", &hits))
	defer srv.Close()
	t.Setenv(updateCheckEnv, "1")

	var stdout, stderr bytes.Buffer
	Run([]string{"--version", "--api-url", srv.URL}, &stdout, &stderr)
	assert.Zero(t, hits)
	assert.Contains(t, stdout.String(), "dispat dev (")
}

// TestEnvAllowsUpdateCheckReadsOnlyAClearNo: the variable is a switch, and
// anything that is not a plain false leaves the default alone rather than
// turning the feature off by accident.
func TestEnvAllowsUpdateCheckReadsOnlyAClearNo(t *testing.T) {
	for value, want := range map[string]bool{
		"0": false, "false": false, "FALSE": false, " 0 ": false,
		"1": true, "true": true, "": true, "maybe": true,
	} {
		t.Setenv(updateCheckEnv, value)
		assert.Equal(t, want, envAllowsUpdateCheck(), "value %q", value)
	}
	os.Unsetenv(updateCheckEnv)
	assert.True(t, envAllowsUpdateCheck(), "unset means the default, which is on")
}

// TestSelfUpdateUsageMistakes: every combination that cannot mean anything is
// refused before a config file is looked for or a request is made, with the
// usage exit rather than a failure exit.
func TestSelfUpdateUsageMistakes(t *testing.T) {
	for name, args := range map[string][]string{
		"a positional argument": {"self-update", "1.2.0"},
		"rollback and release":  {"self-update", "--rollback", "--release", "1.2.0"},
		"rollback and force":    {"self-update", "--rollback", "--force"},
		"rollback and a line":   {"self-update", "--rollback", "--prerelease"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			assert.Equal(t, 2, Run(args, &stdout, &stderr))
		})
	}

	// --rollback and --check do compose: the pair asks what a rollback would
	// restore, which is a question worth being able to ask.
	var stdout, stderr bytes.Buffer
	assert.NotEqual(t, 2, Run([]string{"self-update", "--rollback", "--check"}, &stdout, &stderr))
}

// TestSelfUpdateNeedsNoConfigFile: it is about the binary, not about any
// repository it happens to be standing in, so it runs the same in an empty
// directory as in a monorepo.
func TestSelfUpdateNeedsNoConfigFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// A local build refuses, which is exit 1 — the point is that it got as far
	// as refusing rather than failing to find a configuration.
	code := Run([]string{"self-update", "--root", t.TempDir()}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout.String()+stderr.String(), "config file not found")
}

// TestUpdateSourceIsDispatsOwnRepository: the github block of a config says
// where the user's packages go, and asking it for dispat's release tags would
// come back empty forever. Only a flag the user set redirects the source.
func TestUpdateSourceIsDispatsOwnRepository(t *testing.T) {
	fs, o := newFlagSet()
	require.NoError(t, fs.Parse(nil))
	src := updateSource(o, fs)
	assert.Empty(t, src.Owner, "the defaults are the package's own")
	assert.Empty(t, src.Repo)
	assert.Empty(t, src.APIURL)

	fs, o = newFlagSet()
	require.NoError(t, fs.Parse([]string{"--owner", "acme", "--repo", "tools",
		"--api-url", "https://ghe.acme.test/api/v3"}))
	src = updateSource(o, fs)
	assert.Equal(t, "acme", src.Owner)
	assert.Equal(t, "tools", src.Repo)
	assert.Equal(t, "https://ghe.acme.test/api/v3", src.APIURL)
}

// TestUpdateSourceReadsTheNamedTokenVariable: a token only raises the rate
// limit, so the conventional variable is tried when nothing named one.
func TestUpdateSourceReadsTheNamedTokenVariable(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "conventional")
	t.Setenv("ACME_TOKEN", "named")

	fs, o := newFlagSet()
	require.NoError(t, fs.Parse(nil))
	assert.Equal(t, "conventional", updateSource(o, fs).Token)

	fs, o = newFlagSet()
	require.NoError(t, fs.Parse([]string{"--token-env", "ACME_TOKEN"}))
	assert.Equal(t, "named", updateSource(o, fs).Token)
}
