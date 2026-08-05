package integration

// Shared fixtures. Only shapes used by more than one test file (or more
// than one scenario within a file) live here; a config exercised by exactly
// one test stays next to that test, written out in full, because the config
// *is* the test input and hiding it behind a builder would obscure what is
// being exercised.

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// packageNames returns [prefix0, prefix1, ...] — the package set of the
// budget-style concurrency scenarios.
func packageNames(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// seedIndependentPackages creates every named package under packages/ and
// commits them all with one multi-scope feat, so they all release in the
// same run with no dependency edges among them.
func seedIndependentPackages(r *harness.Repo, names []string) {
	scope := ""
	for i, n := range names {
		r.SeedPackage("packages", n)
		if i > 0 {
			scope += ","
		}
		scope += n
	}
	r.Commit(fmt.Sprintf("feat(%s): bootstrap %d independent packages", scope, len(names)))
}

// singlePackageRepo returns a repository with one "core" package under a
// one-space config running the given build script (working directory:
// packages/core). Nothing is committed yet: each scenario stages its own
// history.
func singlePackageRepo(t *testing.T, buildScript string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {"build": %q, "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  %s
}`, buildScript, harness.Base("1")))
	r.SeedPackage("packages", "core")
	return r
}

// linkedRepo returns a repository with two packages in one space and a
// consumer -> provider dependency edge between them, both stages running
// the given build script. Nothing is committed yet.
func linkedRepo(t *testing.T, provider, consumer, buildScript string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfig(fmt.Sprintf(`{
  "scripts": {"build": %q, "publish": "echo publishing"},
  "spaces": {"libs": {"path": "packages", "run": {"build": "build", "publish": "publish"}}},
  "dependencies": [{"consumer": %q, "provider": %q}],
  %s
}`, buildScript, consumer, provider, harness.Base("1")))
	r.SeedPackage("packages", provider)
	r.SeedPackage("packages", consumer)
	return r
}

// echoBuild is the inert build script of scenarios that assert on plan
// outcomes rather than on script execution.
const echoBuild = "echo building"

// markerBuild is the build script of scenarios that assert scripts ran — or
// did not run — according to the plan: each execution appends one line to
// build.log in the monorepo root (scripts run inside packages/<name>, two
// levels down). failIfMarker instead fails whenever a FAIL file exists in
// the package folder — the untracked marker the failure scenarios plant and
// later lift, without needing a commit either way.
const (
	markerBuild  = "echo ran >> ../../build.log"
	failIfMarker = "[ ! -f FAIL ]"
)

// buildRuns returns how many times markerBuild has executed: the line count
// of build.log, zero when no build script has run at all.
func buildRuns(r *harness.Repo) int {
	data, err := os.ReadFile(r.Path("build.log"))
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}
