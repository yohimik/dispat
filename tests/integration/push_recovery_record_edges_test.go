// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPushRecoveryRefusesAnUnreadableBranchAfterPublication targets the
// recovery-only branch read. The initial guard reads the branch successfully;
// after a concurrent remote commit rejects the release push, losing that same
// repository fact must preserve the local immutable record and report the
// publication as incomplete rather than pretending it reached the remote.
func TestPushRecoveryRefusesAnUnreadableBranchAfterPublication(t *testing.T) {
	r, remote := newFinalRecoveryRepo(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*rev-parse --abbrev-ref HEAD*", Nth: 2, Code: 128,
	})

	failed := r.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.True(t, harness.IsCodePresent(failed.Events, "E224"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.Equal(t, 2, fault.Matches())
	assert.True(t, r.IsTagged("core@0.1.0"))
	assert.Empty(t, r.Git("-C", remote, "tag", "--list", "core@0.1.0"))

	repairFinalRecovery(t, r)
}

// TestPushRecoveryReportsALateGitHubRecordFailure keeps the durable Git
// outcome separate from its secondary release metadata. The branch moves
// during publication, recovery merges and pushes the exact tagged release,
// and only then GitHub refuses the record. The command must report the missing
// record while preserving the published tag and avoiding a second upload.
func TestPushRecoveryReportsALateGitHubRecordFailure(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		if req.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, "metadata unavailable", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := harness.New(t)
	remote := r.AddBareRemote()
	cfg := libsConfig(midReleasePush(t, remote, "docs: landed while releasing", "REMOTE.md", "remote\n"), 1)
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.GitHub = &models.GitHubConfig{Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN"}
	cfg.Scripts["publish"] = models.Script{"printf 'published\\n' >> ../../publish-count"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): publish before recovering the remote")
	r.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	t.Setenv("DISPAT_IT_TOKEN", "token")

	failed := r.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.True(t, harness.IsCodePresent(failed.Events, "E222"), "stdout:\n%s", failed.Stdout)
	assert.Contains(t, failed.Stdout, `"status":"published"`)
	assert.EqualValues(t, 1, posts.Load())
	assert.True(t, r.IsTagged("core@0.1.0"))
	release := r.Git("rev-parse", "core@0.1.0^{}")
	assert.Equal(t, release, r.Git("-C", remote, "rev-parse", "core@0.1.0^{}"))
	assert.NotEqual(t, release, r.Git("-C", remote, "rev-parse", "refs/heads/"+harness.DefaultBranch),
		"the recovered branch ends at its merge while the tag keeps the release commit")

	retry := r.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	published, err := os.ReadFile(r.Path("publish-count"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(published), "published\n"), "the durable tag prevents a second upload")
	assert.EqualValues(t, 1, posts.Load(), "a no-op retry does not invent another GitHub record attempt")
}
