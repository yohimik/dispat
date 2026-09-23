// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A GitHub API failure happens after native source records and the fleet
// checkpoint. Retrying release must not publish the package a second time.
func TestPolyrepoGitHubFailureKeepsNativeRecordForAPIRepair(t *testing.T) {
	var posts atomic.Int32
	var isRejected atomic.Bool
	isRejected.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if githubTagProbe(w, req, nil) {
			return
		}
		if req.Method == http.MethodPost {
			posts.Add(1)
			if isRejected.Load() {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"core@0.1.0"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	fleet := finalPolyrepo(t)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"},
		"publish": []string{"echo published >> release.txt"},
	}
	cfg["commit"] = map[string]any{"enabled": true}
	cfg["github"] = map[string]any{
		"enabled": true, "allPackages": true,
		"owner": "acme", "repo": "mono", "apiUrl": srv.URL,
	}
	writePolyrepoJSON(t, fleet.control, "dispat.json", cfg)
	fleet.control.Commit("chore: configure fleet recorders")
	t.Setenv("GITHUB_TOKEN", "tkn")
	controlBefore := fleet.control.Git("rev-parse", "HEAD")

	failed := fleet.control.Release()
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Equal(t, int32(1), posts.Load(), "one API create was rejected")
	assert.Contains(t, polyrepoTags(fleet.control, "sources/lib"), "core@0.1.0")
	tagTarget := fleet.control.Git("-C", "sources/lib", "rev-parse", "core@0.1.0^{commit}")
	recordPath := fleet.control.Path("sources", "lib", "packages", "core", "release.txt")
	record, readErr := os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.Equal(t, "published\n", string(record), "one native publish wrote the source marker")
	assert.NotEqual(t, controlBefore, fleet.control.Git("rev-parse", "HEAD"),
		"the control checkpoint records the committed source revision")
	assert.Empty(t, fleet.control.TagList(), "the release tag is source-owned")

	retry := fleet.control.Release()
	require.Zero(t, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, int32(1), posts.Load(), "release retry does not recreate a native or GitHub record")
	assert.Equal(t, 1, len(polyrepoTags(fleet.control, "sources/lib")), "source tag remains immutable")
	assert.Equal(t, tagTarget, fleet.control.Git("-C", "sources/lib", "rev-parse", "core@0.1.0^{commit}"))
	record, readErr = os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.Equal(t, "published\n", string(record), "release retry did not publish twice")

	isRejected.Store(false)
	repaired := fleet.control.CommandEnv(runEnvFor("core", "0.1.0", "core@0.1.0"),
		"github", "--package", "core", "--since", "all")
	require.Zero(t, repaired.Code, "stdout:\n%s\nstderr:\n%s", repaired.Stdout, repaired.Stderr)
	assert.Equal(t, int32(2), posts.Load(), "GitHub record can be repaired without repeating native publication")
	assert.Equal(t, tagTarget, fleet.control.Git("-C", "sources/lib", "rev-parse", "core@0.1.0^{commit}"))
	record, readErr = os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.Equal(t, "published\n", string(record), "metadata repair did not rerun the publisher")
}
