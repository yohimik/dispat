package integration

// Area 45: dispat installing somebody else's release binary, through the
// compiled binary and against a fake releases API. Everything here is real:
// a tool is published as a release asset, downloaded over HTTP, checked
// against what the release advertised, moved onto a folder that is on PATH,
// and then run to see which version answers.
//
// The self-update area covers the machinery this shares with it. What is
// pinned here is everything that could be assumed about dispat and cannot be
// assumed about a stranger's binary: which repository, which of its files,
// what the result is called, where it goes, and whether it is a binary at all.

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The two versions of the fictional tool the scenarios move between, and the
// asset that carries it. The name is deliberately not one dispat could have
// guessed: the whole point is that --asset says which file it is.
const (
	toolOld = "1.0.0"
	toolNew = "1.1.0"
)

// toolRepo is the fixture: a folder standing in for a folder on PATH, and a fake
// API publishing a tool for it.
//
// The fake answers on the server's own goroutines while the scenario driving
// it changes what it publishes, so everything a request reads is behind the
// mutex and every change goes through a method that takes it. Nothing here is
// decoration: without it the race detector fails the suite, which is exactly
// what it is for.
type toolRepo struct {
	*harness.Repo
	bin string // the install folder under test
	api string

	mu sync.Mutex
	// bodies maps a version to the bytes its asset serves, which is what lets
	// a scenario ask for a version and get that version.
	bodies map[string][]byte
	// assets maps a version to the file names its release attaches.
	assets map[string][]string
	// digests is whether a release publishes a checksum, which the GitHub
	// Enterprise versions predating asset digests do not.
	digests bool
	// tagPrefix is what the fake's tags carry before their version.
	tagPrefix string
	// token, when set, makes the repository private: the listing and the
	// asset's API endpoint answer only to that bearer credential, and the
	// public download URL answers with a sign-in page as github.com does.
	token string
	// hits records the paths the fake answered, in order, which is how a test
	// proves a repeated invocation paid for no transfer.
	hits []string
}

// toolScript is a tool: a shell script that says which version it is, which is
// what makes "did the right one land" answerable by running it.
func toolScript(version string) []byte {
	return []byte("#!/bin/sh\necho \"tool " + version + "\"\n")
}

func newToolRepo(t *testing.T) *toolRepo {
	t.Helper()
	r := &toolRepo{
		Repo: harness.New(t), bin: t.TempDir(), digests: true, tagPrefix: "v",
		bodies: map[string][]byte{toolOld: toolScript(toolOld), toolNew: toolScript(toolNew)},
		assets: map[string][]string{
			toolOld: {"tool-" + platform(), "checksums.txt"},
			toolNew: {"tool-" + platform(), "checksums.txt"},
		},
	}
	r.serve(t)
	return r
}

// publish adds a version to what the fake offers, with the files its release
// attaches.
func (r *toolRepo) publish(version string, assets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies[version] = toolScript(version)
	r.assets[version] = assets
}

// attach replaces the files one version's release carries, which is how a
// scenario puts whichever shape of release it is about in front of the
// command.
func (r *toolRepo) attach(version string, assets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assets[version] = assets
}

// withoutDigests stops the fake publishing a checksum, as the GitHub
// Enterprise versions predating asset digests do.
func (r *toolRepo) withoutDigests() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.digests = false
}

// tagsAs sets what the fake writes before the version in its tags.
func (r *toolRepo) tagsAs(prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tagPrefix = prefix
}

// requireToken makes the repository private, which is what a released tool
// nobody outside a company may download looks like.
func (r *toolRepo) requireToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.token = token
}

// platform is how the fixture spells this machine in an asset name, which is
// what --asset renders {os} and {arch} into.
func platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// serve stands up the fake API. Everything it answers is read under the lock,
// so a scenario may change what is published while the command is running.
func (r *toolRepo) serve(t *testing.T) {
	t.Helper()
	var base string
	// release renders one version. The caller holds the lock.
	release := func(version string) map[string]any {
		body := r.bodies[version]
		sum := sha256.Sum256(body)
		assets := make([]map[string]any, 0, len(r.assets[version]))
		for _, name := range r.assets[version] {
			// Both addresses, because every asset a real listing describes
			// carries both: the public browser URL and the asset's own REST
			// endpoint, which is the one that answers when a credential is
			// what makes the repository readable.
			asset := map[string]any{
				"name": name, "size": len(body),
				"browser_download_url": base + "/dl/" + version,
				"url":                  base + "/assets/" + version,
			}
			if r.digests {
				asset["digest"] = "sha256:" + hex.EncodeToString(sum[:])
			}
			assets = append(assets, asset)
		}
		return map[string]any{
			"tag_name": r.tagPrefix + version, "draft": false,
			"prerelease": strings.Contains(version, "-"),
			"html_url":   base + "/acme/tool/releases/tag/" + r.tagPrefix + version,
			"assets":     assets,
		}
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.hits = append(r.hits, req.URL.Path)
		authed := r.token == "" || req.Header.Get("Authorization") == "Bearer "+r.token
		if version, ok := strings.CutPrefix(req.URL.Path, "/dl/"); ok {
			body, known := r.bodies[version]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.token != "" {
				// What github.com serves at the public URL of a private
				// repository: a page, under a 200, that is not the asset.
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, privatePage)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
			return
		}
		if version, ok := strings.CutPrefix(req.URL.Path, "/assets/"); ok {
			body, known := r.bodies[version]
			if !known || !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			if req.Header.Get("Accept") != "application/octet-stream" {
				// Asking this endpoint for anything else answers with the
				// asset's metadata rather than with the file.
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"name":"metadata, not the asset"}`)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
			return
		}
		if !authed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, tag, ok := strings.Cut(req.URL.Path, "/releases/tags/"); ok {
			for version := range r.bodies {
				if r.tagPrefix+version == tag {
					_ = json.NewEncoder(w).Encode(release(version))
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		list := make([]map[string]any, 0, len(r.bodies))
		for version := range r.bodies {
			list = append(list, release(version))
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	t.Cleanup(srv.Close)
	r.api = base
}

// requests is the paths the fake has answered so far.
func (r *toolRepo) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hits...)
}

// install runs the command with the fake API and the fixture's folder wired
// in, naming the asset the way a reader would.
func (r *toolRepo) install(args ...string) harness.RunResult {
	r.T.Helper()
	return r.Command(append([]string{"install", "https://github.com/acme/tool",
		"--api-url", r.api, "--bin-dir", r.bin, "--asset", "tool-{os}-{arch}"}, args...)...)
}

// installWithToken is install with a credential in the environment and
// --token-env naming the variable it is in.
//
// Naming it is what a private repository behind an endpoint of its own takes:
// the conventional GITHUB_TOKEN is dropped whenever the endpoint was not
// itself set on purpose, so that a repository URL cannot make dispat hand
// somebody's github.com credential to a host they never named. See
// TestInstallKeepsTheTokenAwayFromAnotherHost.
func (r *toolRepo) installWithToken(token string, args ...string) harness.RunResult {
	r.T.Helper()
	return r.CommandEnv([]string{"DISPAT_TOKEN=" + token},
		append([]string{"install", "https://github.com/acme/tool",
			"--api-url", r.api, "--bin-dir", r.bin, "--asset", "tool-{os}-{arch}",
			"--token-env", "DISPAT_TOKEN"}, args...)...)
}

// bare runs the command with nothing but the API and the folder, for the
// scenarios that are about the flags this leaves out.
func (r *toolRepo) bare(args ...string) harness.RunResult {
	r.T.Helper()
	return r.Command(append([]string{"install", "https://github.com/acme/tool",
		"--api-url", r.api, "--bin-dir", r.bin}, args...)...)
}

// installed is the path the tool lands at, and version asks it which one it is.
func (r *toolRepo) installed() string { return filepath.Join(r.bin, "tool"+exeSuffix()) }

// version runs an installed tool and reads which one it is. It is a plain
// exec rather than the harness's own runner: the harness drives dispat and
// appends --root to what it launches, and the thing being run here is not
// dispat.
func (r *toolRepo) version(path string) string {
	r.T.Helper()
	out, err := exec.Command(path).CombinedOutput()
	require.NoError(r.T, err, "running %s:\n%s", path, out)
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "tool ")
	require.True(r.T, ok, "not a version line: %q", out)
	return rest
}

// TestInstallAToolFromAnotherRepository: the whole thing, over the
// process boundary. dispat reads somebody else's releases, fetches the file
// they named, checks it against what the release advertised, and puts it on
// PATH under the repository's own name, and the proof is that the file now
// runs and says which version it is.
func TestInstallAToolFromAnotherRepository(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	// --check first: it changes nothing and exits 1 because there is
	// something to install, which is what makes it a gate.
	res := r.install("--check")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assertOrderedIn(t, res.Stdout,
		"repository acme/tool",
		"release    "+toolNew,
		"asset      tool-"+platform(),
		"install to "+r.installed(),
		"install it with: dispat install",
	)
	assert.NoFileExists(t, r.installed(), "--check touches nothing")

	res = r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "downloading tool-"+platform())
	assert.Contains(t, res.Stdout, "installed tool "+toolNew+" at "+r.installed())
	assert.Equal(t, toolNew, r.version(r.installed()), "the file on PATH is the release")

	// Nothing was kept, because nothing was there: a first install has no
	// previous binary to talk about.
	assert.NotContains(t, res.Stdout, "the previous binary is at")
	assert.NoFileExists(t, backupPath(r.installed()))
}

// TestInstallIsIdempotent: a provisioning script runs the same line on every
// boot, and the second run must cost nothing. The destination is compared
// against the checksum the release published, so "already there" is a fact
// about the bytes rather than about a version string anybody wrote down.
func TestInstallIsIdempotent(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	require.Equal(t, 0, r.install().Code)
	before := r.requests()

	res := r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "is already v"+toolNew)
	assert.Contains(t, res.Stdout, "--force")
	assert.Equal(t, 0, countDownloads(r.requests())-countDownloads(before),
		"and the second run pays for no transfer")

	// --check agrees by exiting 0, which is the whole of the gate.
	assert.Equal(t, 0, r.install("--check").Code)

	// A destination somebody else overwrote is not this release, so the same
	// line installs again. This is what makes the command a repair.
	require.NoError(t, os.WriteFile(r.installed(), []byte("#!/bin/sh\necho tool 0.0.1\n"), 0o755))
	assert.Equal(t, 1, r.install("--check").Code, "different bytes are something to do")
	require.Equal(t, 0, r.install().Code)
	assert.Equal(t, toolNew, r.version(r.installed()))

	// --force installs it again anyway, and what it replaced is kept.
	res = r.install("--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "the previous binary is at "+backupPath(r.installed()))
	assert.Equal(t, toolNew, r.version(backupPath(r.installed())))
}

// TestInstallKeepsAndRestoresWhatItReplaced: the same safety property
// self-update is built around, for a tool dispat knows nothing about. It
// rotates, so a restore is itself reversible and nobody has to be sure before
// pressing it.
func TestInstallKeepsAndRestoresWhatItReplaced(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	require.Equal(t, 0, r.install("--release", toolOld).Code)
	require.Equal(t, toolOld, r.version(r.installed()))

	res := r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r.version(r.installed()))
	assert.Equal(t, toolOld, r.version(backupPath(r.installed())), "and the old one is beside it")
	assert.Contains(t, res.Stdout, `put it back with "dispat install acme/tool --rollback"`)

	// The gate first: it says there is something to restore and restores
	// nothing.
	res = r.bare("--rollback", "--check")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "the backup of tool is at "+backupPath(r.installed()))
	assert.Equal(t, toolNew, r.version(r.installed()))

	res = r.bare("--rollback")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "rolled back tool at "+r.installed())
	assert.Equal(t, toolOld, r.version(r.installed()))
	assert.Equal(t, toolNew, r.version(backupPath(r.installed())))

	require.Equal(t, 0, r.bare("--rollback").Code)
	assert.Equal(t, toolNew, r.version(r.installed()), "a second rollback returns")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "nothing is parked and forgotten between the renames: %v", entries)
}

// TestInstallBackupExpiresOnItsOwn: the copy is kept for a week and then
// removed by the next download of that same tool. Nothing has to be cleaned up
// by hand, and nothing else in the folder is ever touched.
func TestInstallBackupExpiresOnItsOwn(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	require.Equal(t, 0, r.install("--release", toolOld).Code)
	require.Equal(t, 0, r.install().Code)
	require.FileExists(t, backupPath(r.installed()))

	sixDays := time.Now().Add(-6 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(backupPath(r.installed()), sixDays, sixDays))
	require.Equal(t, 0, r.install("--check").Code)
	assert.FileExists(t, backupPath(r.installed()), "inside the week it stays")

	eightDays := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(backupPath(r.installed()), eightDays, eightDays))
	require.Equal(t, 0, r.install("--check").Code)
	assert.NoFileExists(t, backupPath(r.installed()), "past the week the next one clears it")
	assert.Equal(t, toolNew, r.version(r.installed()), "and only the backup is ever removed")

	res := r.bare("--rollback")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "--release", "the refusal says how to get an old version anyway")
}

// TestInstallNamesTheFileAndTheFolder: a project whose binary is not called
// after its repository, installed somewhere the reader chose. Both are flags
// because neither is derivable, and the folder is created rather than
// demanded.
func TestInstallNamesTheFileAndTheFolder(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	elsewhere := filepath.Join(t.TempDir(), "nested", "bin")

	res := r.Command("install", "acme/tool", "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--bin-dir", elsewhere, "--as", "gh")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	installed := filepath.Join(elsewhere, "gh"+exeSuffix())
	assert.FileExists(t, installed, "the folder is created on the way")
	assert.Equal(t, toolNew, r.version(installed))
	assert.Contains(t, res.Stdout, "installed gh "+toolNew)
	assert.Contains(t, res.Stdout, "is not on PATH",
		"a tool the shell cannot find is a successful install that looks like a failed one")

	// ...and a folder the shell already searches says nothing, because there
	// is nothing for the reader to do about it.
	res = r.CommandEnv([]string{"PATH=" + elsewhere + string(os.PathListSeparator) + os.Getenv("PATH")},
		"install", "acme/tool", "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--bin-dir", elsewhere, "--as", "gh", "--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "is not on PATH")
}

// TestInstallRefusesToGuessWhichFileIsTheBinary: a release carries a binary,
// a checksum file and often much more, and installing the wrong one globally
// is worse than any amount of typing. The refusal lists what is there.
func TestInstallRefusesToGuessWhichFileIsTheBinary(t *testing.T) {
	r := newToolRepo(t)

	res := r.bare("--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "--asset")
	assert.Contains(t, res.Stdout, "checksums.txt", "it says what it found")

	// A release carrying exactly one file has one answer, and asking for a
	// pattern to state the obvious would be ceremony.
	r.attach(toolNew, "tool-"+platform())
	res = r.bare("--check")
	assert.Equal(t, 1, res.Code, "still a gate: there is something to install")
	assert.Contains(t, res.Stdout, "asset      tool-"+platform())

	// A glob reaches a name nobody wants to type, and one reaching two files
	// is a pattern that has not chosen.
	r.attach(toolNew, "tool-"+platform(), "tool-"+platform()+".sha256")
	assert.Contains(t, r.bare("--asset", "tool-*", "--check").Stdout, "matches 2")

	res = r.bare("--asset", "*"+runtime.GOARCH, "--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "asset      tool-"+platform())

	// A release with nothing for this platform names what it does have.
	r.attach(toolNew, "tool-plan9-386")
	res = r.bare("--asset", "tool-{os}-{arch}", "--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "tool-"+platform(), "the one it wanted")
	assert.Contains(t, res.Stdout, "tool-plan9-386", "and the one there is")
}

// TestInstallRefusesWhatItCannotTrust: the checks stand between a download
// and a file that is about to be run, so a release whose checksum does not
// describe what arrives is refused with the folder untouched.
func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	r := newToolRepo(t)
	var base string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := toolScript(toolNew)
		if strings.HasPrefix(req.URL.Path, "/dl/") {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "v" + toolNew, "draft": false, "prerelease": false,
			"assets": []map[string]any{{
				"name": "tool-" + platform(), "size": len(body),
				"browser_download_url": base + "/dl/x",
				"digest":               "sha256:" + strings.Repeat("00", 32),
			}},
		}})
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	defer srv.Close()

	res := r.Command("install", "acme/tool", "--api-url", base, "--bin-dir", r.bin,
		"--asset", "tool-{os}-{arch}")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "hashes to")
	assert.Contains(t, res.Stdout, "install: ", "the failure names the command, not the package")
	assert.NoFileExists(t, r.installed(), "and nothing was installed")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Empty(t, entries, "the refused download is cleaned up: %v", entries)
}

// TestInstallRefusesAFolderItCannotWriteTo: /usr/local/bin belongs to root on
// most machines, so this is the first failure a real install meets. It has to
// arrive before the transfer rather than after it, and it has to say what to
// do about it.
func TestInstallRefusesAFolderItCannotWriteTo(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	r := newToolRepo(t)
	require.NoError(t, os.Chmod(r.bin, 0o500))
	t.Cleanup(func() { _ = os.Chmod(r.bin, 0o700) })

	before := countDownloads(r.requests())
	res := r.install()
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "not writable")
	assert.Contains(t, res.Stdout, "re-run with the rights")
	assert.Equal(t, before, countDownloads(r.requests()),
		"the refusal costs no transfer, which is the whole point of staging in the target folder")
	assert.NoFileExists(t, r.installed())
}

// TestInstallWithoutAPublishedChecksum: GitHub Enterprise versions before
// asset digests existed send none. That is a check that cannot be made, not a
// reason to refuse, and it is said out loud because it is also what makes the
// "already installed" question unanswerable.
func TestInstallWithoutAPublishedChecksum(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	r.withoutDigests()

	res := r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r.version(r.installed()))
	assert.Contains(t, res.Stdout, "no checksum")

	// And the same line installs again rather than claiming it is done: a
	// guess that skips the install is what leaves a machine on an old binary
	// forever.
	assert.Equal(t, 1, r.install("--check").Code)
}

// TestInstallReachesAnyPublishedVersion: --release names one exactly,
// downgrades included, and --prerelease is how a candidate line is opted into.
// Neither happens on its own.
func TestInstallReachesAnyPublishedVersion(t *testing.T) {
	requireShell(t)
	const candidate = "1.2.0-rc.1"
	r := newToolRepo(t)
	r.publish(candidate, "tool-"+platform())

	require.Equal(t, 0, r.install().Code)
	assert.Equal(t, toolNew, r.version(r.installed()), "the candidate is passed over")

	require.Equal(t, 0, r.install("--prerelease").Code)
	assert.Equal(t, candidate, r.version(r.installed()), "asked for, it is installed")

	require.Equal(t, 0, r.install("--release", "v"+toolOld).Code)
	assert.Equal(t, toolOld, r.version(r.installed()), "a named version is reached going backwards")

	res := r.install("--release", "9.9.9")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "v9.9.9")
	assert.Equal(t, toolOld, r.version(r.installed()), "a version nobody published changes nothing")
}

// TestInstallReadsTheRepositoryTagsHoweverTheyAreSpelled: a foreign
// repository tags its releases its own way, and the two spellings that cover
// nearly all of them are "v1.2.3" and "1.2.3". Neither is guessable, so
// --tag-prefix says which, and a prefix nothing carries is named in the
// refusal rather than reported as "you are up to date".
func TestInstallReadsTheRepositoryTagsHoweverTheyAreSpelled(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	r.tagsAs("")

	res := r.install("--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no matching release")
	assert.Contains(t, res.Stdout, "--tag-prefix")
	assert.Contains(t, res.Stdout, "--prerelease")

	res = r.install("--tag-prefix", "")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r.version(r.installed()))

	// A monorepo publishing a release per module is the other end of the
	// same idea, and the prefix is what filters the listing down to one.
	r2 := newToolRepo(t)
	r2.tagsAs("tool/v")
	res = r2.install("--tag-prefix", "tool/v")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r2.version(r2.installed()))
}

// TestInstallPipesAnAssetThatIsNotABinary: most releases ship an archive, and
// unpacking one is the job of the tool that unpacks archives. The verification
// is the same either way, so what reaches the command has been checked against
// the size and the checksum the release published, and nothing is ever
// renamed onto PATH by dispat itself.
func TestInstallPipesAnAssetThatIsNotABinary(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	res := r.install("--pipe", `cat > unpacked && chmod +x unpacked`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "piped tool-"+platform())
	assert.Equal(t, toolNew, r.version(filepath.Join(r.bin, "unpacked")),
		"the pipe runs in the install folder, so what it writes lands there")
	assert.NoFileExists(t, r.installed(), "and dispat installed nothing of its own")

	// The same file by path, for a command that has to seek, and the name the
	// release published, for one that switches on the archive layout.
	res = r.install("--pipe", `echo "name=$DISPAT_ASSET_NAME"; wc -c < "$DISPAT_ASSET"`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "name=tool-"+platform())
	assert.Contains(t, res.Stdout, fmt.Sprint(len(toolScript(toolNew))))

	// A pipe that fails is a failed install, and it leaves nothing behind.
	res = r.install("--pipe", "exit 3")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "the pipe failed")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only what the first pipe wrote: %v", entries)
}

// TestInstallPipeIsAlwaysSomethingToDo: a pipe has no destination file to
// compare against, so the question "is it already installed" has no answer and
// the gate says so rather than inventing one.
func TestInstallPipeIsAlwaysSomethingToDo(t *testing.T) {
	r := newToolRepo(t)
	res := r.install("--pipe", "cat > unpacked", "--check")
	assert.Equal(t, 1, res.Code)
	assertOrderedIn(t, res.Stdout, "asset      tool-"+platform(), "pipe       cat > unpacked", "in         "+r.bin)
	assert.NotContains(t, res.Stdout, "install to")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Empty(t, entries, "--check downloads nothing, pipe or not")
}

// TestInstallRefusesADestinationItMustNotReplace: /usr/local/bin is a shared
// folder, and a name that is already a directory there belongs to somebody.
// Installing over it would rename that directory aside to put a binary where
// it stood, which is a thing no download may do quietly.
func TestInstallRefusesADestinationItMustNotReplace(t *testing.T) {
	r := newToolRepo(t)
	require.NoError(t, os.Mkdir(r.installed(), 0o755))

	for _, args := range [][]string{nil, {"--force"}, {"--check"}} {
		res := r.install(args...)
		assert.Equal(t, 1, res.Code, "args: %v\nstdout:\n%s", args, res.Stdout)
		assert.Contains(t, res.Stdout, "is a folder")
	}
	assert.DirExists(t, r.installed(), "and it is still where it was")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing was staged beside it: %v", entries)

	// A pipe never touches the destination, so it never asks about it.
	res := r.install("--pipe", "cat > unpacked")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
}

// TestInstallPipeSeesTheAssetUnderItsOwnName: an unpacker switches on the
// suffix, and .tar.gz is two of them. The staged file therefore carries the
// name the release published rather than the temporary one it arrived under.
func TestInstallPipeSeesTheAssetUnderItsOwnName(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	r.attach(toolNew, "tool_1.1.0_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")

	res := r.bare("--asset", "tool_{version}_{os}_{arch}.tar.gz",
		"--pipe", `case "$DISPAT_ASSET" in *.tar.gz) echo "suffix kept";; *) echo "suffix lost: $DISPAT_ASSET";; esac`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "suffix kept")
	assert.Contains(t, res.Stdout, "piped tool_1.1.0_")
}

// TestInstallPipeOutputStaysOutOfTheJSONStream: the events are read line by
// line by whatever ingests them, and a command that prints its own progress
// must not land among them. It goes to the error stream instead, where a
// person still sees it.
func TestInstallPipeOutputStaysOutOfTheJSONStream(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	// The command's output is derived rather than quoted, so finding it is
	// finding what the command printed and not the command line itself, which
	// the event carries by design.
	res := r.install("--log-format", "json", "--pipe", `cat > unpacked; echo chatter | tr a-z A-Z`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "CHATTER")
	assert.NotContains(t, res.Stdout, "CHATTER")
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		var event map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &event), "every line is an event")
	}
	piped := suEvent(t, res.Stdout, "asset piped")
	assert.Equal(t, r.bin, piped["dir"])
	assert.FileExists(t, filepath.Join(r.bin, "unpacked"))
}

// TestInstallKeepsTheTokenAwayFromAnotherHost: the endpoint here comes from
// an argument rather than from a flag somebody set on purpose, so a URL naming
// any other host must not be enough to make dispat hand it the github.com
// credentials sitting in the environment.
func TestInstallKeepsTheTokenAwayFromAnotherHost(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		seen = append(seen, req.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	r := newToolRepo(t)
	secret := []string{"GITHUB_TOKEN=the-secret", "DISPAT_TOKEN=the-other"}

	// The repository's own host derives the endpoint, so nothing was set on
	// purpose and nothing is sent.
	host := strings.TrimPrefix(srv.URL, "http://")
	res := r.CommandEnv(secret, "install", "https://"+host+"/acme/tool",
		"--api-url", srv.URL, "--bin-dir", r.bin, "--check")
	assert.NotEqual(t, 0, res.Code)

	// --token-env is how a token reaches an endpoint deliberately.
	res = r.CommandEnv(secret, "install", "https://"+host+"/acme/tool",
		"--api-url", srv.URL, "--token-env", "DISPAT_TOKEN", "--bin-dir", r.bin, "--check")
	assert.NotEqual(t, 0, res.Code)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Empty(t, seen[0], "the github.com token stays where it belongs")
	assert.Equal(t, "Bearer the-other", seen[1], "and a named one is sent")
}

// TestInstallFromAPrivateRepository: the whole private path over the process
// boundary. The fake publishes its releases only to a bearer credential and
// answers the public download URL with a sign-in page, exactly as github.com
// does, so the tool landing on PATH and running is proof that both the listing
// and the asset were fetched with the token.
func TestInstallFromAPrivateRepository(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	r.requireToken("sesame")

	// Nothing is readable without the credential, and the refusal is about
	// the listing rather than about the download.
	res := r.install("--check")
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stderr+res.Stdout, "404")
	assert.Zero(t, countDownloads(r.requests()), "a listing nobody could read fetches nothing")

	res = r.installWithToken("sesame")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed tool "+toolNew+" at "+r.installed())
	assert.Equal(t, toolNew, r.version(r.installed()), "the file on PATH is the release")
	assert.Equal(t, 1, countAssetAPI(r.requests()), "the asset came from its API endpoint")
	assert.Zero(t, countDownloads(r.requests()),
		"and the public URL, which would have served a page, was never asked")
}

// TestInstallFromAPrivateRepositoryNeedsTheTokenNamed: the conventional
// GITHUB_TOKEN is not sent to an endpoint that came from an argument, so a
// private repository behind one is unreachable until --token-env says which
// variable to use. The run says so at debug level rather than leaving the
// reader with an unexplained refusal.
func TestInstallFromAPrivateRepositoryNeedsTheTokenNamed(t *testing.T) {
	r := newToolRepo(t)
	r.requireToken("sesame")

	res := r.CommandEnv([]string{"GITHUB_TOKEN=sesame"}, "install", "https://github.com/acme/tool",
		"--api-url", r.api, "--bin-dir", r.bin, "--asset", "tool-{os}-{arch}",
		"--check", "--log-level", "debug")
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "--token-env",
		"the run names the flag that would have sent it")

	res = r.installWithToken("sesame", "--check")
	assert.Equal(t, 1, res.Code, "with the token named there is something to install")
}

// TestInstallWithoutATokenStaysOnThePublicURL: the fence around the change.
// Every release the fake publishes names an asset endpoint, as every real one
// does, and a public repository still downloads from the browser URL and sends
// no credentials anywhere.
func TestInstallWithoutATokenStaysOnThePublicURL(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	res := r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r.version(r.installed()))
	assert.Equal(t, 1, countDownloads(r.requests()))
	assert.Zero(t, countAssetAPI(r.requests()),
		"no token, no reason to touch the endpoint that wants one")
}

// TestInstallEventsReachTheJSONStream: the report is for a person and the
// event is for the stream CI already ingests. A job that provisions a runner
// can record what it installed without scraping stdout.
func TestInstallEventsReachTheJSONStream(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	res := r.install("--log-format", "json", "--check")
	require.Equal(t, 1, res.Code)
	check := suEvent(t, res.Stdout, "install check")
	assert.Equal(t, "acme/tool", check["repository"])
	assert.Equal(t, "v"+toolNew, check["tag"])
	assert.Equal(t, "tool-"+platform(), check["asset"])
	assert.Equal(t, true, check["pending"])
	assert.Equal(t, r.installed(), check["path"])

	res = r.install("--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		var event map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &event),
			"every line is an event, so no report text leaked in beside them")
	}
	installed := suEvent(t, res.Stdout, "tool installed")
	assert.Equal(t, "acme/tool", installed["repository"])
	assert.Equal(t, r.installed(), installed["path"])
	assert.Equal(t, toolNew, installed["version"])
	assert.NotContains(t, installed, "backup", "a first install kept nothing")

	res = r.install("--log-format", "json", "--force")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, backupPath(r.installed()), suEvent(t, res.Stdout, "tool installed")["backup"])
}

// TestInstallTracesTheDecisionsItMade: the debug stream is what answers "why
// did it install that", which is the only question a download raises after
// the fact. Each of the four choices it makes says what it chose.
func TestInstallTracesTheDecisionsItMade(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)

	res := r.install("--log-format", "json", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, message := range []string{
		"install: destination resolved",
		"install: release selected",
		"install: asset selected",
		"install: asset downloaded and verified",
		"install: checksum matches the release digest",
	} {
		suEvent(t, res.Stdout, message)
	}
	assert.Equal(t, r.installed(),
		suEvent(t, res.Stdout, "install: the destination was compared against the release digest")["path"])
}

// TestInstallRefusesABadCommandLineBeforeAnyRequest: a usage mistake must
// cost nothing, so each of these is answered without the fake being asked a
// single question.
func TestInstallRefusesABadCommandLineBeforeAnyRequest(t *testing.T) {
	r := newToolRepo(t)
	for name, args := range map[string][]string{
		"a URL naming only a host":  {"install", "https://github.com/onlyowner"},
		"no repository at all":      {"install"},
		"two repositories":          {"install", "acme/tool", "acme/other"},
		"a name that is a path":     {"install", "acme/tool", "--as", "../evil"},
		"a rollback that installs":  {"install", "acme/tool", "--rollback", "--release", "1.0.0"},
		"a placeholder nobody has":  {"install", "acme/tool", "--asset", "tool-{arch64}"},
		"an owner beside the URL":   {"install", "acme/tool", "--owner", "other"},
		"a repo beside the URL":     {"install", "acme/tool", "--repo", "other"},
		"a flag of another command": {"install", "acme/tool", "--tag", "1.2.0"},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(r.requests())
			res := r.Command(append(args, "--api-url", r.api, "--bin-dir", r.bin)...)
			assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
			if !strings.Contains(name, "placeholder") {
				assert.Equal(t, before, len(r.requests()), "a usage mistake costs no request")
			}
		})
	}
}

// TestInstallNamesAFlagThatIsNotIts: the refusal a provisioning script's
// author reads. `--tag 1.2.0` is the mistake anyone pinning a version makes —
// --tag is commit's, and the value beside it used to become a second
// repository, so install answered by complaining about an argument nobody
// typed. The message must name the flag, say whose it is, and point at
// install's own way of asking, all before a single request.
func TestInstallNamesAFlagThatIsNotIts(t *testing.T) {
	r := newToolRepo(t)
	before := len(r.requests())

	res := r.Command("install", "acme/tool", "--tag", "1.2.0", "--api-url", r.api, "--bin-dir", r.bin)
	require.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "--tag is not an install flag")
	assert.Contains(t, out, "dispat commit")
	assert.Contains(t, out, "--release")
	assert.NotContains(t, out, "install takes one repository",
		"the flag is named, not the argument the mis-parse invented")
	assert.Equal(t, before, len(r.requests()), "and nothing was asked")
	assert.NoFileExists(t, r.installed())
}

// TestInstallManifestIsAShellScript: the property the install command is meant
// to carry, asserted as a script rather than as a sequence of assertions. A
// list of pinned installs is a shell script, and a shell script under `set -e`
// is only usable if every line either does its work or stops the file: the
// installs are idempotent, so re-running the manifest costs no transfer, and a
// usage mistake exits 2 rather than being ignored, so a mistyped line can never
// be mistaken for a completed one.
func TestInstallManifestIsAShellScript(t *testing.T) {
	requireShell(t)
	r := newToolRepo(t)
	r.publish("2.0.0", "other-"+platform(), "checksums.txt")

	manifest := func() string {
		return "set -e\n" +
			"dispat install acme/tool --release " + toolOld +
			" --api-url " + r.api + " --bin-dir " + r.bin + " --asset 'tool-{os}-{arch}'\n" +
			"dispat install acme/tool --release 2.0.0" +
			" --api-url " + r.api + " --bin-dir " + r.bin + " --asset 'other-{os}-{arch}' --as other\n"
	}

	res := r.Shell(manifest())
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolOld, r.version(r.installed()))
	assert.Equal(t, "2.0.0", r.version(filepath.Join(r.bin, "other"+exeSuffix())))

	// Run it again, the way a provisioning script runs on every boot: both
	// lines are satisfied already, so the file exits 0 having downloaded
	// nothing.
	before := countDownloads(r.requests())
	res = r.Shell(manifest())
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, before, countDownloads(r.requests()), "a second run pays for no transfer")

	// A line asking for the version with commit's --tag stops the file instead
	// of installing something else, which is what makes `set -e` worth writing.
	before = countDownloads(r.requests())
	res = r.Shell("set -e\n" +
		"dispat install acme/tool --tag 1.2.0 --api-url " + r.api + " --bin-dir " + r.bin + "\n" +
		"dispat install acme/tool --release " + toolNew +
		" --api-url " + r.api + " --bin-dir " + r.bin + " --asset 'tool-{os}-{arch}'\n")
	assert.Equal(t, 2, res.Code, "the usage exit reaches the shell")
	assert.Contains(t, res.Stdout+res.Stderr, "--tag is not an install flag")
	assert.Equal(t, before, countDownloads(r.requests()), "and the line after it never ran")
	assert.Equal(t, toolOld, r.version(r.installed()), "so the manifest's own version is still installed")
}

// TestInstallCommandWordKeepsItsScript: every command word permanently
// shadows a run script of the same name, and "install" is a name a repository
// might well have given one. The two-word spelling still reaches it.
func TestInstallCommandWordKeepsItsScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["install"] = models.Script{"echo the script ran"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Command("install", "acme/tool", "--check")
	assert.NotContains(t, res.Stdout, "the script ran", "the command word wins")

	res = r.RunScript("install", "--since", "all")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "the script ran", "the two-word spelling still reaches it")
}

// countDownloads is how many of the fake's answers were the asset itself,
// which is what proves a repeated invocation paid for no transfer.
func countDownloads(paths []string) int { return countPrefix(paths, "/dl/") }

// countAssetAPI is how many answers came from the asset's REST endpoint, the
// address a credential unlocks. Together with countDownloads it says which of
// the two addresses a run chose.
func countAssetAPI(paths []string) int { return countPrefix(paths, "/assets/") }

func countPrefix(paths []string, prefix string) int {
	var n int
	for _, path := range paths {
		if strings.HasPrefix(path, prefix) {
			n++
		}
	}
	return n
}

// privatePage stands in for the sign-in page github.com serves at the public
// download URL of a private repository's asset: a 200 that is not the file.
const privatePage = "<!DOCTYPE html><html><body>Sign in to GitHub</body></html>"

// requireShell skips where /bin/sh is not what a downloaded "tool" runs
// through. The scenarios prove which binary landed by running it, and on
// Windows a shell script is not a program.
func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's tool is a shell script")
	}
}
