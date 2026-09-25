package selfupdate

// install.sh actually run, against a fake releases API.
//
// The rest of install_script_test.go reads the script as text, which is enough
// for the contracts it shares with this package (the asset name, the tag
// prefix). What the authenticated download has to get right cannot be read
// off the source: whether the credential reaches the asset endpoint, whether
// it is kept away from the object storage the endpoint redirects to, and
// whether an unauthenticated run still goes to the public URL. So the script
// is executed, once per downloader, with the fake standing in for GitHub.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptVersion is the release the scenarios install. Naming it explicitly
// means the fake never has to answer the listing walk.
const scriptVersion = "1.2.3"

// scriptFixture is a release published three times over, the way GitHub does
// it: the API that describes it, the asset endpoint that serves it to an
// authenticated request, the object storage that endpoint redirects to, and
// the public download host.
//
// The storage host answers under "localhost" while the API answers under
// "127.0.0.1". They are the same interface, and deliberately not the same
// name: a downloader decides whether a redirect may carry the Authorization
// header by comparing host names, so two spellings of loopback are what makes
// a cross-host redirect expressible without a resolver.
type scriptFixture struct {
	api, storage, public *httptest.Server
	storageURL           string
	body                 []byte
	token                string
	// refuseAsset makes the API endpoint answer 403 to an authenticated
	// request, which is what a token that reads the listing and not the
	// assets gets.
	refuseAsset bool
	// refusal answers the release lookups in place of the release, the way
	// GitHub refuses a request it will not serve.
	refusal *scriptRefusal

	mu          sync.Mutex
	lookups     int
	apiHits     int
	storageHits int
	publicHits  int
	storageAuth string
}

// scriptRefusal is one refusal of the releases API: its status, the headers
// that say whether and when to come back, and GitHub's message. It answers
// the first times lookups, or every lookup when times is 0.
type scriptRefusal struct {
	status  int
	headers map[string]string
	message string
	times   int
}

// signInPage is what github.com serves at the public download URL of a private
// repository's asset: a page, under a 200, that is not the file.
const signInPage = "<!DOCTYPE html><html><body>Sign in to GitHub</body></html>"

// newScriptFixture publishes one release. An empty token is a public
// repository, where every address answers to everyone; a token makes it
// private, and then the API and the asset endpoint demand that credential and
// the public host answers with the sign-in page.
func newScriptFixture(t *testing.T, token string) *scriptFixture {
	t.Helper()
	f := &scriptFixture{
		body:  []byte("#!/bin/sh\necho \"dispat " + scriptVersion + " (test)\"\n"),
		token: token,
	}

	f.storage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		f.storageHits++
		f.storageAuth = req.Header.Get("Authorization")
		f.mu.Unlock()
		w.Header().Set("Content-Length", fmt.Sprint(len(f.body)))
		_, _ = w.Write(f.body)
	}))
	t.Cleanup(f.storage.Close)
	f.storageURL = atHost(t, f.storage.URL, "localhost") + "/objects/1"

	f.public = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.publicHits++
		private := f.token != ""
		f.mu.Unlock()
		if private {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, signInPage)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(f.body)))
		_, _ = w.Write(f.body)
	}))
	t.Cleanup(f.public.Close)

	var apiBase string
	f.api = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authed := f.token == "" || req.Header.Get("Authorization") == "Bearer "+f.token
		if req.URL.Path == "/assets/1" {
			f.mu.Lock()
			f.apiHits++
			refuseAsset := f.refuseAsset
			f.mu.Unlock()
			if !authed || req.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			if refuseAsset {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
				return
			}
			http.Redirect(w, req, f.storageURL, http.StatusFound)
			return
		}
		f.mu.Lock()
		f.lookups++
		lookup, refusal := f.lookups, f.refusal
		f.mu.Unlock()
		if refusal != nil && (refusal.times == 0 || lookup <= refusal.times) {
			for name, value := range refusal.headers {
				w.Header().Set(name, value)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(refusal.status)
			fmt.Fprintf(w, `{"message":%q,"documentation_url":"https://docs.github.com/rest"}`, refusal.message)
			return
		}
		if !authed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, f.releaseJSON(apiBase))
	}))
	apiBase = "http://" + f.api.Listener.Addr().String()
	f.api.Start()
	t.Cleanup(f.api.Close)
	// Written out by hand for the sake of the key order, so it is checked
	// rather than assumed to still be a document.
	require.True(t, json.Valid([]byte(f.releaseJSON(apiBase))), "the fixture is not valid JSON")
	return f
}

// releaseJSON is the by-tag response, written out rather than encoded from a
// map, because the order of the keys is what the script's two walks depend on
// and a Go map would sort them: an asset's "url" comes before its "name", and
// its "digest" after, which is the order GitHub sends. The release and the
// uploader carry a "url" of their own, as they do in the real answer, so a
// walk that took any url rather than the asset's would be caught here, and
// the upload_url template is left in because its braces are exactly the kind
// of punctuation the field splitter has to survive.
//
// The release's own "name" is the asset's, which is what a release titled
// after its binary looks like and is the collision both walks have to survive:
// before them stands a "url" belonging to the release and another belonging to
// its author, and a walk that started reading at the top would take one of
// those for the asset's endpoint.
func (f *scriptFixture) releaseJSON(base string) string {
	sum := sha256.Sum256(f.body)
	return `{
  "url": "` + base + `/releases/9",
  "assets_url": "` + base + `/releases/9/assets",
  "upload_url": "` + base + `/releases/9/assets{?name,label}",
  "html_url": "` + base + `/o/r/releases/tag/x",
  "id": 9,
  "author": {"login": "a", "id": 1, "url": "` + base + `/users/a", "html_url": "` + base + `/a"},
  "node_id": "RE_9",
  "tag_name": "` + DefaultTagPrefix + scriptVersion + `",
  "name": "` + AssetName(runtime.GOOS, runtime.GOARCH) + `",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "url": "` + base + `/assets/1",
      "id": 1,
      "node_id": "RA_1",
      "name": "` + AssetName(runtime.GOOS, runtime.GOARCH) + `",
      "label": null,
      "uploader": {"login": "u", "id": 2, "url": "` + base + `/users/u"},
      "content_type": "application/octet-stream",
      "state": "uploaded",
      "size": ` + fmt.Sprint(len(f.body)) + `,
      "digest": "sha256:` + hex.EncodeToString(sum[:]) + `",
      "download_count": 0,
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-01-01T00:00:00Z",
      "browser_download_url": "` + f.public.URL + `/yohimik/dispat/releases/download/` +
		DefaultTagPrefix + scriptVersion + `/` + AssetName(runtime.GOOS, runtime.GOARCH) + `"
    }
  ]
}`
}

func (f *scriptFixture) refuse(refusal *scriptRefusal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusal = refusal
}

func (f *scriptFixture) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
}

func (f *scriptFixture) counts() (api, storage, public int, storageAuth string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.apiHits, f.storageHits, f.publicHits, f.storageAuth
}

// run executes install.sh against the fixture with the given extra arguments,
// and answers what it wrote and where the binary landed.
func (f *scriptFixture) run(t *testing.T, downloader string, args ...string) (string, int, string) {
	t.Helper()
	binDir := t.TempDir()
	full := append([]string{
		filepath.Join(repoRoot(t), "install.sh"),
		"--version", scriptVersion, "--bin-dir", binDir,
		"--os", runtime.GOOS, "--arch", runtime.GOARCH,
	}, args...)
	cmd := exec.Command("/bin/sh", full...)
	cmd.Env = []string{
		"PATH=" + scriptPath(t, downloader),
		"HOME=" + t.TempDir(),
		"DISPAT_API_URL=" + f.api.URL,
		"DISPAT_DOWNLOAD_URL=" + f.public.URL,
	}
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		require.ErrorAs(t, err, &exitErr, "launching install.sh")
		code = exitErr.ExitCode()
	}
	return string(out), code, filepath.Join(binDir, "dispat")
}

// scriptPath builds a PATH holding exactly one downloader, because install.sh
// prefers curl wherever both are installed and each branch has to be driven
// on its own. Everything else the script reaches for is symlinked in beside
// it; the two checksum tools are optional, since the script only needs one of
// them and says so when it has neither.
func scriptPath(t *testing.T, downloader string) string {
	t.Helper()
	if _, err := exec.LookPath(downloader); err != nil {
		t.Skipf("%s is not installed", downloader)
	}
	dir := t.TempDir()
	for _, name := range []string{
		downloader, "uname", "tr", "sed", "awk", "sort", "tail", "cut",
		"chmod", "mv", "rm", "mkdir", "grep", "cat", "sha256sum", "shasum",
		"mktemp", "date", "sleep",
	} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		require.NoError(t, os.Symlink(path, filepath.Join(dir, name)))
	}
	return dir
}

// TestInstallScriptReachesAPrivateRepository: the script's half of the
// feature, run rather than read. The fake serves the asset only from its API
// endpoint and only to a bearer request, and answers the public URL with a
// sign-in page, so a binary that lands and runs is proof the credential went
// where it had to. It is also proof it went no further: object storage
// answers under a different host name and records what it was sent.
func TestInstallScriptReachesAPrivateRepository(t *testing.T) {
	requireExec(t)
	for _, downloader := range []string{"curl", "wget"} {
		t.Run(downloader, func(t *testing.T) {
			f := newScriptFixture(t, "sesame")

			// Without the credential nothing is readable, and the refusal
			// arrives before any transfer.
			out, code, _ := f.run(t, downloader)
			assert.NotEqual(t, 0, code, "output:\n%s", out)
			api, storage, public, _ := f.counts()
			assert.Zero(t, api)
			assert.Zero(t, storage)
			assert.Zero(t, public)

			out, code, target := f.run(t, downloader, "--token", "sesame")
			require.Equal(t, 0, code, "output:\n%s", out)
			assert.Contains(t, out, "from the release API")
			assert.Contains(t, out, "checksum verified")
			installed, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, string(f.body), string(installed), "the release itself is on PATH")

			api, storage, public, storageAuth := f.counts()
			assert.Equal(t, 1, api, "the asset was asked for at its API endpoint")
			assert.Equal(t, 1, storage, "and the redirect to object storage was followed")
			assert.Zero(t, public, "the public URL, which serves a page, was never asked")
			assert.Empty(t, storageAuth, "object storage is another host and never sees the credential")
		})
	}
}

// TestInstallScriptWithoutATokenStaysOnThePublicURL: the fence around the
// change. Every release names an asset endpoint, as every real one does, and
// an unauthenticated run still downloads from the public host and never
// touches the endpoint that wants a credential.
func TestInstallScriptWithoutATokenStaysOnThePublicURL(t *testing.T) {
	requireExec(t)
	for _, downloader := range []string{"curl", "wget"} {
		t.Run(downloader, func(t *testing.T) {
			f := newScriptFixture(t, "")

			out, code, target := f.run(t, downloader)
			require.Equal(t, 0, code, "output:\n%s", out)
			assert.NotContains(t, out, "from the release API")
			installed, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, string(f.body), string(installed))

			api, storage, public, _ := f.counts()
			assert.Equal(t, 1, public)
			assert.Zero(t, api, "no token, no reason to touch the endpoint that wants one")
			assert.Zero(t, storage)
		})
	}
}

// TestInstallScriptFallsBackToThePublicURL: an endpoint that refuses is tried
// once more on the public URL with no credential, exactly as dispat's own
// downloader does it. A token that can read a repository's listing and not its
// assets is a real shape, and before the endpoint existed that install simply
// worked.
func TestInstallScriptFallsBackToThePublicURL(t *testing.T) {
	requireExec(t)
	for _, downloader := range []string{"curl", "wget"} {
		t.Run(downloader, func(t *testing.T) {
			// A public repository's fixture, so the public URL serves the
			// binary, with the endpoint refusing an authenticated request.
			f := newScriptFixture(t, "")
			f.mu.Lock()
			f.refuseAsset = true
			f.mu.Unlock()

			out, code, target := f.run(t, downloader, "--token", "sesame")
			require.Equal(t, 0, code, "output:\n%s", out)
			assert.Contains(t, out, "trying the public download URL")
			assert.Contains(t, out, "checksum verified")
			installed, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, string(f.body), string(installed))

			api, storage, public, _ := f.counts()
			assert.Equal(t, 1, api, "the endpoint was asked first")
			assert.Zero(t, storage)
			assert.Equal(t, 1, public, "and the public URL answered after it")
		})
	}
}

// requireErrorHeaders skips a scenario that reads the headers of a refusal
// through a downloader that cannot show them: busybox's wget prints only the
// status line of an error answer, which install.sh then reports as it is.
func requireErrorHeaders(t *testing.T, downloader string) {
	t.Helper()
	if downloader != "wget" {
		return
	}
	help, _ := exec.Command("wget", "--help").CombinedOutput()
	if strings.Contains(string(help), "BusyBox") {
		t.Skip("busybox wget prints no headers of an error answer")
	}
}

// TestInstallScriptWaitsOutARateLimit: an anonymous lookup shares the
// address's hourly quota with every other anonymous caller there, which is
// what the image builds meet on a busy runner. A refusal that says the limit
// is spent, and when it resets, is waited out and asked again instead of
// being read as a release that does not exist.
func TestInstallScriptWaitsOutARateLimit(t *testing.T) {
	requireExec(t)
	for _, downloader := range []string{"curl", "wget"} {
		t.Run(downloader, func(t *testing.T) {
			requireErrorHeaders(t, downloader)
			f := newScriptFixture(t, "")
			f.refuse(&scriptRefusal{
				status: http.StatusForbidden,
				headers: map[string]string{
					"X-RateLimit-Remaining": "0",
					"X-RateLimit-Reset":     strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10),
				},
				message: "API rate limit exceeded for 203.0.113.7.",
				times:   1,
			})

			out, code, target := f.run(t, downloader)
			require.Equal(t, 0, code, "output:\n%s", out)
			assert.Contains(t, out, "the GitHub API is rate limited (HTTP 403")
			assert.Contains(t, out, "retrying in 0s (attempt 2 of 3)",
				"a reset already past is no reason to wait")
			assert.Equal(t, 2, f.lookupCount(), "one refusal, then the release")
			installed, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, string(f.body), string(installed))
		})
	}
}

// TestInstallScriptGivesUpOnASpentRateLimit: three attempts in all, and the
// failure then says the limit is spent and how to get a larger one, rather
// than that the release does not exist.
func TestInstallScriptGivesUpOnASpentRateLimit(t *testing.T) {
	requireExec(t)
	for _, downloader := range []string{"curl", "wget"} {
		t.Run(downloader, func(t *testing.T) {
			requireErrorHeaders(t, downloader)
			f := newScriptFixture(t, "")
			f.refuse(&scriptRefusal{
				status:  http.StatusTooManyRequests,
				headers: map[string]string{"Retry-After": "0"},
				message: "You have exceeded a secondary rate limit.",
			})

			out, code, _ := f.run(t, downloader)
			assert.NotEqual(t, 0, code, "output:\n%s", out)
			assert.Equal(t, 3, f.lookupCount(), "three attempts in all")
			assert.Contains(t, out, "cannot read the release "+DefaultTagPrefix+scriptVersion+
				": the GitHub API rate limit is spent (HTTP 429")
			assert.Contains(t, out, "pass --token")
			assert.NotContains(t, out, "no release for")
			if downloader == "curl" {
				assert.Contains(t, out, "HTTP 429: You have exceeded a secondary rate limit.",
					"GitHub's own explanation is kept")
			}
			_, _, public, _ := f.counts()
			assert.Zero(t, public, "nothing is downloaded without the release")
		})
	}
}

// TestInstallScriptFailsARefusalAtOnce: a refusal that is no rate limit is
// asked once. Only a 404 means the release does not exist; any other status
// is reported as itself.
func TestInstallScriptFailsARefusalAtOnce(t *testing.T) {
	requireExec(t)
	for _, tc := range []struct {
		name    string
		refusal scriptRefusal
		want    string
	}{
		{
			name:    "missing",
			refusal: scriptRefusal{status: http.StatusNotFound, message: "Not Found"},
			want:    "no release for " + DefaultTagPrefix + scriptVersion + ". Check the version",
		},
		{
			name:    "forbidden",
			refusal: scriptRefusal{status: http.StatusForbidden, message: "Resource not accessible by integration"},
			want:    "cannot read the release " + DefaultTagPrefix + scriptVersion + ": HTTP 403",
		},
	} {
		for _, downloader := range []string{"curl", "wget"} {
			t.Run(tc.name+"/"+downloader, func(t *testing.T) {
				f := newScriptFixture(t, "")
				f.refuse(&tc.refusal)

				out, code, _ := f.run(t, downloader)
				assert.NotEqual(t, 0, code, "output:\n%s", out)
				assert.Contains(t, out, tc.want)
				assert.NotContains(t, out, "retrying")
				assert.Equal(t, 1, f.lookupCount(), "a refusal that is no rate limit is not asked again")
			})
		}
	}
}

// TestInstallScriptsAgreeOnTheAuthenticatedDownload: install.ps1 cannot be
// executed here, because the image the Go tests run in has no PowerShell, so
// the two scripts are compared as text instead. What has to hold on both
// sides is the shape of the authenticated download: the asset's own endpoint
// rather than the public URL, the octet-stream Accept that makes it serve
// bytes, the bearer credential, and a redirect that is never followed with
// the credential still attached.
func TestInstallScriptsAgreeOnTheAuthenticatedDownload(t *testing.T) {
	sh := readRepoFile(t, "install.sh")
	ps1 := readRepoFile(t, "install.ps1")

	for name, script := range map[string]string{"install.sh": sh, "install.ps1": ps1} {
		assert.Contains(t, script, "application/octet-stream",
			"%s must ask the asset endpoint for the file rather than for its metadata", name)
		assert.Contains(t, script, "Bearer", "%s must send the credential as a bearer token", name)
	}

	assert.Contains(t, sh, "ASSET_API_URL",
		"install.sh must read the asset's own url out of the release")
	assert.Contains(t, sh, `/^"url":"/`,
		"and it must read it from the url field, anchored against every other _url key")
	assert.Contains(t, sh, "--max-redirect=0",
		"wget forwards headers across redirects, so it must be given none to follow")

	assert.Contains(t, ps1, "$assetInfo.url",
		"install.ps1 must read the asset's own url out of the release")
	assert.Contains(t, ps1, "-MaximumRedirection 0",
		"Windows PowerShell forwards Authorization across redirects, so it must not follow one")

	// The unauthenticated path is the one nobody may change by accident: it
	// is what every public install runs, and it is also what an endpoint that
	// refuses falls back to.
	assert.Contains(t, sh, `download "$(public_asset_url)" "$1"`,
		"install.sh must still fetch the public URL with no headers when there is no token")
	assert.Contains(t, sh, `printf '%s' "${DOWNLOAD_URL}/${OWNER}/${REPO}/releases/download/${TAG}/${ASSET}"`,
		"and that URL is the release's own download address")
	assert.Contains(t, ps1, `Invoke-WebRequest -Uri $publicUrl -OutFile $destination`,
		"install.ps1 must still fetch the public URL with no headers when there is no token")
	assert.Contains(t, ps1, `$publicUrl = "$DownloadUrl/$Owner/$Repo/releases/download/$tag/$asset"`,
		"and that URL is the release's own download address")

	// Both fall back to it once when the endpoint refuses, which is what
	// keeps a token that reads the listing and not the assets installing.
	assert.Contains(t, sh, "trying the public download URL")
	assert.Contains(t, ps1, "trying the public download URL")

	// And both stop at the redirect rather than following it with the
	// credential still attached, each in the way its own downloader needs.
	assert.Contains(t, ps1, "-PassThru",
		"Windows PowerShell 5.1 returns the refused redirect rather than raising it, so it has to be asked for")
	assert.Contains(t, sh, "wget --max-redirect=0 --version",
		"the busybox probe is the option's own exit code, not the wording of its help text")
}

// TestInstallScriptsAgreeOnTheRateLimit: install.ps1 cannot be executed here
// either, so its half of the rate-limit rule is compared as text with the
// half of install.sh the scenarios above run. Both read the same two signals,
// wait at most a minute, stop after three attempts, and call a release
// missing only when the API answered 404.
func TestInstallScriptsAgreeOnTheRateLimit(t *testing.T) {
	sh := readRepoFile(t, "install.sh")
	ps1 := readRepoFile(t, "install.ps1")

	for _, want := range []string{"retry-after", "x-ratelimit-remaining", "x-ratelimit-reset"} {
		assert.Contains(t, strings.ToLower(sh), want, "install.sh must read %s", want)
		assert.Contains(t, strings.ToLower(ps1), want, "install.ps1 must read %s", want)
	}
	for name, script := range map[string]string{"install.sh": sh, "install.ps1": ps1} {
		assert.Contains(t, script, "retrying in", "%s must say it waits, and for how long", name)
		assert.Contains(t, script, "of 3)", "%s must stop after three attempts", name)
		assert.Contains(t, script, "the GitHub API rate limit is spent", "%s must name a spent limit as such", name)
	}

	assert.Contains(t, sh, `[ "$ATTEMPT" -lt 3 ] || return 1`)
	assert.Contains(t, sh, `[ "$WAIT" -le 60 ] || WAIT=60`, "install.sh caps a wait at a minute")
	assert.Contains(t, sh, `[ "$STATUS" != 404 ] || die "no release for ${TAG}.`,
		"install.sh calls a release missing only on a 404")

	assert.Contains(t, ps1, "$attempt -lt 3", "install.ps1 must stop after three attempts")
	assert.Contains(t, ps1, "[Math]::Min([int]$retryAfter, 60)", "install.ps1 caps a wait at a minute")
	assert.Contains(t, ps1, "Start-Sleep -Seconds $wait")
	assert.Contains(t, ps1, `if ($_.Exception.Data['Status'] -eq 404) { throw "no release for $tag.`,
		"install.ps1 calls a release missing only on a 404")
	assert.NotContains(t, ps1, `} catch {
    throw "no release for $tag.`, "install.ps1 must not turn every failure into a missing release")
}
