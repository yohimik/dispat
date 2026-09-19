// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the machine-readable half of the two download
// commands. `--log-format json` is what a provisioning script or a CI gate
// reads, and every outcome those commands have — nothing to do, something to
// install, a rollback with a backup and one without — has to be a structured
// line rather than the sentence a person reads. The pretty half of each pair
// is what the rest of the suite already drives, so these assert the shape and
// the exit code together.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailJSONLine is the first logged line whose message is msg, failing the
// test when there is none.
func covTailJSONLine(t *testing.T, res harness.RunResult, msg string) harness.Event {
	t.Helper()
	for _, e := range res.Events {
		if e.Str("message") == msg {
			return e
		}
	}
	t.Fatalf("no %q line in:\n%s\nstderr:\n%s", msg, res.Stdout, res.Stderr)
	return nil
}

// TestCovTailSelfUpdateReportsItselfAsJSON: the update check is a CI gate as
// often as a person's question, so each of its outcomes is one structured line
// carrying what the gate decides on — the version running, the version
// available, and whether the same invocation without --check would change the
// binary.
func TestCovTailSelfUpdateReportsItselfAsJSON(t *testing.T) {
	jsonArgs := func(args ...string) []string {
		return append([]string{"--log-format", "json"}, args...)
	}

	t.Run("a check that has something to install", func(t *testing.T) {
		r := newSURepo(t)
		res := r.update(jsonArgs("--check")...)
		assert.Equal(t, 1, res.Code, "the gate fails when there is something to do")
		line := covTailJSONLine(t, res, "update check")
		assert.Equal(t, suOld, line.Str("version"))
		assert.Equal(t, suNew, line.Str("latest"))
		assert.Equal(t, true, line["pending"])
	})

	t.Run("an update with nothing to install", func(t *testing.T) {
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code, "the update that brings it up to date")
		require.Equal(t, suNew, r.version(r.exe))

		// The same command again: already current, and the JSON line says so
		// rather than the command saying nothing.
		res := r.CommandBin(r.exe, append([]string{"self-update", "--api-url", r.api,
			"--owner", "o", "--repo", "r"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line := covTailJSONLine(t, res, "already on the latest release")
		assert.Equal(t, suNew, line.Str("version"))
		assert.Equal(t, suNew, line.Str("latest"))
	})

	t.Run("a rollback with nothing to put back", func(t *testing.T) {
		r := newSURepo(t)
		res := r.CommandBin(r.exe, append([]string{"self-update", "--rollback", "--check"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "nothing to do is not a failure; stdout:\n%s", res.Stdout)
		line := covTailJSONLine(t, res, "no backup to roll back to")
		assert.Equal(t, false, line["pending"])

		// And the sentence a person reads, for the same state.
		res = r.CommandBin(r.exe, "self-update", "--rollback", "--check")
		require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "there is no backup to roll back to")
	})

	t.Run("a rollback with a backup to put back", func(t *testing.T) {
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code)
		require.Equal(t, suNew, r.version(r.exe))

		res := r.CommandBin(r.exe, append([]string{"self-update", "--rollback", "--check"}, jsonArgs()...)...)
		assert.Equal(t, 1, res.Code, "there is something to restore")
		line := covTailJSONLine(t, res, "a backup is available")
		assert.Equal(t, suOld, line.Str("backup"))
		assert.Equal(t, true, line["pending"])

		res = r.CommandBin(r.exe, append([]string{"self-update", "--rollback"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line = covTailJSONLine(t, res, "rolled back")
		assert.Equal(t, suNew, line.Str("from"))
		assert.Equal(t, suOld, line.Str("version"))
		assert.Equal(t, filepath.Base(r.exe), filepath.Base(line.Str("path")))
		assert.Equal(t, suOld, r.version(r.exe), "and the file itself is the one the line named")
	})
}

// TestCovTailInstallReportsItselfAsJSON: the same for `dispat install`, whose
// reader is nearly always a provisioning script. Every outcome carries the
// destination path, because a script that installed something has to know
// where it went, and the pending flag the --check gate exits on.
func TestCovTailInstallReportsItselfAsJSON(t *testing.T) {
	// --rollback downloads nothing, so it is refused beside --asset; the two
	// invocations are spelled separately for that reason.
	install := func(r *toolRepo, dir string, args ...string) harness.RunResult {
		r.T.Helper()
		return r.Command(append([]string{"install", "acme/tool", "--api-url", r.api,
			"--asset", "tool-{os}-{arch}", "--bin-dir", dir, "--log-format", "json"}, args...)...)
	}
	rollback := func(r *toolRepo, dir string, args ...string) harness.RunResult {
		r.T.Helper()
		return r.Command(append([]string{"install", "acme/tool", "--api-url", r.api,
			"--bin-dir", dir, "--rollback"}, args...)...)
	}

	t.Run("an install, then the same install with nothing left to do", func(t *testing.T) {
		r := newToolRepo(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "tool"+exeSuffix())

		res := install(r, dir)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.FileExists(t, path)

		res = install(r, dir)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line := covTailJSONLine(t, res, "already installed")
		assert.Equal(t, path, line.Str("path"))
		assert.NotEmpty(t, line.Str("tag"), "the release it matched is named")
	})

	t.Run("a rollback with nothing to put back", func(t *testing.T) {
		r := newToolRepo(t)
		dir := t.TempDir()
		res := rollback(r, dir, "--check", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line := covTailJSONLine(t, res, "no backup to roll back to")
		assert.Equal(t, false, line["pending"])

		res = rollback(r, dir, "--check")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "there is no backup of")
	})

	t.Run("a rollback with a backup to put back", func(t *testing.T) {
		r := newToolRepo(t)
		dir := t.TempDir()
		require.Equal(t, 0, install(r, dir).Code, "the first install")
		r.publish("1.2.0", "tool-"+platform(), "checksums.txt")
		require.Equal(t, 0, install(r, dir).Code, "the second, which keeps the first beside it")

		res := rollback(r, dir, "--check", "--log-format", "json")
		assert.Equal(t, 1, res.Code, "there is something to restore; stdout:\n%s", res.Stdout)
		assert.NotEmpty(t, covTailJSONLine(t, res, "a backup is available").Str("backup"))

		res = rollback(r, dir, "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line := covTailJSONLine(t, res, "rolled back")
		assert.Equal(t, filepath.Join(dir, "tool"+exeSuffix()), line.Str("path"))
	})

	t.Run("a check that would hand the file to a command", func(t *testing.T) {
		r := newToolRepo(t)
		dir := t.TempDir()
		res := install(r, dir, "--pipe", "cat", "--check")
		assert.Equal(t, 1, res.Code, "a pipe always has something to do; stdout:\n%s", res.Stdout)
		line := covTailJSONLine(t, res, "install check")
		assert.Equal(t, "cat", line.Str("pipe"))
		assert.Equal(t, dir, line.Str("dir"), "a piped install names the folder it runs in")
	})
}

// TestCovTailSelfUpdateCheckCarriesTheNotesAsFields: a check that found
// something to install carries the release's own notes, so a job that opens a
// pull request with them does not have to fetch the release a second time to
// read what changed.
func TestCovTailSelfUpdateCheckCarriesTheNotesAsFields(t *testing.T) {
	payload := covSUPayload(t, suNew)
	api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, payload)})
	})
	r := harness.New(t)
	exe := covSUExe(t, suOld)

	res := r.CommandBin(exe, "self-update", "--check", "--log-format", "json",
		"--api-url", api.base, "--owner", "o", "--repo", "r")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	line := covTailJSONLine(t, res, "update check")
	assert.Equal(t, "services/dispat/v"+suNew, line.Str("tag"))

	rendered := strings.Join([]string{line.Str("notes"), line.Str("changelog"), res.Stdout}, "\n")
	assert.Contains(t, rendered, "Features", "the notes reach the field, not only the terminal")
	assert.NotContains(t, rendered, "curl -fsSL",
		"and the install footer is no more notes here than it is on a terminal")
}
