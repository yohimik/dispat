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
	"sync"
	"testing"

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

	mu          sync.Mutex
	apiHits     int
	storageHits int
	publicHits  int
	storageAuth string
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
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authed := f.token == "" || req.Header.Get("Authorization") == "Bearer "+f.token
		if req.URL.Path == "/assets/1" {
			f.mu.Lock()
			f.apiHits++
			f.mu.Unlock()
			if !authed || req.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			http.Redirect(w, req, f.storageURL, http.StatusFound)
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
	t.Cleanup(f.api.Close)
	apiBase = f.api.URL
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
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "url": "` + base + `/assets/1",
      "id": 1,
      "node_id": "RA_1",
      "name": "` + CurrentAssetName() + `",
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
		DefaultTagPrefix + scriptVersion + `/` + CurrentAssetName() + `"
    }
  ]
}`
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
	// is what every public install runs.
	assert.Contains(t, sh, `download "${DOWNLOAD_URL}/${OWNER}/${REPO}/releases/download/${TAG}/${ASSET}" "$1"`,
		"install.sh must still fetch the public URL with no headers when there is no token")
	assert.Contains(t, ps1, `Invoke-WebRequest -Uri "$DownloadUrl/$Owner/$Repo/releases/download/$tag/$asset" -OutFile $destination`,
		"install.ps1 must still fetch the public URL with no headers when there is no token")
}
