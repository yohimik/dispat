package app

// The self-update command's decisions, short of actually replacing a binary:
// which release a set of flags selects, when it refuses, and what --check
// answers. Replacing the binary for real is the black-box suite's job, since
// it needs binaries that run.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// releases serves a listing and by-tag lookups over the given tags, with a
// binary for every platform so asset selection never gets in the way.
func releases(t *testing.T, tags ...string) selfupdate.Source {
	t.Helper()
	list := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		list = append(list, map[string]any{
			"tag_name": tag, "draft": false,
			"prerelease": len(tag) > 0 && tag[len(tag)-1] >= '0' && strings.Contains(tag, "-rc."),
			"assets": []map[string]any{{
				"name": selfupdate.CurrentAssetName(), "browser_download_url": "http://example.invalid/x",
				"size": 1, "digest": "",
			}},
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tag, ok := afterTags(req.URL.Path); ok {
			for _, r := range list {
				if r["tag_name"] == tag {
					_ = json.NewEncoder(w).Encode(r)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)
	return selfupdate.Source{APIURL: srv.URL, Owner: "o", Repo: "r"}
}

func afterTags(path string) (string, bool) {
	_, tag, ok := strings.Cut(path, "/releases/tags/")
	return tag, ok
}

// opts is one invocation, with somewhere to collect what it printed.
func opts(t *testing.T, build selfupdate.Build, src selfupdate.Source) (*SelfUpdateOptions, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &SelfUpdateOptions{
		Build: build, Source: src, GOOS: "linux", GOARCH: "amd64",
		Out: &out, Log: zerolog.New(&out),
	}, &out
}

var released = selfupdate.Build{Version: "1.0.0", Origin: selfupdate.OriginRelease}

// TestSelfUpdateCheckAnswersWhetherItWouldChangeAnything: --check touches
// nothing and answers one question, so its exit code composes with every
// selection flag rather than meaning something different for each.
func TestSelfUpdateCheckAnswersWhetherItWouldChangeAnything(t *testing.T) {
	src := releases(t, "services/dispat/v1.0.0", "services/dispat/v1.2.0")

	for name, tc := range map[string]struct {
		build   selfupdate.Build
		release string
		force   bool
		want    bool
	}{
		"behind the latest":    {build: released, want: true},
		"already the latest":   {build: selfupdate.Build{Version: "1.2.0", Origin: selfupdate.OriginRelease}},
		"ahead of the latest":  {build: selfupdate.Build{Version: "2.0.0", Origin: selfupdate.OriginRelease}},
		"a named older one":    {build: selfupdate.Build{Version: "1.2.0", Origin: selfupdate.OriginRelease}, release: "1.0.0", want: true},
		"the one already run":  {build: released, release: "1.0.0"},
		"forced onto the same": {build: selfupdate.Build{Version: "1.2.0", Origin: selfupdate.OriginRelease}, force: true, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			o, out := opts(t, tc.build, src)
			o.Check, o.Release, o.Force = true, tc.release, tc.force
			pending, err := SelfUpdate(context.Background(), *o)
			require.NoError(t, err)
			assert.Equal(t, tc.want, pending)
			assert.Contains(t, out.String(), "current   dispat "+tc.build.Version)
			if !tc.want {
				assert.Contains(t, out.String(), "nothing to install")
			}
		})
	}
}

// TestSelfUpdateNeverDowngradesOnItsOwn: a release older than what is running
// is not an update, and saying so is the whole protection against a listing
// whose newest entry is a backport.
func TestSelfUpdateNeverDowngradesOnItsOwn(t *testing.T) {
	src := releases(t, "services/dispat/v1.0.0")
	o, out := opts(t, selfupdate.Build{Version: "2.0.0", Origin: selfupdate.OriginRelease}, src)
	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "already the latest release")
	assert.Contains(t, out.String(), "--force")
}

// TestSelfUpdateRefusesToReplaceWhatItDoesNotOwn: a local build has no
// version to compare, and a go install build would be undone by the next go
// install. Both are refusals that name the way forward.
func TestSelfUpdateRefusesToReplaceWhatItDoesNotOwn(t *testing.T) {
	src := releases(t, "services/dispat/v1.2.0")

	o, _ := opts(t, selfupdate.Build{Version: "dev", Origin: selfupdate.OriginDev}, src)
	_, err := SelfUpdate(context.Background(), *o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local build")

	o, _ = opts(t, selfupdate.Build{Version: "1.0.0", Origin: selfupdate.OriginGoInstall}, src)
	_, err = SelfUpdate(context.Background(), *o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), selfupdate.GoInstallCommand)
}

// TestSelfUpdateCheckStillWorksForAGoInstallBuild: it cannot replace the
// binary, but it can say a release is out and how to get it, which is the one
// place that advice belongs.
func TestSelfUpdateCheckStillWorksForAGoInstallBuild(t *testing.T) {
	src := releases(t, "services/dispat/v1.2.0")
	o, out := opts(t, selfupdate.Build{Version: "1.0.0", Origin: selfupdate.OriginGoInstall}, src)
	o.Check = true

	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.True(t, pending)
	assert.Contains(t, out.String(), "go install")
	assert.Contains(t, out.String(), selfupdate.GoInstallCommand)
	assert.NotContains(t, out.String(), "dispat self-update", "the wrong advice for this build")
}

// TestSelfUpdateWithoutABinaryForThePlatform: three of six platforms would
// have gone unserved before the build matrix widened, and one still can on a
// release cut before a platform was added. The refusal says what is there.
func TestSelfUpdateWithoutABinaryForThePlatform(t *testing.T) {
	src := releases(t, "services/dispat/v1.2.0")
	o, _ := opts(t, released, src)
	o.GOOS, o.GOARCH = "plan9", "386"

	_, err := SelfUpdate(context.Background(), *o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dispat-plan9-386")
	assert.Contains(t, err.Error(), selfupdate.CurrentAssetName(), "and names what the release does carry")
}

// TestSelfUpdateWithNoStableReleaseYet: the state dispat's own repository is
// in until 1.0.0. The refusal points at the flag that would find something.
func TestSelfUpdateWithNoStableReleaseYet(t *testing.T) {
	src := releases(t, "services/dispat/v1.0.0-rc.3")
	o, _ := opts(t, selfupdate.Build{Version: "1.0.0-rc.2", Origin: selfupdate.OriginRelease}, src)

	_, err := SelfUpdate(context.Background(), *o)
	require.ErrorIs(t, err, selfupdate.ErrNoRelease)
	assert.Contains(t, err.Error(), "--prerelease")

	// With the flag, the candidate is found and is an update.
	o, out := opts(t, selfupdate.Build{Version: "1.0.0-rc.2", Origin: selfupdate.OriginRelease}, src)
	o.Source.Prerelease = true
	o.Check = true
	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.True(t, pending)
	assert.Contains(t, out.String(), "1.0.0-rc.3")
}

// TestSelfUpdateReportsAVersionNobodyPublished: --release 9.9.9 is a mistake
// worth naming, and it must never read as "you are up to date".
func TestSelfUpdateReportsAVersionNobodyPublished(t *testing.T) {
	src := releases(t, "services/dispat/v1.2.0")
	o, _ := opts(t, released, src)
	o.Release = "9.9.9"

	pending, err := SelfUpdate(context.Background(), *o)
	require.ErrorIs(t, err, selfupdate.ErrNoRelease)
	assert.False(t, pending)
}

// TestSelfUpdateRollbackWithNothingKept: the ordinary state of a binary that
// has never been updated. The refusal names both ways forward, because the
// backup may simply have aged out.
func TestSelfUpdateRollbackWithNothingKept(t *testing.T) {
	o, out := opts(t, released, selfupdate.Source{})
	o.Rollback = true

	_, err := SelfUpdate(context.Background(), *o)
	require.ErrorIs(t, err, selfupdate.ErrNoBackup)
	assert.Contains(t, err.Error(), "--release")

	// Under --check the same state is an answer rather than a failure: there
	// is nothing to roll back to, so nothing would change.
	o, out = opts(t, released, selfupdate.Source{})
	o.Rollback, o.Check = true, true
	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "no backup to roll back to")
}

// TestSelfUpdateSpeaksJSONWhenAskedTo: the report is for a person, the events
// are for the stream CI already ingests, and one code path renders both.
func TestSelfUpdateSpeaksJSONWhenAskedTo(t *testing.T) {
	src := releases(t, "services/dispat/v1.2.0")
	o, out := opts(t, released, src)
	o.Check, o.JSON = true, true

	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.True(t, pending)

	var event map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event))
	assert.Equal(t, "update check", event["message"])
	assert.Equal(t, "1.0.0", event["version"])
	assert.Equal(t, "1.2.0", event["latest"])
	assert.Equal(t, true, event["pending"])
	assert.NotContains(t, out.String(), "install it with", "the report stays out of the stream")
}

// TestHumanBytesReadsLikeADownload: the size is there to set an expectation
// before a transfer, so it is rendered the way a person reads one.
func TestHumanBytesReadsLikeADownload(t *testing.T) {
	assert.Equal(t, "0 B", humanBytes(0))
	assert.Equal(t, "512 B", humanBytes(512))
	assert.Equal(t, "1.0 KiB", humanBytes(1024))
	assert.Equal(t, "13.3 MiB", humanBytes(13_896_386))
	assert.Equal(t, "1.5 GiB", humanBytes(1610612736))
	assert.Equal(t, "1.0 TiB", humanBytes(1<<40))
}
