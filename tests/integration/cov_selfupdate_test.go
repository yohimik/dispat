// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for self-update: the response and filesystem shapes the
// shared fixture in selfupdate_test.go never produces. A listing spread over
// pages, a next page addressed to another host, an asset endpoint that reads
// the listing but refuses the bytes, release notes written to move a terminal
// around rather than to be read, the `--version` line that states the check's
// answer either way, and a folder the running binary may not be replaced in.
//
// Every one of them is driven through the compiled binary against a release
// API written for the scenario, because the answers being asserted are what
// the binary does with a response, not what a parser returns.

import (
	"bytes"
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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covSUAPI is a releases API a scenario writes itself. The shared fake in
// selfupdate_test.go answers one shape well; these scenarios are about the
// other shapes, so the handler is the scenario's and this type only carries
// what every handler needs: the address the server ended up on, which the URLs
// inside a release have to name, and the requests it answered.
type covSUAPI struct {
	base string

	mu   sync.Mutex
	hits []string
}

// requests is the paths the fake has answered so far, query strings included,
// which is how a test sees that a Link header was followed.
func (a *covSUAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.hits...)
}

// covSUServe stands up the fake over plain HTTP and returns it with base
// filled in, so a handler may build absolute URLs for its own server.
func covSUServe(t *testing.T, handle func(a *covSUAPI, w http.ResponseWriter, req *http.Request)) *covSUAPI {
	t.Helper()
	api := &covSUAPI{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.mu.Lock()
		api.hits = append(api.hits, req.URL.RequestURI())
		api.mu.Unlock()
		handle(api, w, req)
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	api.base = "http://" + srv.Listener.Addr().String()
	return api
}

// covSUReleaseJSON renders one release exactly as the API describes one: both
// asset addresses, the published size and the digest of the bytes the fake
// will actually serve.
func covSUReleaseJSON(base, version string, payload []byte) map[string]any {
	sum := sha256.Sum256(payload)
	return map[string]any{
		"tag_name":   "services/dispat/v" + version,
		"draft":      false,
		"prerelease": strings.Contains(version, "-"),
		"body":       suBody,
		"html_url":   base + "/o/r/releases/tag/services%2Fdispat%2Fv" + version,
		"assets": []any{map[string]any{
			"name":                 assetName(),
			"size":                 len(payload),
			"browser_download_url": base + "/dl/" + version,
			"url":                  base + "/assets/" + version,
			"digest":               "sha256:" + hex.EncodeToString(sum[:]),
		}},
	}
}

// covSUForeignRelease is another module's release: what a monorepo listing is
// mostly made of, and what a page carrying nothing for dispat looks like.
var covSUForeignRelease = map[string]any{
	"tag_name": "pkg/ccme/v9.9.9", "draft": false, "prerelease": false, "assets": []any{},
}

// covSUPayload is the bytes a release of the given version hands out.
func covSUPayload(t *testing.T, version string) []byte {
	t.Helper()
	data, err := os.ReadFile(harness.BuildVersioned(t, version))
	require.NoError(t, err)
	return data
}

// covSUExe copies a version-stamped binary somewhere a self-update may
// replace it, which is the fixture of every scenario here: the suite's shared
// build must never be the file under test.
func covSUExe(t *testing.T, version string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "dispat"+exeSuffix())
	copyFile(t, harness.BuildVersioned(t, version), exe)
	return exe
}

// covVersionOf asks a binary which version it is.
func covVersionOf(t *testing.T, r *harness.Repo, path string) string {
	t.Helper()
	res := r.CommandBin(path, "--version")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, line := range strings.Split(res.Stdout, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "dispat "); ok {
			v, _, _ := strings.Cut(rest, " (")
			return v
		}
	}
	t.Fatalf("no version line in:\n%s", res.Stdout)
	return ""
}

// TestSelfUpdateVersionReportsTheCheckOutcome: `--version` is the one
// invocation that states the check's answer either way. Behind, it is the
// ordinary notice; current, it says so, which is what makes `dispat --version`
// an answer to "am I up to date" rather than only to "what am I running".
func TestSelfUpdateVersionReportsTheCheckOutcome(t *testing.T) {
	r := newSURepo(t)
	on := []string{"DISPAT_UPDATE_CHECK=1"}
	args := []string{"--version", "--api-url", r.api, "--owner", "o", "--repo", "r"}

	res := r.CommandBinEnv(r.exe, on, args...)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "dispat "+suOld, "the version line is still the point")
	assert.Contains(t, res.Stdout, "a newer stable release is available: "+suNew)

	// The same command on the release the fake publishes: nothing to install,
	// and the line says that rather than saying nothing.
	current := covSUExe(t, suNew)
	res = r.CommandBinEnv(current, on, args...)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "this is the latest stable release")
	assert.NotContains(t, res.Stdout, "a newer stable release is available")
}

// TestSelfUpdateWalksAPaginatedListing: a repository with more releases than
// one page holds still answers "which version is current", and the Link header
// is how the walk continues. The next page's address is the server's own text,
// so one that leaves the configured host ends the listing instead of carrying
// the operator's token somewhere nobody configured.
func TestSelfUpdateWalksAPaginatedListing(t *testing.T) {
	payload := covSUPayload(t, suNew)

	t.Run("the release on the second page is found", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Query().Get("page") == "2" {
				_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, payload)})
				return
			}
			w.Header().Set("Link", `<`+a.base+`/repos/o/r/releases?per_page=100&page=2>; rel="prev", `+
				`<`+a.base+`/repos/o/r/releases?per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]any{covSUForeignRelease})
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "available dispat "+suNew)
		assert.Contains(t, strings.Join(api.requests(), "\n"), "page=2",
			"the Link header is what reached the page the release is on")
	})

	t.Run("a next page on another host ends the listing", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", `<http://127.0.0.1:1/repos/o/r/releases?per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]any{covSUForeignRelease})
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		out := res.Stdout + res.Stderr
		assert.Contains(t, out, "leaves the configured API host")
		assert.Contains(t, out, "no matching release")
		for _, path := range api.requests() {
			assert.NotContains(t, path, "page=2", "the foreign page was never requested")
		}
	})
}

// TestSelfUpdateFallsBackToThePublicDownloadURL: a credential that reads the
// listing and not the assets is a real shape — a fine-grained token, a proxy
// in front of the API — and it used to be an install that simply worked. The
// endpoint's refusal is said out loud, the public address is tried once with
// no credential, and the staged file starts empty again so the refusal's own
// body cannot end up in the installed binary.
func TestSelfUpdateFallsBackToThePublicDownloadURL(t *testing.T) {
	payload := covSUPayload(t, suNew)
	const refusal = `{"message":"Resource not accessible by personal access token"}`
	api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, refusal)
		case strings.HasPrefix(req.URL.Path, "/dl/"):
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			_, _ = w.Write(payload)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, payload)})
		}
	})
	r := harness.New(t)
	exe := covSUExe(t, suOld)

	res := r.CommandBinEnv(exe, []string{"GITHUB_TOKEN=sesame"},
		"self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+suNew)
	assert.Contains(t, res.Stdout+res.Stderr, "trying the public download URL")
	assert.Equal(t, suNew, covVersionOf(t, r, exe), "the bytes that landed are the release's")

	paths := strings.Join(api.requests(), "\n")
	assert.Contains(t, paths, "/assets/", "the endpoint was tried first")
	assert.Contains(t, paths, "/dl/", "and the public address second")
}

// covHostileNotes is a release body written to act on a terminal rather than
// to be read by one: colour and title sequences, sequences that never finish,
// bare control bytes, a line far past what one line of notes may be, and more
// bullets than a summary prints. Whoever publishes a release writes the body,
// so this is less an attacker than a stray sequence, and the answer is the
// same either way — none of it reaches the terminal as itself.
var covHostileNotes = "### \x1b[1mFeatures\x1b[0m\n\n" +
	"- a bullet that sets the window title \x1b]0;titled\x07 and carries on\n" +
	"- a bullet ending on a bare escape \x1b\n" +
	"- a bullet whose colour sequence never ends \x1b[38;2;255\n" +
	"- a bullet whose operating system command never ends \x1b]8;;http://never.printed\n" +
	"- a bullet with a two byte sequence \x1bN inside it\n" +
	"- \x07\x7fa bullet opening on a bell and a delete\n" +
	"- " + strings.Repeat("длинная строка про изменение ", 20) + "\n" +
	"\n### Fixes\n\n" + strings.Repeat("- one more fix nobody has room for\n", 60) +
	"\n---\n\n**Install this version:**\n\n```sh\ncurl -fsSL https://never.printed/install.sh | sh\n```\n"

// TestSelfUpdateNotesAreSafeToPrint: the notes are read out of somebody else's
// markdown, so what a terminal would act on is taken out rather than printed —
// whole sequences, not just the escape that opens them, because the tail of one
// is visible debris. An overlong line is cut on a rune boundary and a body with
// more in it than a summary holds says so and points at the changelog.
func TestSelfUpdateNotesAreSafeToPrint(t *testing.T) {
	r := newSURepo(t)
	r.body = covHostileNotes
	r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})

	res := r.update("--check")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "what changed in "+suNew)
	assert.Contains(t, res.Stdout, "Features", "the heading survives its colouring")
	assert.Contains(t, res.Stdout, "... the notes go on",
		"a body past the summary's bounds points at the changelog instead")

	for _, debris := range []string{"[1m", "[0m", "]0;", "titled", "38;2;255", "never.printed", "\x1b"} {
		assert.NotContains(t, res.Stdout, debris, "a terminal must not be handed %q", debris)
	}
	assert.Contains(t, res.Stdout, "a bullet with a two byte sequence",
		"a two byte sequence takes only itself")
	assert.Contains(t, res.Stdout, "a bullet opening on a bell and a delete",
		"bare control bytes are dropped and the text stays")
	assert.Contains(t, res.Stdout, " ...", "the overlong line is cut rather than printed whole")
	assert.NotContains(t, res.Stdout, "curl -fsSL", "and the footer is still not notes")
	assert.Equal(t, suOld, covVersionOf(t, r.Repo, r.exe), "--check still installs nothing")
}

// TestSelfUpdateRollbackNeedsAFolderItCanWriteTo: a rollback is three renames
// in the binary's own folder, so a folder that will not take a file is refused
// before anything moves, naming what the reader has to change. The binary that
// was running is still the one that runs.
func TestSelfUpdateRollbackNeedsAFolderItCanWriteTo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions do not gate a rename on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a folder whatever its mode says")
	}
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code, "the update that leaves a backup to roll back to")
	require.Equal(t, suNew, covVersionOf(t, r.Repo, r.exe))

	dir := filepath.Dir(r.exe)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	res := r.CommandBin(r.exe, "self-update", "--rollback")
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "rights to replace")
	assert.Equal(t, suNew, covVersionOf(t, r.Repo, r.exe), "and nothing was rotated")
}

// TestSelfUpdateReadsOnlyTheReleasesItCanInstall: the listing of a monorepo
// carries other modules' releases, drafts nobody published and tags that are
// not versions at all, and each is passed over for its own reason rather than
// failing the check. Past a point a listing stops being an answer to "which
// version is current" and is refused by size, before it is parsed.
func TestSelfUpdateReadsOnlyTheReleasesItCanInstall(t *testing.T) {
	payload := covSUPayload(t, suNew)

	t.Run("a draft and a tag with no version in it are passed over", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{
				// Higher than anything published, and a draft: a release
				// nobody can install yet.
				map[string]any{"tag_name": "services/dispat/v9.9.9", "draft": true,
					"prerelease": false, "assets": []any{}},
				// dispat's own prefix over something that is not a version.
				map[string]any{"tag_name": "services/dispat/vnightly", "draft": false,
					"prerelease": false, "assets": []any{}},
				covSUForeignRelease,
				covSUReleaseJSON(a.base, suNew, payload),
			})
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--log-level", "debug",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "available dispat "+suNew,
			"the draft is not a release this can install")
		assert.NotContains(t, res.Stdout, "9.9.9")
		assert.Contains(t, res.Stdout, "tag carries no version",
			"and the tag that is not a version says why it was passed over")
	})

	t.Run("a listing past the cap is refused by size", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// A well-formed listing whose one release carries a body no
			// release has: read far enough to know it is over the bound, and
			// no further.
			_, _ = w.Write([]byte(`[{"tag_name":"services/dispat/v1.1.0","draft":false,"body":"`))
			_, _ = w.Write(bytes.Repeat([]byte("x"), 9<<20))
			_, _ = w.Write([]byte(`","assets":[]}]`))
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "larger than",
			"the bound is named rather than the parse failing further down")
	})
}

// TestSelfUpdateRefusesABinaryThatIsNotTheRelease: a file that downloaded
// intact can still be the wrong thing entirely, and finding that out after the
// swap means finding it out with no dispat left. So the candidate is run
// before it is trusted with the running one's place: a file that is not a
// program at all, and a program answering with another version, are both
// refused with the working binary untouched and nothing kept beside it.
func TestSelfUpdateRefusesABinaryThatIsNotTheRelease(t *testing.T) {
	serve := func(t *testing.T, payload []byte) *covSUAPI {
		t.Helper()
		return covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, "/dl/") {
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				_, _ = w.Write(payload)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, payload)})
		})
	}

	for name, tc := range map[string]struct {
		payload func(t *testing.T) []byte
		want    string
	}{
		"a file that is not a program": {
			payload: func(t *testing.T) []byte { return []byte("this release shipped a README by mistake\n") },
			want:    "does not run",
		},
		"a program answering with another version": {
			payload: func(t *testing.T) []byte { return covSUPayload(t, suOld) },
			want:    "reports a different version",
		},
	} {
		t.Run(name, func(t *testing.T) {
			payload := tc.payload(t)
			api := serve(t, payload)
			r := harness.New(t)
			exe := covSUExe(t, suOld)

			res := r.CommandBin(exe, "self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
			assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			assert.Equal(t, suOld, covVersionOf(t, r, exe), "the working binary is where it was")
			assert.NoFileExists(t, backupPath(exe), "and nothing was kept beside it")

			entries, err := os.ReadDir(filepath.Dir(exe))
			require.NoError(t, err)
			assert.Len(t, entries, 1, "the staged download was removed: %v", entries)
		})
	}
}
