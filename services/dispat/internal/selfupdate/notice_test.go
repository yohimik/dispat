package selfupdate

import (
	"context"

	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme/v2"
)

// version is a parsed version, for the tests that build a Result by hand.
func version(t *testing.T, s string) ccme.Version {
	t.Helper()
	v, err := ccme.ParseVersion(s)
	require.NoError(t, err)
	return v
}

// TestCheckAnswersWhenTheServerDoes: the ordinary path. The result carries
// both versions so the caller can render either answer without asking again.
func TestCheckAnswersWhenTheServerDoes(t *testing.T) {
	s := *listing(t, releaseJSON("services/dispat/v1.2.0", false, false))
	ch := Check(context.Background(), s, Build{Version: "1.0.0", Origin: OriginRelease})

	select {
	case res := <-ch:
		assert.Equal(t, "1.0.0", res.Current.String())
		assert.Equal(t, "1.2.0", res.Latest.String())
		assert.True(t, res.Behind())
	case <-time.After(5 * time.Second):
		t.Fatal("the check never answered")
	}
}

// TestCheckIsSilentWhenItCannotAnswer: offline, rate limited, refused,
// cancelled — every failure looks the same from the outside, which is nothing
// on the channel and so nothing printed. There is one behaviour to reason
// about and it is silence.
func TestCheckIsSilentWhenItCannotAnswer(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer refusing.Close()

	for name, s := range map[string]Source{
		"a refusal":      {APIURL: refusing.URL},
		"nothing at all": {APIURL: "http://127.0.0.1:1"},
		"not a url":      {APIURL: "://"},
	} {
		t.Run(name, func(t *testing.T) {
			ch := Check(context.Background(), s, Build{Version: "1.0.0"})
			select {
			case res := <-ch:
				t.Fatalf("a failed check must say nothing, got %+v", res)
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

// TestCheckNeverStartsForALocalBuild: "dev" compares to nothing, so there is
// no question to ask and no request to make.
func TestCheckNeverStartsForALocalBuild(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	ch := Check(context.Background(), Source{APIURL: srv.URL}, Build{Version: "dev", Origin: OriginDev})
	select {
	case <-ch:
		t.Fatal("a local build has nothing to report")
	case <-time.After(200 * time.Millisecond):
	}
	assert.Zero(t, hits, "and nothing to ask")
}

// TestCheckIgnoresThePrereleaseSetting: someone running a release candidate
// wants to hear about the stable it leads to, not about the next candidate,
// so the notice is about stable releases whatever the command was asked for.
func TestCheckIgnoresThePrereleaseSetting(t *testing.T) {
	s := *listing(t,
		releaseJSON("services/dispat/v1.0.0", false, false),
		releaseJSON("services/dispat/v2.0.0-rc.1", true, false),
	)
	s.Prerelease = true
	ch := Check(context.Background(), s, Build{Version: "0.9.0", Origin: OriginRelease})

	select {
	case res := <-ch:
		assert.Equal(t, "1.0.0", res.Latest.String(), "the candidate is not what a notice offers")
	case <-time.After(5 * time.Second):
		t.Fatal("the check never answered")
	}
}

// TestCheckStopsWithTheContext: the caller cancels on the way out, and the
// request goes with it rather than outliving the process's interest in it.
func TestCheckStopsWithTheContext(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch := Check(ctx, Source{APIURL: srv.URL}, Build{Version: "1.0.0"})
	<-started
	cancel()
	select {
	case <-ch:
		t.Fatal("a cancelled check says nothing")
	case <-time.After(300 * time.Millisecond):
	}
}

// TestNoticeSaysSomethingOnlyWhenThereIsSomethingToSay: a command that is
// current prints nothing at all, which is what keeps this from becoming noise
// on every invocation forever.
func TestNoticeSaysSomethingOnlyWhenThereIsSomethingToSay(t *testing.T) {
	current := Result{Current: version(t, "1.2.0"), Latest: version(t, "1.2.0"), Origin: OriginRelease}
	assert.Empty(t, Notice(current, "linux"))
	assert.Contains(t, Status(current, "linux"), "this is the latest stable release")

	ahead := Result{Current: version(t, "1.3.0-rc.1"), Latest: version(t, "1.2.0"), Origin: OriginRelease}
	assert.Empty(t, Notice(ahead, "linux"), "running ahead of the latest stable is not being behind")

	behind := Result{Current: version(t, "1.0.0"), Latest: version(t, "1.2.0"), Origin: OriginRelease}
	notice := Notice(behind, "linux")
	assert.Contains(t, notice, "1.2.0")
	assert.Contains(t, notice, "you have 1.0.0")
	assert.Contains(t, notice, `run "dispat self-update" to install it`)
	assert.Equal(t, notice, Status(behind, "linux"), "--version says the same thing")
}

// TestNoticeSuggestsNothingToAGoInstallBuild: pointing it at dispat
// self-update would be wrong, since the next go install undoes it, and the
// command that knows the right answer is the one that prints it.
func TestNoticeSuggestsNothingToAGoInstallBuild(t *testing.T) {
	res := Result{Current: version(t, "1.0.0"), Latest: version(t, "1.2.0"), Origin: OriginGoInstall}
	notice := Notice(res, "darwin")
	assert.Contains(t, notice, "1.2.0", "it still says a release is out")
	assert.NotContains(t, notice, "self-update")
	assert.NotContains(t, notice, "notarised", "and does not explain an install it is not suggesting")
}

// TestMacNoteIsForMacOnly: dispat's binaries are not notarised, which is
// worth knowing before an update and again after one, and means nothing
// anywhere else.
func TestMacNoteIsForMacOnly(t *testing.T) {
	assert.Empty(t, MacNote("linux", "/usr/local/bin/dispat"))
	assert.Empty(t, MacNote("windows", ""))

	before := MacNote("darwin", "")
	assert.Contains(t, before, "not notarised")
	assert.NotContains(t, before, "xattr", "with no binary installed yet there is no path to name")

	after := MacNote("darwin", "/usr/local/bin/dispat")
	assert.Contains(t, after, "xattr -d com.apple.quarantine /usr/local/bin/dispat")
}

// TestDescribeTellsTheThreeBuildsApart: how the binary was produced decides
// how it is updated, and --version says so where the reader is already
// looking.
func TestDescribeTellsTheThreeBuildsApart(t *testing.T) {
	released := Describe("1.2.0")
	assert.Equal(t, Build{Version: "1.2.0", Origin: OriginRelease}, released)
	assert.Equal(t, runtime.GOOS+"_"+runtime.GOARCH, released.Platform(runtime.GOOS, runtime.GOARCH))

	// The test binary carries no module version, so an unstamped build is
	// the local one; the go install branch is what the integration suite and
	// the platform line cover.
	local := Describe("dev")
	assert.Equal(t, "dev", local.Version)
	assert.Equal(t, OriginDev, local.Origin)
	assert.Equal(t, "linux_amd64", local.Platform("linux", "amd64"))

	goInstall := Build{Version: "1.2.0", Origin: OriginGoInstall}
	assert.Equal(t, "linux_amd64, go install", goInstall.Platform("linux", "amd64"))
}

// TestAssetNameMatchesTheBuildScript: this and services/dispat/Dockerfile,
// which cross-compiles the release binaries under these names, are the two
// halves of one contract, and a self-update that cannot find its binary is
// what a disagreement looks like.
func TestAssetNameMatchesTheBuildScript(t *testing.T) {
	assert.Equal(t, "dispat-linux-amd64", AssetName("linux", "amd64"))
	assert.Equal(t, "dispat-linux-arm64", AssetName("linux", "arm64"))
	assert.Equal(t, "dispat-darwin-amd64", AssetName("darwin", "amd64"))
	assert.Equal(t, "dispat-darwin-arm64", AssetName("darwin", "arm64"))
	assert.Equal(t, "dispat-windows-amd64.exe", AssetName("windows", "amd64"))
	assert.Equal(t, "dispat-windows-arm64.exe", AssetName("windows", "arm64"))
	assert.Equal(t, AssetName(runtime.GOOS, runtime.GOARCH), CurrentAssetName())
}

// TestExecutableResolvesThroughASymlink: a symlinked install must replace the
// real file, or the update lands somewhere nothing runs from.
func TestExecutableResolvesThroughASymlink(t *testing.T) {
	exe, err := Executable()
	require.NoError(t, err)
	assert.NotEmpty(t, exe)
	resolved, err := filepath.EvalSymlinks(exe)
	require.NoError(t, err)
	assert.Equal(t, resolved, exe, "the path is already fully resolved")
}

// TestTempPatternKeepsTheExtension: Windows will not execute a file without
// one, and both the download and the rollback run the file they park.
func TestTempPatternKeepsTheExtension(t *testing.T) {
	assert.Equal(t, "dispat-update-*", tempPattern("/usr/local/bin/dispat", "update"))
	assert.Equal(t, "dispat-update-*.exe", tempPattern(`C:\bin\dispat.exe`, "update"))
	assert.Equal(t, "dispat-rollback-*", tempPattern("/usr/local/bin/dispat", "rollback"))
}
