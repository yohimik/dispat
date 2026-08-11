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

	i := &Installer{Exe: exe}
	backup, err := i.Install(context.Background(), asset, "1.1.0")
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

			i := &Installer{Exe: exe}
			_, err := i.Install(context.Background(), asset, want)
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
	i := &Installer{Exe: exe}
	_, err := i.Install(context.Background(), asset, "1.1.0")
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

	i := &Installer{Exe: exe}
	_, err := i.Install(context.Background(), Asset{Name: "dispat", URL: srv.URL}, "1.1.0")
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

	i := &Installer{Exe: exe}
	_, err := i.Install(context.Background(), Asset{Name: "dispat-x", URL: srv.URL}, "1.1.0")
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
	i := &Installer{Exe: exe}
	_, err := i.Install(context.Background(), asset, "1.1.0")
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
