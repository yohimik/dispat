// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestPublicationInFlightRetainsReleaseExclusion(t *testing.T) {
	repo := harness.New(t)
	repo.SeedPackage("packages", "core")
	destination := t.TempDir()
	queued := filepath.Join(destination, "queued-requests")
	published := filepath.Join(destination, "published")
	verified := filepath.Join(destination, "verification-reads")
	cfg := libsConfig("true", 1)
	cfg.Scripts["publish"] = models.Script{
		"printf 'exact-request\\n' >> " + harness.ShQuote(queued) + "; exit 7",
	}
	cfg.Scripts["verify"] = models.Script{
		"printf 'read\\n' >> " + harness.ShQuote(verified) +
			"; test -f " + harness.ShQuote(published) + " || exit 3",
	}
	cfg.Publish = &models.PublishConfig{AdoptPublished: &models.AdoptPublishedConfig{
		Verify: []string{"verify"},
	}}
	repo.WriteConfigModel(cfg)
	repo.Commit("feat(core): publish through a delayed destination")
	remote := repo.AddBareRemote()
	env := append(append([]string{}, harness.LockEnabled...), executionNodeHomeEnv(t, t.TempDir())...)
	res := repo.StartReleaseEnv(env, "release").WaitForDiagnostic(60*time.Second, func() {
		t.Logf("retained remote refs: %s", bareGit(t, remote, "show-ref"))
	})
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.FileExists(t, queued, "the external destination accepted a request before the command failed")
	require.FileExists(t, verified, "an absent destination read does not fence that pending request")
	require.NoFileExists(t, published)
	require.Contains(t, res.Stdout+res.Stderr, "E228")
	require.True(t, remoteHoldsLock(t, remote), "the pending effect retains release exclusion")
	require.False(t, repo.IsTagged("core@0.1.0"))

	require.NoError(t, os.WriteFile(published, []byte("exact-request\n"), 0o600),
		"the destination can commit the queued request after local cancellation")
	retry := repo.StartReleaseEnv(env, "release").WaitForDiagnostic(60*time.Second, func() {
		t.Logf("retained remote refs: %s", bareGit(t, remote, "show-ref"))
	})
	require.NotZero(t, retry.Code, "a new run cannot bypass unresolved exact-effect reconciliation\n%s", retry.Stdout)
	requests, err := os.ReadFile(queued)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(requests), "exact-request"),
		"a fresh plan cannot submit a conflicting second request")
	require.True(t, remoteHoldsLock(t, remote))
}

func TestUnsettledLaterPublicationRecordsEarlierKnownRelease(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		localCapacity bool
	}{
		{name: "ordinary executor"},
		{name: "compiled local executor", localCapacity: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testUnsettledLaterPublicationRecordsEarlierKnownRelease(t, testCase.localCapacity)
		})
	}
}

func testUnsettledLaterPublicationRecordsEarlierKnownRelease(t *testing.T, localCapacity bool) {
	t.Helper()
	repo := harness.New(t)
	seedIndependentPackages(repo, []string{"alpha", "zeta"})
	remote := repo.AddBareRemote()
	destination := t.TempDir()
	known := filepath.Join(destination, "known")
	queued := filepath.Join(destination, "queued")
	hooks := filepath.Join(destination, "hooks")
	cfg := libsConfig("true", 1)
	if localCapacity {
		cfg.Execution = &models.ExecutionConfig{Concurrency: models.Int(1)}
	}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true}
	cfg.Scripts["publish"] = models.Script{
		"if [ \"$DISPAT_PACKAGE\" = alpha ]; then printf 'published\\n' >> " + harness.ShQuote(known) +
			"; else printf 'queued\\n' >> " + harness.ShQuote(queued) + "; exit 7; fi",
	}
	cfg.Scripts["verify"] = models.Script{"exit 3"}
	cfg.Scripts["hook"] = models.Script{"printf 'ran\\n' >> " + harness.ShQuote(hooks)}
	cfg.Publish = &models.PublishConfig{AdoptPublished: &models.AdoptPublishedConfig{Verify: []string{"verify"}}}
	cfg.Run = &models.RunConfig{PostAll: []string{"hook"}, BeforeCommit: []string{"hook"}}
	repo.WriteConfigModel(cfg)
	repo.Commit("chore: configure uncertain second publication")
	env := append(append([]string{}, harness.LockEnabled...), executionNodeHomeEnv(t, t.TempDir())...)
	res := repo.StartReleaseEnv(env, "release").WaitForDiagnostic(60*time.Second, func() {
		t.Logf("retained remote refs: %s", bareGit(t, remote, "show-ref"))
	})
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	require.FileExists(t, known, "alpha published before zeta's uncertain request")
	require.FileExists(t, queued, "zeta sent an external request before failing")
	require.Contains(t, res.Stdout+res.Stderr, "E228")
	require.True(t, repo.IsTagged("alpha@0.1.0"), "known publication still gets its durable tag")
	require.NotEmpty(t, bareGit(t, remote, "rev-parse", "refs/tags/alpha@0.1.0^{}"),
		"known release tag reaches the remote while the lock remains held")
	require.False(t, repo.IsTagged("zeta@0.1.0"), "uncertain publication has no invented record")
	require.Equal(t, "chore(release): alpha@0.1.0", repo.Git("log", "-1", "--format=%s"),
		"the release commit records only the known publication")
	require.True(t, remoteHoldsLock(t, remote), "known recording does not end release exclusion")
	require.NoFileExists(t, hooks, "unsettled closing must not run postAll or commit hooks")
}
