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
	dlOld = "1.0.0"
	dlNew = "1.1.0"
)

// dlRepo is the fixture: a folder standing in for a folder on PATH, and a fake
// API publishing a tool for it.
type dlRepo struct {
	*harness.Repo
	bin string // the install folder under test
	api string
	// bodies maps a version to the bytes its asset serves, which is what lets
	// a scenario ask for a version and get that version.
	bodies map[string][]byte
	// assets maps a version to the file names its release attaches.
	assets map[string][]string
	// digests switches the published checksum off, for the GitHub Enterprise
	// versions that send none.
	digests bool
	// tagPrefix is what the fake's tags carry before their version.
	tagPrefix string

	mu   sync.Mutex
	hits []string
}

// dlScript is a tool: a shell script that says which version it is, which is
// what makes "did the right one land" answerable by running it.
func dlScript(version string) []byte {
	return []byte("#!/bin/sh\necho \"tool " + version + "\"\n")
}

func newDLRepo(t *testing.T) *dlRepo {
	t.Helper()
	r := &dlRepo{
		Repo: harness.New(t), bin: t.TempDir(), digests: true, tagPrefix: "v",
		bodies: map[string][]byte{dlOld: dlScript(dlOld), dlNew: dlScript(dlNew)},
		assets: map[string][]string{
			dlOld: {"tool-" + platform(), "checksums.txt"},
			dlNew: {"tool-" + platform(), "checksums.txt"},
		},
	}
	r.serve(t)
	return r
}

// platform is how the fixture spells this machine in an asset name, which is
// what --asset renders {os} and {arch} into.
func platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// serve stands up the fake API over whatever the fixture currently declares.
func (r *dlRepo) serve(t *testing.T) {
	t.Helper()
	var base string
	release := func(version string) map[string]any {
		body := r.bodies[version]
		sum := sha256.Sum256(body)
		assets := make([]map[string]any, 0, len(r.assets[version]))
		for _, name := range r.assets[version] {
			asset := map[string]any{
				"name": name, "size": len(body),
				"browser_download_url": base + "/dl/" + version,
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
		r.hits = append(r.hits, req.URL.Path)
		r.mu.Unlock()
		if version, ok := strings.CutPrefix(req.URL.Path, "/dl/"); ok {
			body, known := r.bodies[version]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
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
func (r *dlRepo) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hits...)
}

// download runs the command with the fake API and the fixture's folder wired
// in, naming the asset the way a reader would.
func (r *dlRepo) download(args ...string) harness.RunResult {
	r.T.Helper()
	return r.Command(append([]string{"download", "https://github.com/acme/tool",
		"--api-url", r.api, "--bin-dir", r.bin, "--asset", "tool-{os}-{arch}"}, args...)...)
}

// bare runs the command with nothing but the API and the folder, for the
// scenarios that are about the flags this leaves out.
func (r *dlRepo) bare(args ...string) harness.RunResult {
	r.T.Helper()
	return r.Command(append([]string{"download", "https://github.com/acme/tool",
		"--api-url", r.api, "--bin-dir", r.bin}, args...)...)
}

// installed is the path the tool lands at, and version asks it which one it is.
func (r *dlRepo) installed() string { return filepath.Join(r.bin, "tool"+exeSuffix()) }

// version runs an installed tool and reads which one it is. It is a plain
// exec rather than the harness's own runner: the harness drives dispat and
// appends --root to what it launches, and the thing being run here is not
// dispat.
func (r *dlRepo) version(path string) string {
	r.T.Helper()
	out, err := exec.Command(path).CombinedOutput()
	require.NoError(r.T, err, "running %s:\n%s", path, out)
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "tool ")
	require.True(r.T, ok, "not a version line: %q", out)
	return rest
}

// TestDownloadInstallsAToolFromAnotherRepository: the whole thing, over the
// process boundary. dispat reads somebody else's releases, fetches the file
// they named, checks it against what the release advertised, and puts it on
// PATH under the repository's own name, and the proof is that the file now
// runs and says which version it is.
func TestDownloadInstallsAToolFromAnotherRepository(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)

	// --check first: it changes nothing and exits 1 because there is
	// something to install, which is what makes it a gate.
	res := r.download("--check")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assertOrderedIn(t, res.Stdout,
		"repository acme/tool",
		"release    "+dlNew,
		"asset      tool-"+platform(),
		"install to "+r.installed(),
		"install it with: dispat download",
	)
	assert.NoFileExists(t, r.installed(), "--check touches nothing")

	res = r.download()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "downloading tool-"+platform())
	assert.Contains(t, res.Stdout, "installed tool "+dlNew+" at "+r.installed())
	assert.Equal(t, dlNew, r.version(r.installed()), "the file on PATH is the release")

	// Nothing was kept, because nothing was there: a first install has no
	// previous binary to talk about.
	assert.NotContains(t, res.Stdout, "the previous binary is at")
	assert.NoFileExists(t, backupPath(r.installed()))
}

// TestDownloadIsIdempotent: a provisioning script runs the same line on every
// boot, and the second run must cost nothing. The destination is compared
// against the checksum the release published, so "already there" is a fact
// about the bytes rather than about a version string anybody wrote down.
func TestDownloadIsIdempotent(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	require.Equal(t, 0, r.download().Code)
	before := r.requests()

	res := r.download()
	require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "is already v"+dlNew)
	assert.Contains(t, res.Stdout, "--force")
	assert.Equal(t, 0, countDownloads(r.requests())-countDownloads(before),
		"and the second run pays for no transfer")

	// --check agrees by exiting 0, which is the whole of the gate.
	assert.Equal(t, 0, r.download("--check").Code)

	// A destination somebody else overwrote is not this release, so the same
	// line installs again. This is what makes the command a repair.
	require.NoError(t, os.WriteFile(r.installed(), []byte("#!/bin/sh\necho tool 0.0.1\n"), 0o755))
	assert.Equal(t, 1, r.download("--check").Code, "different bytes are something to do")
	require.Equal(t, 0, r.download().Code)
	assert.Equal(t, dlNew, r.version(r.installed()))

	// --force installs it again anyway, and what it replaced is kept.
	res = r.download("--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "the previous binary is at "+backupPath(r.installed()))
	assert.Equal(t, dlNew, r.version(backupPath(r.installed())))
}

// TestDownloadKeepsAndRestoresWhatItReplaced: the same safety property
// self-update is built around, for a tool dispat knows nothing about. It
// rotates, so a restore is itself reversible and nobody has to be sure before
// pressing it.
func TestDownloadKeepsAndRestoresWhatItReplaced(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	require.Equal(t, 0, r.download("--release", dlOld).Code)
	require.Equal(t, dlOld, r.version(r.installed()))

	res := r.download()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, dlNew, r.version(r.installed()))
	assert.Equal(t, dlOld, r.version(backupPath(r.installed())), "and the old one is beside it")
	assert.Contains(t, res.Stdout, `put it back with "dispat download acme/tool --rollback"`)

	// The gate first: it says there is something to restore and restores
	// nothing.
	res = r.bare("--rollback", "--check")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "the backup of tool is at "+backupPath(r.installed()))
	assert.Equal(t, dlNew, r.version(r.installed()))

	res = r.bare("--rollback")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "rolled back tool at "+r.installed())
	assert.Equal(t, dlOld, r.version(r.installed()))
	assert.Equal(t, dlNew, r.version(backupPath(r.installed())))

	require.Equal(t, 0, r.bare("--rollback").Code)
	assert.Equal(t, dlNew, r.version(r.installed()), "a second rollback returns")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "nothing is parked and forgotten between the renames: %v", entries)
}

// TestDownloadBackupExpiresOnItsOwn: the copy is kept for a week and then
// removed by the next download of that same tool. Nothing has to be cleaned up
// by hand, and nothing else in the folder is ever touched.
func TestDownloadBackupExpiresOnItsOwn(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	require.Equal(t, 0, r.download("--release", dlOld).Code)
	require.Equal(t, 0, r.download().Code)
	require.FileExists(t, backupPath(r.installed()))

	sixDays := time.Now().Add(-6 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(backupPath(r.installed()), sixDays, sixDays))
	require.Equal(t, 0, r.download("--check").Code)
	assert.FileExists(t, backupPath(r.installed()), "inside the week it stays")

	eightDays := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(backupPath(r.installed()), eightDays, eightDays))
	require.Equal(t, 0, r.download("--check").Code)
	assert.NoFileExists(t, backupPath(r.installed()), "past the week the next one clears it")
	assert.Equal(t, dlNew, r.version(r.installed()), "and only the backup is ever removed")

	res := r.bare("--rollback")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "--release", "the refusal says how to get an old version anyway")
}

// TestDownloadNamesTheFileAndTheFolder: a project whose binary is not called
// after its repository, installed somewhere the reader chose. Both are flags
// because neither is derivable, and the folder is created rather than
// demanded.
func TestDownloadNamesTheFileAndTheFolder(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	elsewhere := filepath.Join(t.TempDir(), "nested", "bin")

	res := r.Command("download", "acme/tool", "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--bin-dir", elsewhere, "--as", "gh")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	installed := filepath.Join(elsewhere, "gh"+exeSuffix())
	assert.FileExists(t, installed, "the folder is created on the way")
	assert.Equal(t, dlNew, r.version(installed))
	assert.Contains(t, res.Stdout, "installed gh "+dlNew)
	assert.Contains(t, res.Stdout, "is not on PATH",
		"a tool the shell cannot find is a successful install that looks like a failed one")

	// ...and a folder the shell already searches says nothing, because there
	// is nothing for the reader to do about it.
	res = r.CommandEnv([]string{"PATH=" + elsewhere + string(os.PathListSeparator) + os.Getenv("PATH")},
		"download", "acme/tool", "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--bin-dir", elsewhere, "--as", "gh", "--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NotContains(t, res.Stdout, "is not on PATH")
}

// TestDownloadRefusesToGuessWhichFileIsTheBinary: a release carries a binary,
// a checksum file and often much more, and installing the wrong one globally
// is worse than any amount of typing. The refusal lists what is there.
func TestDownloadRefusesToGuessWhichFileIsTheBinary(t *testing.T) {
	r := newDLRepo(t)

	res := r.bare("--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "--asset")
	assert.Contains(t, res.Stdout, "checksums.txt", "it says what it found")

	// A release carrying exactly one file has one answer, and asking for a
	// pattern to state the obvious would be ceremony.
	r.assets[dlNew] = []string{"tool-" + platform()}
	res = r.bare("--check")
	assert.Equal(t, 1, res.Code, "still a gate: there is something to install")
	assert.Contains(t, res.Stdout, "asset      tool-"+platform())

	// A glob reaches a name nobody wants to type, and one reaching two files
	// is a pattern that has not chosen.
	r.assets[dlNew] = []string{"tool-" + platform(), "tool-" + platform() + ".sha256"}
	assert.Contains(t, r.bare("--asset", "tool-*", "--check").Stdout, "matches 2")

	res = r.bare("--asset", "*"+runtime.GOARCH, "--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "asset      tool-"+platform())

	// A release with nothing for this platform names what it does have.
	r.assets[dlNew] = []string{"tool-plan9-386"}
	res = r.bare("--asset", "tool-{os}-{arch}", "--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "tool-"+platform(), "the one it wanted")
	assert.Contains(t, res.Stdout, "tool-plan9-386", "and the one there is")
}

// TestDownloadRefusesWhatItCannotTrust: the checks stand between a download
// and a file that is about to be run, so a release whose checksum does not
// describe what arrives is refused with the folder untouched.
func TestDownloadRefusesWhatItCannotTrust(t *testing.T) {
	r := newDLRepo(t)
	var base string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := dlScript(dlNew)
		if strings.HasPrefix(req.URL.Path, "/dl/") {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "v" + dlNew, "draft": false, "prerelease": false,
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

	res := r.Command("download", "acme/tool", "--api-url", base, "--bin-dir", r.bin,
		"--asset", "tool-{os}-{arch}")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "hashes to")
	assert.Contains(t, res.Stdout, "download: ", "the failure names the command, not the package")
	assert.NoFileExists(t, r.installed(), "and nothing was installed")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Empty(t, entries, "the refused download is cleaned up: %v", entries)
}

// TestDownloadWithoutAPublishedChecksum: GitHub Enterprise versions before
// asset digests existed send none. That is a check that cannot be made, not a
// reason to refuse, and it is said out loud because it is also what makes the
// "already installed" question unanswerable.
func TestDownloadWithoutAPublishedChecksum(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	r.digests = false

	res := r.download()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, dlNew, r.version(r.installed()))
	assert.Contains(t, res.Stdout, "no checksum")

	// And the same line installs again rather than claiming it is done: a
	// guess that skips the install is what leaves a machine on an old binary
	// forever.
	assert.Equal(t, 1, r.download("--check").Code)
}

// TestDownloadReachesAnyPublishedVersion: --release names one exactly,
// downgrades included, and --prerelease is how a candidate line is opted into.
// Neither happens on its own.
func TestDownloadReachesAnyPublishedVersion(t *testing.T) {
	requireShell(t)
	const candidate = "1.2.0-rc.1"
	r := newDLRepo(t)
	r.bodies[candidate] = dlScript(candidate)
	r.assets[candidate] = []string{"tool-" + platform()}

	require.Equal(t, 0, r.download().Code)
	assert.Equal(t, dlNew, r.version(r.installed()), "the candidate is passed over")

	require.Equal(t, 0, r.download("--prerelease").Code)
	assert.Equal(t, candidate, r.version(r.installed()), "asked for, it is installed")

	require.Equal(t, 0, r.download("--release", "v"+dlOld).Code)
	assert.Equal(t, dlOld, r.version(r.installed()), "a named version is reached going backwards")

	res := r.download("--release", "9.9.9")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "v9.9.9")
	assert.Equal(t, dlOld, r.version(r.installed()), "a version nobody published changes nothing")
}

// TestDownloadReadsTheRepositoryTagsHoweverTheyAreSpelled: a foreign
// repository tags its releases its own way, and the two spellings that cover
// nearly all of them are "v1.2.3" and "1.2.3". Neither is guessable, so
// --tag-prefix says which, and a prefix nothing carries is named in the
// refusal rather than reported as "you are up to date".
func TestDownloadReadsTheRepositoryTagsHoweverTheyAreSpelled(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	r.tagPrefix = ""

	res := r.download("--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no matching release")
	assert.Contains(t, res.Stdout, "--tag-prefix")
	assert.Contains(t, res.Stdout, "--prerelease")

	res = r.download("--tag-prefix", "")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, dlNew, r.version(r.installed()))

	// A monorepo publishing a release per module is the other end of the
	// same idea, and the prefix is what filters the listing down to one.
	r2 := newDLRepo(t)
	r2.tagPrefix = "tool/v"
	res = r2.download("--tag-prefix", "tool/v")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, dlNew, r2.version(r2.installed()))
}

// TestDownloadPipesAnAssetThatIsNotABinary: most releases ship an archive, and
// unpacking one is the job of the tool that unpacks archives. The verification
// is the same either way, so what reaches the command has been checked against
// the size and the checksum the release published, and nothing is ever
// renamed onto PATH by dispat itself.
func TestDownloadPipesAnAssetThatIsNotABinary(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)

	res := r.download("--pipe", `cat > unpacked && chmod +x unpacked`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "piped tool-"+platform())
	assert.Equal(t, dlNew, r.version(filepath.Join(r.bin, "unpacked")),
		"the pipe runs in the install folder, so what it writes lands there")
	assert.NoFileExists(t, r.installed(), "and dispat installed nothing of its own")

	// The same file by path, for a command that has to seek, and the name the
	// release published, for one that switches on the archive layout.
	res = r.download("--pipe", `echo "name=$DISPAT_ASSET_NAME"; wc -c < "$DISPAT_ASSET"`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "name=tool-"+platform())
	assert.Contains(t, res.Stdout, fmt.Sprint(len(dlScript(dlNew))))

	// A pipe that fails is a failed install, and it leaves nothing behind.
	res = r.download("--pipe", "exit 3")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "the pipe failed")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only what the first pipe wrote: %v", entries)
}

// TestDownloadPipeIsAlwaysSomethingToDo: a pipe has no destination file to
// compare against, so the question "is it already installed" has no answer and
// the gate says so rather than inventing one.
func TestDownloadPipeIsAlwaysSomethingToDo(t *testing.T) {
	r := newDLRepo(t)
	res := r.download("--pipe", "cat > unpacked", "--check")
	assert.Equal(t, 1, res.Code)
	assertOrderedIn(t, res.Stdout, "asset      tool-"+platform(), "pipe       cat > unpacked", "in         "+r.bin)
	assert.NotContains(t, res.Stdout, "install to")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Empty(t, entries, "--check downloads nothing, pipe or not")
}

// TestDownloadRefusesADestinationItMustNotReplace: /usr/local/bin is a shared
// folder, and a name that is already a directory there belongs to somebody.
// Installing over it would rename that directory aside to put a binary where
// it stood, which is a thing no download may do quietly.
func TestDownloadRefusesADestinationItMustNotReplace(t *testing.T) {
	r := newDLRepo(t)
	require.NoError(t, os.Mkdir(r.installed(), 0o755))

	for _, args := range [][]string{nil, {"--force"}, {"--check"}} {
		res := r.download(args...)
		assert.Equal(t, 1, res.Code, "args: %v\nstdout:\n%s", args, res.Stdout)
		assert.Contains(t, res.Stdout, "is a folder")
	}
	assert.DirExists(t, r.installed(), "and it is still where it was")

	entries, err := os.ReadDir(r.bin)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing was staged beside it: %v", entries)

	// A pipe never touches the destination, so it never asks about it.
	res := r.download("--pipe", "cat > unpacked")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
}

// TestDownloadPipeSeesTheAssetUnderItsOwnName: an unpacker switches on the
// suffix, and .tar.gz is two of them. The staged file therefore carries the
// name the release published rather than the temporary one it arrived under.
func TestDownloadPipeSeesTheAssetUnderItsOwnName(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)
	r.assets[dlNew] = []string{"tool_1.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"}

	res := r.bare("--asset", "tool_{version}_{os}_{arch}.tar.gz",
		"--pipe", `case "$DISPAT_ASSET" in *.tar.gz) echo "suffix kept";; *) echo "suffix lost: $DISPAT_ASSET";; esac`)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "suffix kept")
	assert.Contains(t, res.Stdout, "piped tool_1.1.0_")
}

// TestDownloadPipeOutputStaysOutOfTheJSONStream: the events are read line by
// line by whatever ingests them, and a command that prints its own progress
// must not land among them. It goes to the error stream instead, where a
// person still sees it.
func TestDownloadPipeOutputStaysOutOfTheJSONStream(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)

	// The command's output is derived rather than quoted, so finding it is
	// finding what the command printed and not the command line itself, which
	// the event carries by design.
	res := r.download("--log-format", "json", "--pipe", `cat > unpacked; echo chatter | tr a-z A-Z`)
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

// TestDownloadKeepsTheTokenAwayFromAnotherHost: the endpoint here comes from
// an argument rather than from a flag somebody set on purpose, so a URL naming
// any other host must not be enough to make dispat hand it the github.com
// credentials sitting in the environment.
func TestDownloadKeepsTheTokenAwayFromAnotherHost(t *testing.T) {
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

	r := newDLRepo(t)
	secret := []string{"GITHUB_TOKEN=the-secret", "DISPAT_TOKEN=the-other"}

	// The repository's own host derives the endpoint, so nothing was set on
	// purpose and nothing is sent.
	host := strings.TrimPrefix(srv.URL, "http://")
	res := r.CommandEnv(secret, "download", "https://"+host+"/acme/tool",
		"--api-url", srv.URL, "--bin-dir", r.bin, "--check")
	assert.NotEqual(t, 0, res.Code)

	// --token-env is how a token reaches an endpoint deliberately.
	res = r.CommandEnv(secret, "download", "https://"+host+"/acme/tool",
		"--api-url", srv.URL, "--token-env", "DISPAT_TOKEN", "--bin-dir", r.bin, "--check")
	assert.NotEqual(t, 0, res.Code)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Empty(t, seen[0], "the github.com token stays where it belongs")
	assert.Equal(t, "Bearer the-other", seen[1], "and a named one is sent")
}

// TestDownloadEventsReachTheJSONStream: the report is for a person and the
// event is for the stream CI already ingests. A job that provisions a runner
// can record what it installed without scraping stdout.
func TestDownloadEventsReachTheJSONStream(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)

	res := r.download("--log-format", "json", "--check")
	require.Equal(t, 1, res.Code)
	check := suEvent(t, res.Stdout, "download check")
	assert.Equal(t, "acme/tool", check["repository"])
	assert.Equal(t, "v"+dlNew, check["tag"])
	assert.Equal(t, "tool-"+platform(), check["asset"])
	assert.Equal(t, true, check["pending"])
	assert.Equal(t, r.installed(), check["path"])

	res = r.download("--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		var event map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &event),
			"every line is an event, so no report text leaked in beside them")
	}
	installed := suEvent(t, res.Stdout, "tool installed")
	assert.Equal(t, "acme/tool", installed["repository"])
	assert.Equal(t, r.installed(), installed["path"])
	assert.Equal(t, dlNew, installed["version"])
	assert.NotContains(t, installed, "backup", "a first install kept nothing")

	res = r.download("--log-format", "json", "--force")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, backupPath(r.installed()), suEvent(t, res.Stdout, "tool installed")["backup"])
}

// TestDownloadTracesTheDecisionsItMade: the debug stream is what answers "why
// did it install that", which is the only question a download raises after
// the fact. Each of the four choices it makes says what it chose.
func TestDownloadTracesTheDecisionsItMade(t *testing.T) {
	requireShell(t)
	r := newDLRepo(t)

	res := r.download("--log-format", "json", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, message := range []string{
		"download: destination resolved",
		"download: release selected",
		"download: asset selected",
		"download: asset downloaded and verified",
		"download: checksum matches the release digest",
	} {
		suEvent(t, res.Stdout, message)
	}
	assert.Equal(t, r.installed(),
		suEvent(t, res.Stdout, "download: the destination was compared against the release digest")["path"])
}

// TestDownloadRefusesABadCommandLineBeforeAnyRequest: a usage mistake must
// cost nothing, so each of these is answered without the fake being asked a
// single question.
func TestDownloadRefusesABadCommandLineBeforeAnyRequest(t *testing.T) {
	r := newDLRepo(t)
	for name, args := range map[string][]string{
		"a URL naming only a host": {"download", "https://github.com/onlyowner"},
		"no repository at all":     {"download"},
		"two repositories":         {"download", "acme/tool", "acme/other"},
		"a name that is a path":    {"download", "acme/tool", "--as", "../evil"},
		"a rollback that installs": {"download", "acme/tool", "--rollback", "--release", "1.0.0"},
		"a placeholder nobody has": {"download", "acme/tool", "--asset", "tool-{arch64}"},
		"an owner beside the URL":  {"download", "acme/tool", "--owner", "other"},
		"a repo beside the URL":    {"download", "acme/tool", "--repo", "other"},
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

// TestDownloadCommandWordKeepsItsScript: every command word permanently
// shadows a run script of the same name, and "download" is a name a repository
// might well have given one. The two-word spelling still reaches it.
func TestDownloadCommandWordKeepsItsScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["download"] = models.Script{"echo the script ran"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Command("download", "acme/tool", "--check")
	assert.NotContains(t, res.Stdout, "the script ran", "the command word wins")

	res = r.RunScript("download", "--since", "all")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "the script ran", "the two-word spelling still reaches it")
}

// countDownloads is how many of the fake's answers were the asset itself,
// which is what proves a repeated invocation paid for no transfer.
func countDownloads(paths []string) int {
	var n int
	for _, path := range paths {
		if strings.HasPrefix(path, "/dl/") {
			n++
		}
	}
	return n
}

// requireShell skips where /bin/sh is not what a downloaded "tool" runs
// through. The scenarios prove which binary landed by running it, and on
// Windows a shell script is not a program.
func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's tool is a shell script")
	}
}
