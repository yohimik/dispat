// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestFinalAuthorCommitPreservesWorkWhenValidationCannotStart(t *testing.T) {
	for _, problem := range []string{"temporary folder", "hooks folder"} {
		t.Run(problem, func(t *testing.T) {
			r := authoringRepo(t)
			r.WriteFile("tracked.txt", "uncommitted work\n")
			head := r.Git("rev-parse", "HEAD")
			index := r.Git("diff", "--cached")
			r.WriteFile("blocked", "preserve me\n")
			var env []string
			if problem == "temporary folder" {
				env = []string{"TMPDIR=" + r.Path("blocked"), "TMP=" + r.Path("blocked"), "TEMP=" + r.Path("blocked")}
			} else {
				r.Git("config", "core.hooksPath", r.Path("blocked"))
			}
			res := r.CommandEnv(env, "commit", "-am", "fix(core): retain work")
			require.NotZero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, "blocked")
			assert.Equal(t, head, r.Git("rev-parse", "HEAD"))
			assert.Equal(t, index, r.Git("diff", "--cached"))
			assert.Equal(t, "uncommitted work\n", readRepoFile(t, r, "tracked.txt"))
			assert.Equal(t, "preserve me\n", readRepoFile(t, r, "blocked"))
		})
	}
}

func TestFinalComputeRejectsBrokenInputBeforeApplyingAcceptedChanges(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	for _, name := range []string{"core", "web"} {
		r.SeedPackage("packages", name)
		r.WriteFile("packages/"+name+"/package.json", `{"name":"`+name+`","version":"1.2.3"}`)
	}
	r.Commit("feat(core,web): existing packages")
	before := readRepoFile(t, r, "dispat.json")
	res := r.CommandInput("yes\n"+strings.Repeat("x", 128*1024)+"\n", "compute", "--interactive")
	require.NotZero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "token too long")
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json"), "an input error must discard earlier accepted suggestions")
	assert.NoFileExists(t, r.Path("dispat.json.backup"))
}

func TestFinalComputeRefusesAnUnavailableBackupAndCanRetry(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name":"core","version":"1.2.3"}`)
	r.Commit("feat(core): existing package")
	before := readRepoFile(t, r, "dispat.json")
	r.WriteFile("dispat.json.backup/keep", "existing backup folder\n")
	res := r.Command("compute", "--write")
	require.NotZero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "writing the config failed")
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json"))
	assert.Equal(t, "existing backup folder\n", readRepoFile(t, r, "dispat.json.backup/keep"))
	require.NoError(t, os.Rename(r.Path("dispat.json.backup"), r.Path("saved-backup")))
	res = r.Command("compute", "--write")
	require.Zero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, before, readRepoFile(t, r, "dispat.json.backup"))
	assert.Contains(t, readRepoFile(t, r, "dispat.json"), `"core": "1.2.3"`)
	assert.Zero(t, r.Command("compute", "--check").Code)
}

func TestFinalStandaloneCommitRetainsItsRecordWhenPinExportFails(t *testing.T) {
	r := singlePackageRepo(t, echoBuild)
	r.Commit("feat(core): publishable work")
	before := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/generated.txt", "release output\n")
	r.WriteFile("blocked-output/keep", "existing folder\n")
	res := r.CommandEnv([]string{"DISPAT_OUTPUT=" + r.Path("blocked-output")}, "commit", "--package", "core", "--tag")
	require.NotZero(t, res.Code, "%s\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "exporting the commit pin failed")
	after := r.Git("rev-parse", "HEAD")
	assert.NotEqual(t, before, after, "the completed release commit remains durable")
	assert.Equal(t, after, r.Git("rev-parse", "core@0.1.0^{commit}"))
	assert.Equal(t, "release output", r.Git("show", "HEAD:packages/core/generated.txt"))
	assert.Equal(t, "existing folder\n", readRepoFile(t, r, "blocked-output/keep"))
}
