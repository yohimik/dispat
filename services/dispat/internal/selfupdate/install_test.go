package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The install and rollback paths both run the binary they are about to trust,
// so these tests need files that really execute. A shell script is the
// cheapest thing that does, which also means they are Unix tests: Windows is
// covered by the integration suite, which builds real binaries.
func requireExec(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixtures are shell scripts")
	}
}

// fakeBinary writes something that answers --version the way dispat does.
func fakeBinary(t *testing.T, path, version string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho \"dispat %s (%s_%s)\"\n", version, runtime.GOOS, runtime.GOARCH)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

// brokenBinary writes a file that cannot run at all.
func brokenBinary(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("\x00\x01not a program"), 0o755))
}

// assetServer serves body as the release asset, and reports how the asset
// should be described so the checks agree with what is on the wire.
func assetServer(t *testing.T, body []byte) (Asset, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The asset download must never carry credentials: it redirects to
		// object storage, which rejects a forwarded Authorization header.
		assert.Empty(t, req.Header.Get("Authorization"), "the asset download is unauthenticated")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	return Asset{
		Name: CurrentAssetName(), URL: srv.URL + "/dl",
		Size: int64(len(body)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}, srv
}

// TestInstallReplacesTheBinaryAndKeepsTheOldOne: the whole path, end to end.
// What was running is kept as the backup with its clock started now, what
// arrived is in its place, and the mode of the outgoing binary is carried
// over rather than reset to whatever the umask says.
func TestInstallReplacesTheBinaryAndKeepsTheOldOne(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")
	require.NoError(t, os.Chmod(exe, 0o750))

	newBinary := []byte("#!/bin/sh\necho \"dispat 1.1.0 (test)\"\n")
	asset, _ := assetServer(t, newBinary)

	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	backup, err := i.Install(context.Background(), asset)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "dispat.backup"), backup)
	assert.Equal(t, newBinary, read(t, exe), "the release is in place")
	assert.Contains(t, string(read(t, backup)), "1.0.0", "the outgoing binary is kept")

	info, err := os.Stat(exe)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), info.Mode().Perm(), "the mode is carried over, execute bits included")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "nothing is left behind: %v", names(entries))
}

// TestInstallRefusesWhatDoesNotMatchTheRelease: every check happens before the
// swap, so a download that is truncated, corrupt, or simply not the version
// that was asked for leaves the working binary exactly where it was.
func TestInstallRefusesWhatDoesNotMatchTheRelease(t *testing.T) {
	requireExec(t)
	body := []byte("#!/bin/sh\necho \"dispat 1.1.0 (test)\"\n")
	good, _ := assetServer(t, body)

	for name, tc := range map[string]struct {
		mutate func(*Asset)
		want   string
		want2  string
	}{
		"a digest that does not match": {
			mutate: func(a *Asset) { a.Digest = "sha256:" + hex.EncodeToString(make([]byte, 32)) },
			want:   "hashes to",
		},
		"a size that does not match": {
			mutate: func(a *Asset) { a.Size = 99999 },
			want:   "the download is incomplete",
		},
		"a version that does not match": {
			mutate: func(a *Asset) {}, want: "different version",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "dispat")
			fakeBinary(t, exe, "1.0.0")

			asset := good
			tc.mutate(&asset)
			want := "1.1.0"
			if name == "a version that does not match" {
				want = "2.0.0"
			}

			i := &Installer{Exe: exe, Validator: VersionValidator{Want: want}}
			_, err := i.Install(context.Background(), asset)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)

			assert.Contains(t, string(read(t, exe)), "1.0.0", "the working binary is untouched")
			assert.NoFileExists(t, BackupPath(exe), "nothing was moved, so there is no backup")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "the failed download is cleaned up: %v", names(entries))
		})
	}
}

// TestInstallRefusesABinaryThatDoesNotRun: a file can arrive intact and still
// be the wrong thing entirely. Finding that out after the swap means finding
// it out with no dispat left.
func TestInstallRefusesABinaryThatDoesNotRun(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	asset, _ := assetServer(t, []byte("\x00\x01not a program"))
	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), asset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not run")
	assert.Contains(t, string(read(t, exe)), "1.0.0")
}

// TestInstallRefusesBeforeDownloadingWhenItCannotWrite: /usr/local/bin
// belongs to root on most machines. The refusal has to come before fifteen
// megabytes cross the network, and it has to say what to do about it.
func TestInstallRefusesBeforeDownloadingWhenItCannotWrite(t *testing.T) {
	requireExec(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()

	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), Asset{Name: "dispat", URL: srv.URL})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not writable")
	assert.Contains(t, err.Error(), "re-run with the rights")
	assert.Zero(t, hits, "the refusal costs no download")
}

// TestInstallReportsADownloadThatFails: a release whose asset URL answers
// with anything but 200 is an error naming the status, not a silent no-op.
func TestInstallReportsADownloadThatFails(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), Asset{Name: "dispat-x", URL: srv.URL})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

// TestInstallAcceptsAReleaseWithoutADigest: GitHub Enterprise versions before
// asset digests existed send none. That is a check that cannot be made, not a
// reason to refuse an update, and the size and the smoke test still stand.
func TestInstallAcceptsAReleaseWithoutADigest(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	asset, _ := assetServer(t, []byte("#!/bin/sh\necho \"dispat 1.1.0 (test)\"\n"))
	asset.Digest = ""
	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), asset)
	require.NoError(t, err)
	assert.Contains(t, string(read(t, exe)), "1.1.0")
}

// TestReplaceRestoresTheBinaryWhenTheSecondRenameFails: between the two
// renames there is no binary at exe. If the second one cannot happen, the old
// one goes back rather than leaving nothing to run.
func TestReplaceRestoresTheBinaryWhenTheSecondRenameFails(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	// A file that is not there when the second rename comes: the shape of
	// every way that rename can fail, and the only one a test can stage
	// without root.
	_, err := Replace(exe, filepath.Join(dir, "vanished"))
	require.Error(t, err)
	assert.FileExists(t, exe, "the binary is back where it was")
	assert.Contains(t, string(read(t, exe)), "1.0.0")
}

// TestReplaceOverwritesAnOlderBackup: two updates in a row leave one backup,
// the binary the second one replaced. Windows will not rename onto a file
// that exists, so the old one is removed rather than renamed over.
func TestReplaceOverwritesAnOlderBackup(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	require.NoError(t, os.WriteFile(BackupPath(exe), []byte("ancient"), 0o755))
	incoming := filepath.Join(dir, "incoming")
	fakeBinary(t, incoming, "1.2.0")

	backup, err := Replace(exe, incoming)
	require.NoError(t, err)
	assert.Contains(t, string(read(t, exe)), "1.2.0")
	assert.Contains(t, string(read(t, backup)), "1.1.0", "the backup is the binary just replaced")
}

// TestInstallerDefaultsToTheRunningBinary: an installer nobody configured
// replaces the binary that is running, through a client that will wait out a
// large download on a slow link.
func TestInstallerDefaultsToTheRunningBinary(t *testing.T) {
	var i Installer
	exe, err := i.exe()
	require.NoError(t, err)
	running, err := Executable()
	require.NoError(t, err)
	assert.Equal(t, running, exe)
	assert.Equal(t, downloadTimeout, i.client().Timeout)

	// The same default on the rollback side, which finds no backup beside
	// the test binary and says so rather than touching anything.
	_, _, err = Rollback(context.Background(), "")
	assert.ErrorIs(t, err, ErrNoBackup)
}

// TestReplaceReportsAFirstRenameThatFails: before the first rename nothing
// has moved, so the failure is simply reported. It is the case where there is
// nothing to undo, and the message has to name what it could not move.
func TestReplaceReportsAFirstRenameThatFails(t *testing.T) {
	requireExec(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	incoming := filepath.Join(dir, "incoming")
	fakeBinary(t, exe, "1.0.0")
	fakeBinary(t, incoming, "1.1.0")
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := Replace(exe, incoming)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "moving "+exe+" aside")
	assert.FileExists(t, incoming, "and the file that would have replaced it is still there")
	assert.Contains(t, string(read(t, exe)), "1.0.0", "the binary it could not move is untouched")
}

// TestReplaceInstallsWhereNothingWasBefore: a path nothing occupies yet is a
// first install rather than a replacement. There is nothing to step aside, so
// the single rename is the whole of it and no backup is reported: `dispat
// download` puts a tool somewhere for the first time through exactly this.
func TestReplaceInstallsWhereNothingWasBefore(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	incoming := filepath.Join(dir, "incoming")
	fakeBinary(t, incoming, "1.1.0")

	backup, err := Replace(exe, incoming)
	require.NoError(t, err)
	assert.Empty(t, backup, "there was nothing to keep")
	assert.Contains(t, string(read(t, exe)), "1.1.0")
	assert.NoFileExists(t, BackupPath(exe), "and none was invented")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing is left beside it: %v", names(entries))
}

// TestRestoreRotatesWithoutRunningAnything: the rotation the two restores
// share. It asks nothing of either file, which is what lets `dispat install`
// use it for a tool that answers no --version at all, and it rotates rather
// than moves, so a second call returns.
func TestRestoreRotatesWithoutRunningAnything(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(BackupPath(exe), []byte("previous"), 0o755))

	require.NoError(t, Restore(exe))
	assert.Equal(t, "previous", string(read(t, exe)))
	assert.Equal(t, "current", string(read(t, BackupPath(exe))))

	require.NoError(t, Restore(exe))
	assert.Equal(t, "current", string(read(t, exe)), "a second restore returns")
	assert.Equal(t, "previous", string(read(t, BackupPath(exe))))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "nothing is parked and forgotten between the renames: %v", names(entries))

	// The backup's clock starts at the rotation, so the week PruneBackup
	// counts is the week since it became one.
	info, err := os.Stat(BackupPath(exe))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), info.ModTime(), time.Minute)

	assert.ErrorIs(t, Restore(filepath.Join(dir, "absent")), ErrNoBackup)
}

// TestRestoreReportsTheOneLegItCanLose: the rotation is three renames, and the
// last of them only decides whether the restore is itself reversible. A
// failure there is named rather than described, because both callers have to
// tell "it did not happen" from "it happened and cannot be undone again", and
// deciding that by reading a message is how the distinction goes missing the
// day the message is reworded.
func TestRestoreReportsTheOneLegItCanLose(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the scenario needs a directory the running user cannot write")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(exe, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(BackupPath(exe), []byte("previous"), 0o755))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := Restore(exe)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotWritable, "nothing moved, so nothing was restored")
	assert.Equal(t, "current", string(read(t, exe)))
}

// TestReplaceReportsAnUnremovableBackup: the previous backup is removed first
// because Windows will not rename onto a file that exists. A path that cannot
// be cleared stops the swap before anything moves.
func TestReplaceReportsAnUnremovableBackup(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")
	// A non-empty directory where the backup belongs: Remove refuses it.
	require.NoError(t, os.Mkdir(BackupPath(exe), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(BackupPath(exe), "x"), nil, 0o644))

	_, err := Replace(exe, filepath.Join(dir, "incoming"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing the previous backup")
	assert.FileExists(t, exe, "the working binary never moved")
}

// TestRollbackRefusesWhenItCannotWrite: the rotate needs somewhere to park the
// outgoing binary, so a directory that cannot be written to is refused before
// anything is renamed.
func TestRollbackRefusesWhenItCannotWrite(t *testing.T) {
	requireExec(t)
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.1.0")
	fakeBinary(t, BackupPath(exe), "1.0.0")
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, _, err := Rollback(context.Background(), exe)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not writable")
}

// TestInstallerKeepsTheClientItWasGiven: the command hands its own client down
// so a test, or a proxy configuration, reaches the download too.
func TestInstallerKeepsTheClientItWasGiven(t *testing.T) {
	client := &http.Client{Timeout: time.Second}
	i := Installer{Client: client}
	assert.Same(t, client, i.client())
}

// TestAtReportsAnAnswerItCannotRead: an endpoint that is reachable and
// returns something other than a release is an error, not an absent version.
func TestAtReportsAnAnswerItCannotRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html>this is not the api</html>")
	}))
	defer srv.Close()

	s := &Source{APIURL: srv.URL, Owner: "o", Repo: "r"}
	_, err := s.At(context.Background(), "1.0.0")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoRelease)
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestInstallWithATokenDownloadsFromTheAPIEndpoint: a token moves the
// download to the asset's API endpoint with the octet-stream Accept — the
// address that answers for a repository the public URL will not serve — and
// the public URL is left alone.
func TestInstallWithATokenDownloadsFromTheAPIEndpoint(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	newBinary := []byte("#!/bin/sh\necho \"dispat 1.1.0 (test)\"\n")
	publicHits := 0
	public := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { publicHits++ }))
	t.Cleanup(public.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "Bearer sesame", req.Header.Get("Authorization"))
		assert.Equal(t, "application/octet-stream", req.Header.Get("Accept"))
		w.Header().Set("Content-Length", fmt.Sprint(len(newBinary)))
		_, _ = w.Write(newBinary)
	}))
	t.Cleanup(api.Close)
	sum := sha256.Sum256(newBinary)
	a := Asset{
		Name: CurrentAssetName(), URL: public.URL + "/dl", APIURL: api.URL + "/assets/1",
		Size: int64(len(newBinary)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}

	i := &Installer{Exe: exe, Token: "sesame", Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), a)
	require.NoError(t, err)
	assert.Zero(t, publicHits, "a token means the public URL is never asked")
}

// TestInstallWithoutATokenIgnoresTheAPIEndpoint: no token, no change — the
// public URL serves the bytes exactly as before, unauthenticated, even when
// the listing reported an API endpoint.
func TestInstallWithoutATokenIgnoresTheAPIEndpoint(t *testing.T) {
	requireExec(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "dispat")
	fakeBinary(t, exe, "1.0.0")

	newBinary := []byte("#!/bin/sh\necho \"dispat 1.1.0 (test)\"\n")
	a, _ := assetServer(t, newBinary)
	a.APIURL = "http://127.0.0.1:1/never-asked"

	i := &Installer{Exe: exe, Validator: VersionValidator{Want: "1.1.0"}}
	_, err := i.Install(context.Background(), a)
	require.NoError(t, err)
}
