package app

// The install command's decisions, short of actually installing a binary:
// which release and which file a set of flags selects, what --check answers,
// and every refusal that costs no request. Installing for real is the
// black-box suite's job, since it needs a file that runs.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/install"
	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// toolReleases serves a listing whose releases carry the named assets, so a
// scenario can put whichever files it is about in front of the command.
func toolReleases(t *testing.T, tag string, assets ...string) selfupdate.Source {
	t.Helper()
	src, _ := toolRepo(t, "", tag, assets...)
	return src
}

// routes counts which of a release's two download addresses the command asked
// for, which is the whole of what the token is supposed to decide.
type routes struct{ public, api int }

// toolRepo is a repository the install command can be pointed at, with the
// listing and both download addresses a real release has: the public browser
// URL and the asset's own API endpoint.
//
// An empty token is a public repository, where everything answers to everyone.
// A token makes it private, and then the fixture behaves the way GitHub does:
// the listing and the API endpoint both demand that credential, and the public
// URL answers 200 with the sign-in page rather than with the binary, which is
// what makes an unauthenticated download fail on its checksum instead of on
// its status.
func toolRepo(t *testing.T, token, tag string, assets ...string) (selfupdate.Source, *routes) {
	t.Helper()
	var base string
	seen := &routes{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authed := token == "" || req.Header.Get("Authorization") == "Bearer "+token
		switch {
		case strings.HasPrefix(req.URL.Path, "/download/"):
			seen.public++
			if token != "" {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, signInPage)
				return
			}
			fmt.Fprint(w, toolBody)
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			seen.api++
			if !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			assert.Equal(t, "application/octet-stream", req.Header.Get("Accept"),
				"the API endpoint serves the bytes only when they are what was asked for")
			fmt.Fprint(w, toolBody)
		default:
			if !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			list := []map[string]any{{"tag_name": tag, "draft": false, "prerelease": false,
				"assets": assetList(base, assets)}}
			w.Header().Set("Content-Type", "application/json")
			if want, ok := afterTags(req.URL.Path); ok {
				if want == tag {
					_ = json.NewEncoder(w).Encode(list[0])
					return
				}
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(list)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return selfupdate.Source{APIURL: srv.URL, Owner: "acme", Repo: "tool",
		TagPrefix: "v", Command: install.Command, Token: token}, seen
}

// signInPage stands in for what github.com serves at the public download URL
// of a private repository: a page, with a 200 on it, that is not the asset.
const signInPage = "<!DOCTYPE html><html><body>Sign in to GitHub</body></html>"

// toolBody is what the fixture's releases publish, so a destination holding it
// really is the file the release describes and "already installed" is a fact
// about the bytes rather than about the fixture.
const toolBody = "the released tool"

// assetList describes the assets the way the API does, with both addresses:
// every asset in a real listing carries "url" alongside
// "browser_download_url", so a fixture that omits it could never show which of
// the two the download chose.
func assetList(base string, names []string) []map[string]any {
	sum := sha256.Sum256([]byte(toolBody))
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name, "size": len(toolBody),
			"browser_download_url": base + "/download/" + name,
			"url":                  base + "/assets/" + name,
			"digest":               "sha256:" + hex.EncodeToString(sum[:])})
	}
	return out
}

// instOpts is one invocation, with somewhere to collect what it printed and a
// folder nothing else is using.
func instOpts(t *testing.T, src selfupdate.Source) (*InstallOptions, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &InstallOptions{
		Repository: install.Repository{Owner: "acme", Repo: "tool"},
		Source:     src, Asset: "tool-{os}-{arch}", BinDir: t.TempDir(),
		GOOS: "linux", GOARCH: "amd64",
		Out: &out,
		Log: zerolog.New(&out),
	}, &out
}

// TestInstallCheckAnswersWhetherThereIsAnythingToDo: the whole of what --check
// decides, and the only thing that decides its exit code. A destination
// holding nothing is something to install; one holding this exact file is not.
func TestInstallCheckAnswersWhetherThereIsAnythingToDo(t *testing.T) {
	opts, out := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	opts.Check = true

	pending, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending, "nothing is installed yet")
	assert.Contains(t, out.String(), "repository acme/tool")
	assert.Contains(t, out.String(), "release    1.4.0 (v1.4.0)")
	assert.Contains(t, out.String(), "install it with: dispat install")

	// The destination now holds exactly what the release published, so the
	// same invocation has nothing to do.
	writeDigestFile(t, filepath.Join(opts.BinDir, "tool"))
	out.Reset()
	pending, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "nothing to install")

	// ...unless it is forced, which is how a tampered file is repaired.
	opts.Force = true
	out.Reset()
	pending, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending)
}

// TestInstallCheckReportsAPipeInstead: a pipe has no destination file to
// compare against, so it is always something to do and the report says what
// the command would be handed rather than where a file would land.
func TestInstallCheckReportsAPipeInstead(t *testing.T) {
	opts, out := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	opts.Check, opts.Pipe = true, "tar -xz"

	pending, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending)
	assert.Contains(t, out.String(), "pipe       tar -xz")
	assert.Contains(t, out.String(), "in         "+opts.BinDir)
	assert.NotContains(t, out.String(), "install to")
}

// TestInstallCheckSpeaksJSON: the report is for a person and the event is for
// the stream CI already ingests, and the two say the same thing.
func TestInstallCheckSpeaksJSON(t *testing.T) {
	opts, out := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	opts.Check, opts.JSON = true, true

	pending, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending)
	event := jsonEvent(t, out.String(), "install check")
	assert.Equal(t, "acme/tool", event["repository"])
	assert.Equal(t, "v1.4.0", event["tag"])
	assert.Equal(t, "tool-linux-amd64", event["asset"])
	assert.Equal(t, true, event["pending"])
	assert.Equal(t, filepath.Join(opts.BinDir, "tool"), event["path"])

	// And the "already there" answer, which is the other half of the gate.
	writeDigestFile(t, filepath.Join(opts.BinDir, "tool"))
	opts.Check = false
	out.Reset()
	_, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	jsonEvent(t, out.String(), "already installed")

	// A pipe reports the command and the folder rather than a path.
	opts.Check, opts.Pipe = true, "tar -xz"
	out.Reset()
	_, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	event = jsonEvent(t, out.String(), "install check")
	assert.Equal(t, "tar -xz", event["pipe"])
	assert.NotContains(t, event, "path")
}

// TestInstallRefusesBeforeItFetchesAnything: every one of these is decided
// before a single byte of the asset moves, and each says what to do about it.
func TestInstallRefusesBeforeItFetchesAnything(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*InstallOptions)
		src    func(*testing.T) selfupdate.Source
		want   string
	}{
		"a release nobody published": {
			mutate: func(o *InstallOptions) { o.Release = "9.9.9" },
			want:   "no matching release",
		},
		"a repository with no releases at all": {
			src:  func(t *testing.T) selfupdate.Source { return toolReleases(t, "not-a-version") },
			want: "no matching release under \"v\"",
		},
		"a release with nothing for this platform": {
			src:  func(t *testing.T) selfupdate.Source { return toolReleases(t, "v1.4.0", "tool-plan9-386") },
			want: "tool-plan9-386",
		},
		"a placeholder nobody defines": {
			mutate: func(o *InstallOptions) { o.Asset = "tool-{arch64}" },
			want:   "{arch64}",
		},
		"a name that is a path": {
			mutate: func(o *InstallOptions) { o.Name = "../evil" },
			want:   "not a path",
		},
		"nowhere to install it": {
			mutate: func(o *InstallOptions) {
				o.BinDir = ""
				o.Env = emptyEnv{}
			},
			want: "--bin-dir",
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := toolReleases(t, "v1.4.0", "tool-linux-amd64")
			if tc.src != nil {
				src = tc.src(t)
			}
			opts, _ := instOpts(t, src)
			if tc.mutate != nil {
				tc.mutate(opts)
			}
			pending, err := Install(context.Background(), *opts)
			require.Error(t, err)
			assert.False(t, pending, "a refusal is never a pending install")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInstallRefusesADestinationItMustNotReplace: a name that is already a
// directory belongs to somebody, and installing over it would rename that
// directory aside to put a binary where it stood.
func TestInstallRefusesADestinationItMustNotReplace(t *testing.T) {
	opts, _ := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	require.NoError(t, os.Mkdir(filepath.Join(opts.BinDir, "tool"), 0o755))

	_, err := Install(context.Background(), *opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a folder")
	assert.DirExists(t, filepath.Join(opts.BinDir, "tool"), "and it is still where it was")
}

// TestInstallWithoutAChecksumSaysSo: a release that publishes none cannot be
// compared, and the honest answer is "install it": the guess that skips the
// install is the one that leaves a machine on an old binary forever.
func TestInstallWithoutAChecksumSaysSo(t *testing.T) {
	src := toolReleases(t, "v1.4.0", "tool-linux-amd64")
	src.Command = install.Command
	opts, out := instOpts(t, src)
	opts.Check = true
	// The same file the digest would have described, so only the missing
	// digest can be what makes this pending.
	writeDigestFile(t, filepath.Join(opts.BinDir, "tool"))
	opts.Source = noDigestSource(t, "v1.4.0", "tool-linux-amd64")

	pending, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending)
	assert.Contains(t, out.String(), "no checksum")
}

// TestInstallRollbackNeedsNoRelease: a rollback restores what is already
// installed, so it reads nothing and reports what it found either way.
func TestInstallRollbackNeedsNoRelease(t *testing.T) {
	opts, out := instOpts(t, selfupdate.Source{APIURL: "http://example.invalid"})
	opts.Rollback, opts.Check = true, true
	installed := filepath.Join(opts.BinDir, "tool")

	pending, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "no backup")

	require.NoError(t, os.WriteFile(installed, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(selfupdate.BackupPath(installed), []byte("previous"), 0o755))
	out.Reset()
	pending, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.True(t, pending, "there is something to restore")
	assert.Contains(t, out.String(), selfupdate.BackupPath(installed))

	opts.Check = false
	out.Reset()
	pending, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.False(t, pending)
	assert.Contains(t, out.String(), "rolled back tool at "+installed)
	assert.Equal(t, "previous", readFile(t, installed))
	assert.Equal(t, "current", readFile(t, selfupdate.BackupPath(installed)))

	// And with nothing left to restore, the refusal says how to get an old
	// version anyway.
	require.NoError(t, os.Remove(selfupdate.BackupPath(installed)))
	_, err = Install(context.Background(), *opts)
	require.ErrorIs(t, err, selfupdate.ErrNoBackup)
	assert.Contains(t, err.Error(), "--release")
}

// TestInstallRollbackSpeaksJSON: the same three answers as fields, for the
// job that has to record what it did to a runner.
func TestInstallRollbackSpeaksJSON(t *testing.T) {
	opts, out := instOpts(t, selfupdate.Source{APIURL: "http://example.invalid"})
	opts.Rollback, opts.JSON, opts.Check = true, true, true
	installed := filepath.Join(opts.BinDir, "tool")

	_, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, false, jsonEvent(t, out.String(), "no backup to roll back to")["pending"])

	require.NoError(t, os.WriteFile(installed, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(selfupdate.BackupPath(installed), []byte("previous"), 0o755))
	out.Reset()
	_, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, true, jsonEvent(t, out.String(), "a backup is available")["pending"])

	opts.Check = false
	out.Reset()
	_, err = Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, installed, jsonEvent(t, out.String(), "rolled back")["path"])
}

// TestInstallReportsADestinationItCannotRead: a file dispat may replace but
// cannot open is neither "already installed" nor "install over it": it is a
// machine the operator has to look at, and it is reported rather than guessed
// past.
func TestInstallReportsADestinationItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the scenario needs a file the running user cannot read")
	}
	opts, _ := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	opts.Check = true
	require.NoError(t, os.WriteFile(filepath.Join(opts.BinDir, "tool"), []byte(toolBody), 0o000))

	_, err := Install(context.Background(), *opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading")
}

// TestInstallReportsAFolderItCannotCreate: the install folder is created on
// the way, and a parent that refuses is a refusal rather than a download
// nobody can place.
func TestInstallReportsAFolderItCannotCreate(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	locked := t.TempDir()
	opts, _ := instOpts(t, toolReleases(t, "v1.4.0", "tool-linux-amd64"))
	opts.BinDir = filepath.Join(locked, "nested", "bin")
	require.NoError(t, os.Chmod(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, err := Install(context.Background(), *opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create")
}

// TestInstallNamesBothFlagsThatWouldFindARelease: a repository whose releases
// dispat can see none of has two things wrong with it and neither is guessable
// from outside, so both are named rather than either assumed.
func TestInstallNamesBothFlagsThatWouldFindARelease(t *testing.T) {
	opts, _ := instOpts(t, toolReleases(t, "not-a-version"))
	opts.Check = true

	_, err := Install(context.Background(), *opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `under "v"`)
	assert.Contains(t, err.Error(), "--prerelease")
	assert.Contains(t, err.Error(), "--tag-prefix")

	// With the prefix already dropped, only the prereleases are left to
	// suggest, and the refusal says which prefix found nothing.
	opts.Source.AnyTag = true
	_, err = Install(context.Background(), *opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with no tag prefix")

	// ...and with the prereleases already in, only the prefix is.
	opts.Source.Prerelease = true
	opts.Source.AnyTag = false
	_, err = Install(context.Background(), *opts)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "--prerelease")
	assert.Contains(t, err.Error(), "--tag-prefix")
}

// TestInstallSaysWhenTheFolderIsNotOnPath: a tool the shell cannot find is a
// successful install that looks like a failed one, so the one thing that makes
// it look that way is said out loud.
func TestInstallSaysWhenTheFolderIsNotOnPath(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, onPath("", dir), "no PATH at all reaches nothing")
	assert.False(t, onPath("/usr/bin:/bin", dir))
	assert.True(t, onPath("/usr/bin:"+dir+":/bin", dir))
	assert.True(t, onPath(dir+string(filepath.Separator)+":/bin", dir),
		"a trailing separator is the same folder")
	assert.False(t, onPath(string(filepath.ListSeparator)+string(filepath.ListSeparator), dir),
		"the empty entries a PATH picks up are not the current folder here")

	if runtime.GOOS != "windows" {
		// A symlinked folder is the same folder, which is what /var against
		// /private/var on macOS comes down to.
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		assert.True(t, onPath(link, dir))
		assert.False(t, onPath(filepath.Join(t.TempDir(), "absent"), dir),
			"a folder that is not there resolves to nothing rather than to everything")
	}
}

// TestInstallOptionsFallBackToTheRealMachine: the environment is a field so a
// test can ask for a machine it is not running on, and nil is the real one.
func TestInstallOptionsFallBackToTheRealMachine(t *testing.T) {
	opts := InstallOptions{GOOS: "plan9"}
	assert.Equal(t, "plan9", opts.env().GOOS())
	assert.False(t, opts.piping())

	var out, errs bytes.Buffer
	opts = InstallOptions{Out: &out, Err: &errs, Pipe: "sh"}
	assert.True(t, opts.piping())
	assert.Same(t, &out, opts.pipeOut(), "a terminal reads the command's output beside the report")
	opts.JSON = true
	assert.Same(t, &errs, opts.pipeOut(), "a stream whose every line is an event does not")
	opts.Err = nil
	assert.Same(t, &out, opts.pipeOut(), "and with nowhere else to send it, it goes where the report does")
}

// TestInstallFromAPrivateRepositoryUsesTheAssetEndpoint: the token that read
// the listing has to reach the download too, or the install ends with the
// sign-in page written to disk under the tool's name. The fixture serves the
// bytes only from the API endpoint and only to a bearer request, so a file
// holding the release is proof of where it came from.
func TestInstallFromAPrivateRepositoryUsesTheAssetEndpoint(t *testing.T) {
	src, seen := toolRepo(t, "sesame", "v1.4.0", "tool-linux-amd64")
	opts, _ := instOpts(t, src)

	_, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, toolBody, readFile(t, filepath.Join(opts.BinDir, "tool")))
	assert.Equal(t, 1, seen.api, "the asset came from its API endpoint")
	assert.Zero(t, seen.public, "and the public URL was never asked")
}

// TestInstallFromAPrivateRepositoryPipesWhatItDownloaded: the pipe path builds
// its own installer, so it needs the token carried to it separately from the
// path that installs a binary.
func TestInstallFromAPrivateRepositoryPipesWhatItDownloaded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the pipe command is a shell one")
	}
	src, seen := toolRepo(t, "sesame", "v1.4.0", "tool-linux-amd64")
	opts, _ := instOpts(t, src)
	opts.Pipe = "cat"

	_, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, 1, seen.api)
	assert.Zero(t, seen.public)
}

// TestInstallFromAPublicRepositoryStaysOnThePublicURL: the fence around the
// change. A listing that names an API endpoint, which every real listing does,
// still downloads from the browser URL when there is no token to authenticate
// with.
func TestInstallFromAPublicRepositoryStaysOnThePublicURL(t *testing.T) {
	src, seen := toolRepo(t, "", "v1.4.0", "tool-linux-amd64")
	opts, _ := instOpts(t, src)

	_, err := Install(context.Background(), *opts)
	require.NoError(t, err)
	assert.Equal(t, toolBody, readFile(t, filepath.Join(opts.BinDir, "tool")))
	assert.Equal(t, 1, seen.public)
	assert.Zero(t, seen.api, "no token, no reason to touch the endpoint that wants one")
}

// emptyEnv is a machine with no home folder and no writable system folder,
// which is the one case the install location cannot be guessed for.
type emptyEnv struct{}

func (emptyEnv) Getenv(string) string { return "" }
func (emptyEnv) Writable(string) bool { return false }
func (emptyEnv) GOOS() string         { return "linux" }

// noDigestSource is a release that publishes no checksum, as GitHub Enterprise
// versions before asset digests existed do.
func noDigestSource(t *testing.T, tag string, assets ...string) selfupdate.Source {
	t.Helper()
	list := []map[string]any{{"tag_name": tag, "draft": false, "prerelease": false,
		"assets": assetList("http://example.invalid", assets)}}
	for _, a := range list[0]["assets"].([]map[string]any) {
		delete(a, "digest")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	t.Cleanup(srv.Close)
	return selfupdate.Source{APIURL: srv.URL, Owner: "acme", Repo: "tool",
		TagPrefix: "v", Command: install.Command}
}

// writeDigestFile writes the file the fixture's published digest describes,
// which is what "already installed" is asserted against.
func writeDigestFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(toolBody), 0o755))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

// jsonEvent picks one event out of a JSON run by its message.
func jsonEvent(t *testing.T, stream, message string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event["message"] == message {
			return event
		}
	}
	t.Fatalf("no %q event in:\n%s", message, stream)
	return nil
}
