// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review faults for the control-to-source projection guard.
// A control directive is safe only when the active source contains and
// descends from the revision that directive's control snapshot pinned.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func finalProjectionFleet(t *testing.T) *harness.Repo {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "app")
	source.Commit("chore: baseline app")

	control := harness.New(t)
	addPolyrepoSource(t, control, "app-source", "sources/app", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"apps": "sources/app/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")

	control.WriteFile("sources/app/packages/app/main.txt", "released app\n")
	released := commitPolyrepoSource(t, control, "sources/app", "feat(app): first release")
	control.Git("-C", "sources/app", "tag", "app@1.0.0")
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore(release): app@1.0.0")

	control.WriteFile("sources/app/packages/app/main.txt", "advanced app\n")
	commitPolyrepoSource(t, control, "sources/app", "chore: advance app")
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "fix(app): control intent pinned ahead")

	control.Git("-C", "sources/app", "checkout", "-q", "--detach", released)
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore: rewind app pointer")
	return control
}

// TestFinalPolyrepoProjectionFaultsDoNotBecomeARepositoryBoundaryDiagnostic:
// an absent pin is a normal E333 fleet condition, while inability to ask
// whether it is present or ancestral is an operational failure. The latter
// must retain the Git cause and cannot be reported as an ordinary stale pin.
func TestFinalPolyrepoProjectionFaultsDoNotBecomeARepositoryBoundaryDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, want string
	}{
		{
			name:    "source object presence",
			pattern: "*-C */sources/app rev-parse --quiet --verify *^{commit}*",
			want:    "plan: repository app-source",
		},
		{
			name:    "source snapshot ancestry",
			pattern: "*-C */sources/app merge-base --is-ancestor *",
			want:    "ancestry query failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := finalProjectionFleet(t)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern, Code: 128})

			res := control.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "control directive at",
				"an unreadable object database is not an ordinary projection mismatch")
			assert.NotContains(t, combined, "release plan ready")
			assert.Equal(t, 1, fault.Matches(), "the projection proof inquiry ran once")
			assert.Equal(t, []string{"app@1.0.0"}, polyrepoTags(control, "sources/app"),
				"the failed proof adds no release record")
		})
	}
}
