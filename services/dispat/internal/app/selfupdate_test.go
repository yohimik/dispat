package app

// The self-update command's decisions, short of actually replacing a binary:
// which release a set of flags selects, when it refuses, and what --check
// answers. Replacing the binary for real is the black-box suite's job, since
// it needs binaries that run.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
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
	src, _ := releaseRepo(t, "", tags...)
	return src
}

// releaseRepo is what releases is built on, with both download addresses a
// real release carries and a count of which one was asked for.
//
// A token makes the repository private: the listing and the asset's API
// endpoint then demand it, and the public URL answers with a sign-in page.
//
// Neither address ever serves an asset of the size the listing advertises, on
// purpose. The binary a self-update would replace here is the test binary, so
// the download has to fail after the request has been made rather than
// succeed: what it proves is which address was asked and with what, which is
// decided before a single byte arrives.
func releaseRepo(t *testing.T, token string, tags ...string) (selfupdate.Source, *routes) {
	t.Helper()
	var base string
	seen := &routes{}
	list := func() []map[string]any {
		out := make([]map[string]any, 0, len(tags))
		for _, tag := range tags {
			name := selfupdate.CurrentAssetName()
			out = append(out, map[string]any{
				"tag_name": tag, "draft": false,
				"prerelease": len(tag) > 0 && tag[len(tag)-1] >= '0' && strings.Contains(tag, "-rc."),
				"assets": []map[string]any{{
					"name": name, "browser_download_url": base + "/download/" + name,
					"url":  base + "/assets/" + name,
					"size": 1, "digest": "",
				}},
			})
		}
		return out
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authed := token == "" || req.Header.Get("Authorization") == "Bearer "+token
		switch {
		case strings.HasPrefix(req.URL.Path, "/download/"):
			seen.public++
			fmt.Fprint(w, signInPage)
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			seen.api++
			if !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			assert.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
			fmt.Fprint(w, "the released binary")
		default:
			if !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			releases := list()
			if tag, ok := afterTags(req.URL.Path); ok {
				for _, r := range releases {
					if r["tag_name"] == tag {
						_ = json.NewEncoder(w).Encode(r)
						return
					}
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(releases)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return selfupdate.Source{APIURL: srv.URL, Owner: "o", Repo: "r", Token: token}, seen
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

// TestSelfUpdateFromAPrivateRepositoryUsesTheAssetEndpoint: the token the
// source read the listing with reaches the installer, so the download goes to
// the asset's API endpoint carrying it and the public URL, which would answer
// with a sign-in page, is never asked. The transfer is then refused on its
// size, which is how this fixture stops short of replacing the test binary.
func TestSelfUpdateFromAPrivateRepositoryUsesTheAssetEndpoint(t *testing.T) {
	src, seen := releaseRepo(t, "sesame", "services/dispat/v1.2.0")
	o, _ := opts(t, released, src)
	o.GOOS, o.GOARCH = runtime.GOOS, runtime.GOARCH

	_, err := SelfUpdate(context.Background(), *o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the download is incomplete",
		"the endpoint answered with bytes, which is what the credential bought")
	assert.Equal(t, 1, seen.api)
	assert.Zero(t, seen.public, "a token means the public URL is never asked")
}

// TestSelfUpdateFromAPublicRepositoryStaysOnThePublicURL: the fence around the
// change. Every real listing names an API endpoint, and without a token the
// download still goes to the browser URL as it always did.
func TestSelfUpdateFromAPublicRepositoryStaysOnThePublicURL(t *testing.T) {
	src, seen := releaseRepo(t, "", "services/dispat/v1.2.0")
	o, _ := opts(t, released, src)
	o.GOOS, o.GOARCH = runtime.GOOS, runtime.GOARCH

	_, err := SelfUpdate(context.Background(), *o)
	require.Error(t, err)
	assert.Equal(t, 1, seen.public)
	assert.Zero(t, seen.api, "no token, no reason to touch the endpoint that wants one")
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

	event := findEvent(t, out.Bytes(), "update check")
	assert.Equal(t, "1.0.0", event["version"])
	assert.Equal(t, "1.2.0", event["latest"])
	assert.Equal(t, true, event["pending"])
	assert.NotContains(t, out.String(), "install it with", "the report stays out of the stream")
}

// findEvent picks one event out of the stream by its message. The stream
// carries more than the answer — a release with no readable notes says so on
// its way past — so a test that wants one event has to name it.
func findEvent(t *testing.T, stream []byte, message string) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(stream), []byte("\n")) {
		var event map[string]any
		require.NoError(t, json.Unmarshal(line, &event), "every line is one event")
		if event["message"] == message {
			return event
		}
	}
	t.Fatalf("no %q event in %s", message, stream)
	return nil
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

// notesBody is the shape dispat's own releases carry: the sections first, then
// the rule and the install commands the release page closes with.
const notesBody = "### Features\n\n- print the notes after an update\n\n### Fixes\n\n" +
	"- stop a truncated listing failing opaquely\n\n---\n\n**Install this version:**\n\n" +
	"```sh\ncurl -fsSL https://example.invalid/install.sh | sh\n```\n"

// withNotes serves one release carrying a body and a release page, which is
// what the API actually returns and what the plain `releases` helper leaves out.
func withNotes(t *testing.T, tag, body string) selfupdate.Source {
	t.Helper()
	rel := map[string]any{
		"tag_name": tag, "draft": false, "prerelease": false, "body": body,
		"html_url": "https://github.com/o/r/releases/tag/" + strings.ReplaceAll(tag, "/", "%2F"),
		"assets": []map[string]any{{
			"name": selfupdate.CurrentAssetName(), "browser_download_url": "http://example.invalid/x",
			"size": 1, "digest": "",
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got, ok := afterTags(req.URL.Path); ok {
			if got != tag {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{rel})
	}))
	t.Cleanup(srv.Close)
	return selfupdate.Source{APIURL: srv.URL, Owner: "o", Repo: "r"}
}

// TestSelfUpdateCheckShowsWhatWouldArrive: --check is the invocation you run
// while deciding, so it answers with the changes rather than only the number.
// The install commands the body carries stay out of it: they are markup for a
// web page, and the reader already has dispat.
func TestSelfUpdateCheckShowsWhatWouldArrive(t *testing.T) {
	o, out := opts(t, released, withNotes(t, "services/dispat/v1.2.0", notesBody))
	o.Check = true

	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.True(t, pending)

	text := out.String()
	assert.Contains(t, text, "available dispat 1.2.0")
	assert.Contains(t, text, "what changed in 1.2.0")
	assert.Contains(t, text, "Features")
	assert.Contains(t, text, "- print the notes after an update")
	assert.Contains(t, text, "full changelog: https://github.com/o/r/blob/refs/tags/"+
		"services/dispat/v1.2.0/services/dispat/CHANGELOG.md")
	assert.NotContains(t, text, "curl", "the install commands are the page's, not the terminal's")
	assert.Contains(t, text, "install it with: dispat self-update",
		"the notes go above the instruction, not instead of it")
}

// TestSelfUpdateCheckSaysNothingAboutTheReleaseYouHave: being current means
// there is nothing to read, and printing the running version's own notes would
// read as if an update were waiting.
func TestSelfUpdateCheckSaysNothingAboutTheReleaseYouHave(t *testing.T) {
	o, out := opts(t, released, withNotes(t, "services/dispat/v1.0.0", notesBody))
	o.Check = true

	pending, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "nothing to install")
	assert.NotContains(t, out.String(), "what changed")
	assert.NotContains(t, out.String(), "full changelog")
	assert.NotContains(t, out.String(), "release notes",
		"a release nobody would install is not read at all, so it is not reported on either")
}

// TestSelfUpdateWithoutReadableNotes: a release whose body dispat makes nothing
// of still gets its link, because "here is where to look" beats silence, and it
// says so on the log rather than in the report.
func TestSelfUpdateWithoutReadableNotes(t *testing.T) {
	o, out := opts(t, released, withNotes(t, "services/dispat/v1.2.0", ""))
	o.Check = true

	_, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)

	text := out.String()
	assert.NotContains(t, text, "what changed", "no heading over an empty list")
	assert.Contains(t, text, "full changelog: https://github.com/o/r/blob/refs/tags/")
	assert.Contains(t, text, "the release carries no notes", "the reason is on the log")
}

// TestSelfUpdateNotesReachTheJSONStream: the report is for a person and the
// event is for the stream, and both carry the same two things, so a CI job that
// updates dispat can post what changed without scraping stdout.
func TestSelfUpdateNotesReachTheJSONStream(t *testing.T) {
	o, out := opts(t, released, withNotes(t, "services/dispat/v1.2.0", notesBody))
	o.Check, o.JSON = true, true

	_, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)

	event := findEvent(t, out.Bytes(), "update check")
	assert.Contains(t, event["notes"], "what changed in 1.2.0")
	assert.Contains(t, event["notes"], "print the notes after an update")
	assert.Equal(t, "https://github.com/o/r/blob/refs/tags/services/dispat/v1.2.0/"+
		"services/dispat/CHANGELOG.md", event["changelog"])
	assert.NotContains(t, out.String(), "full changelog:", "the report stays out of the stream")
}

// TestSelfUpdateRollbackSaysNothingAboutNotes: a rollback fetches nothing, so
// there is no release whose notes these would be. Linking a changelog for a
// local file swap would be inventing an answer.
func TestSelfUpdateRollbackSaysNothingAboutNotes(t *testing.T) {
	o, out := opts(t, released, withNotes(t, "services/dispat/v1.2.0", notesBody))
	o.Rollback, o.Check = true, true

	_, err := SelfUpdate(context.Background(), *o)
	require.NoError(t, err)
	assert.NotContains(t, out.String(), "what changed")
	assert.NotContains(t, out.String(), "full changelog")
}
