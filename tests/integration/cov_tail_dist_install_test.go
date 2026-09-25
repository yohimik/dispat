// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the two download commands: what `dispat install`
// refuses before it asks anybody anything, and what `dispat self-update`
// refuses about a release, a listing and the backup it would put back. Every
// one of them is an ordinary mistake or an ordinary broken server rather than
// an exotic one, and each has to name what the reader is to change.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovTailInstallRefusesWhatItCannotResolve: the repository reference, the
// asset pattern and the destination folder are all read before a request, so
// each mistake in them costs nothing and has to say which part was wrong.
func TestCovTailInstallRefusesWhatItCannotResolve(t *testing.T) {
	t.Run("a reference with an empty part", func(t *testing.T) {
		for name, tc := range map[string]struct {
			ref  string
			want string
		}{
			"only whitespace":                        {ref: "   ", want: "no repository given"},
			"an owner and no repository":             {ref: "acme/", want: "names no repository"},
			"an empty segment between the two names": {ref: "acme//tool", want: "the repository is empty"},
		} {
			t.Run(name, func(t *testing.T) {
				r := newToolRepo(t)
				res := r.Command("install", tc.ref, "--api-url", r.api, "--check")
				assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
				assert.Contains(t, res.Stdout+res.Stderr, tc.want)
				assert.Contains(t, res.Stdout+res.Stderr, "owner/repo",
					"the refusal shows the spelling that would have worked")
			})
		}
	})

	t.Run("an asset pattern with a placeholder that never closes", func(t *testing.T) {
		r := newToolRepo(t)
		res := r.Command("install", "acme/tool", "--api-url", r.api,
			"--asset", "tool-{os}-{arch", "--check")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "never closed")
	})

	t.Run("a destination it cannot even look at", func(t *testing.T) {
		r := newToolRepo(t)
		// A file where the folder's parent would be: the destination cannot be
		// examined at all, which is neither "nothing is there" nor "something
		// is", and an install may not proceed on a question it could not ask.
		blocker := filepath.Join(t.TempDir(), "not-a-folder")
		require.NoError(t, os.WriteFile(blocker, []byte("a file\n"), 0o644))

		res := r.Command("install", "acme/tool", "--api-url", r.api,
			"--asset", "tool-{os}-{arch}", "--bin-dir", filepath.Join(blocker, "bin"))
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "cannot be read")
	})
}

// TestCovTailInstallRefusesAReleaseThatCarriesNothing: a release with no files
// attached is a real shape — a tag cut before the build finished, a workflow
// that failed after creating the release — and it is not an asset-name
// mismatch. The refusal names the release rather than the pattern, so the
// reader does not go looking at their own spelling.
func TestCovTailInstallRefusesAReleaseThatCarriesNothing(t *testing.T) {
	r := newToolRepo(t)
	r.attach(toolNew)

	res := r.Command("install", "acme/tool", "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--check")
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "carries no files to download")
}

// TestCovTailSelfUpdateRollbackChecksTheBackupFirst: a rollback is only worth
// doing if the file it would put back is a working dispat, and finding out
// otherwise afterwards means finding out with no dispat at all. So the backup
// is run first: one that is not a program refuses the rollback outright, and
// one that runs without saying which version it is rolls back with the
// version left unstated rather than guessed.
func TestCovTailSelfUpdateRollbackChecksTheBackupFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in backups are shell scripts")
	}
	updated := func(t *testing.T) *suRepo {
		t.Helper()
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code, "the update that leaves a backup behind")
		require.Equal(t, suNew, r.version(r.exe))
		return r
	}

	t.Run("a backup that is not a program refuses the rollback", func(t *testing.T) {
		r := updated(t)
		require.NoError(t, os.WriteFile(r.backup, []byte("this was never a binary\n"), 0o755))

		res := r.CommandBin(r.exe, "self-update", "--rollback")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "does not run")
		assert.Equal(t, suNew, r.version(r.exe), "and the working binary is where it was")
	})

	t.Run("a backup that says nothing rolls back with the version unstated", func(t *testing.T) {
		r := updated(t)
		require.NoError(t, os.WriteFile(r.backup, []byte("#!/bin/sh\nexit 0\n"), 0o755))

		res := r.CommandBin(r.exe, "self-update", "--check", "--rollback")
		assert.Equal(t, 1, res.Code, "there is still something to restore; stdout:\n%s", res.Stdout)
		assert.NotContains(t, res.Stdout, "is dispat "+suOld,
			"a version nothing stated is not one to print")
	})
}

// TestCovTailSelfUpdateRefusesAnAnswerThatIsNotARelease: the update check
// reads somebody else's server, so each way an answer can fail to be a release
// is refused on its own terms: a listing that is not a listing, a version
// that is not a version, and a release whose body is not a release.
func TestCovTailSelfUpdateRefusesAnAnswerThatIsNotARelease(t *testing.T) {
	t.Run("a listing that is not JSON", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`<html>a proxy sign-in page</html>`))
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "listing releases")
	})

	t.Run("a release name that is not a version", func(t *testing.T) {
		r := newSURepo(t)
		res := r.update("--release", "nightly")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "is not a version")
		assert.Equal(t, suOld, r.version(r.exe))
	})

	t.Run("a release whose body is not a release", func(t *testing.T) {
		api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html>a proxy sign-in page</html>`))
		})
		r := harness.New(t)
		exe := covSUExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--release", "1.2.3",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "looking up")
	})
}

// TestCovTailSelfUpdateReadsItsOwnRepositoryByDefault: --owner and --repo
// exist for a fork, and leaving them out has to reach dispat's own repository
// rather than nothing. Pointed at a fake that publishes under those defaults,
// the plain command finds the release, which is what says the defaults are
// what a real run uses.
func TestCovTailSelfUpdateReadsItsOwnRepositoryByDefault(t *testing.T) {
	payload := covSUPayload(t, suNew)
	var asked []string
	api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
		asked = append(asked, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, payload)})
	})
	r := harness.New(t)
	exe := covSUExe(t, suOld)

	res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base)
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "available dispat "+suNew)
	assert.Contains(t, strings.Join(asked, "\n"), "/repos/yohimik/dispat/releases",
		"the defaults are dispat's own repository, not an empty pair")
}
