// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A source selected for push must establish that its remote is reachable.
// A failed read refuses the whole fleet before publish; a healthy read on retry
// leaves the unchanged source work available for one release.
func TestFleetSourceRemoteReadFailureStopsBeforePublicationAndCanRetry(t *testing.T) {
	fleet := finalPolyrepo(t)
	bare := filepath.Join(t.TempDir(), "source.git")
	fleet.control.Git("init", "-q", "--bare", bare)
	fleet.control.Git("-C", "sources/lib", "remote", "set-url", "origin", bare)
	fleet.control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/main")
	marker := fleet.control.Path("published.txt")
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo published >> " + harness.ShQuote(marker)},
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{
			"enabled": true, "push": true, "remote": "origin", "branch": "main",
		}},
	}
	writePolyrepoJSON(t, fleet.control, "dispat.json", cfg)
	fleet.control.Commit("chore: require source remote preflight")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib *ls-remote --heads origin*", Code: 128,
	})

	failed := fleet.control.CommandEnv(fault.Env(), "release")
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Equal(t, 1, fault.Matches(), "the source remote proof was attempted")
	assert.Empty(t, polyrepoTags(fleet.control, "sources/lib"))
	assert.NoFileExists(t, marker, "publisher did not run without the remote proof")

	// Git answers again on retry. The exact same push policy and source work
	// must now finish, without loosening any release requirement.
	retried := fleet.control.Release()
	require.Zero(t, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.Contains(t, polyrepoTags(fleet.control, "sources/lib"), "core@0.1.0")
	assert.Contains(t, fleet.control.Git("-C", bare, "tag", "--list"), "core@0.1.0")
	assert.Equal(t,
		fleet.control.Git("-C", "sources/lib", "rev-parse", "core@0.1.0^{commit}"),
		fleet.control.Git("-C", bare, "rev-parse", "core@0.1.0^{commit}"))
	publication, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "published\n", string(publication))
}
