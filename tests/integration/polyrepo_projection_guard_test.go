// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPolyrepoStatusRejectsFutureControlIntentForOlderSourceCheckout builds the
// one shape that reaches the projection guard without any synchronization
// feature: the control repository rewinds a gitlink it had already advanced.
//
// The intent commit stays in the pending window and its own snapshot pins a
// source revision the checkout no longer contains, while control HEAD pins
// exactly what is checked out — so composition admits the fleet and the plan,
// not the snapshot, is where the contradiction has to be caught.
func TestPolyrepoStatusRejectsFutureControlIntentForOlderSourceCheckout(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "app")
	source.Commit("chore: baseline app")

	control := harness.New(t)
	addPolyrepoSource(t, control, "app-source", "sources/app", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"apps": "sources/app/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")

	// A released revision with its ordinary control checkpoint, so the
	// consumer boundary is evidence rather than the thing under test.
	control.WriteFile("sources/app/packages/app/main.txt", "released app\n")
	released := commitPolyrepoSource(t, control, "sources/app", "feat(app): first release")
	control.Git("-C", "sources/app", "tag", "app@1.0.0")
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore(release): app@1.0.0")

	// Intent addressed to the source, pinned at a revision beyond the release.
	control.WriteFile("sources/app/packages/app/main.txt", "advanced app\n")
	control.Git("-C", "sources/app", "add", "-A")
	control.Git("-C", "sources/app", "commit", "-q", "-m", "chore: advance app")
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "fix(app): control intent pinned ahead")

	// The rewind: control HEAD pins the released revision again, and the
	// checkout follows it. Composition is satisfied; the intent above is not.
	control.Git("-C", "sources/app", "checkout", "-q", "--detach", released)
	control.Git("add", "sources/app")
	control.Git("commit", "-q", "-m", "chore: rewind app pointer")

	status := control.Status()
	assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.True(t, harness.IsCodePresent(status.Events, "E333"), "events: %#v", status.Events)
	var projection harness.Event
	for _, event := range status.Events {
		if event.Code() == "E333" {
			projection = event
		}
	}
	assert.Contains(t, projection.Str("message"), "control directive at",
		"the plan-level projection guard must be the diagnostic, not a boundary lookup")
	assert.Contains(t, projection.Str("message"), "app-source")
	assert.Equal(t, []string{"app@1.0.0"}, polyrepoTags(control, "sources/app"),
		"a refused plan adds no release tag")
}

// TestPolyrepoStatusRejectsControlIntentPinningAnUnfetchedSourceRevision is the
// same refusal when the pinned revision is not merely newer than the checkout
// but absent from it, which is the ordinary shape of the problem: the gitlink
// lives in the control tree, and a source clone is under no obligation to hold
// every revision control ever pointed at.
//
// git answers an ancestry question about an object it does not have with a
// fatal error, so the guard has to establish presence before reachability.
// Without that the operator is shown a `merge-base --is-ancestor` exit status
// in place of the diagnostic naming the repository, the pin and the recovery.
func TestPolyrepoStatusRejectsControlIntentPinningAnUnfetchedSourceRevision(t *testing.T) {
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

	// The advanced revision is created in a separate clone and never fetched
	// back, so the object exists to be pinned and the fleet checkout does not
	// have it. Only the control index is moved onto it: staging the submodule
	// from the working tree would re-pin what is checked out.
	scratch := filepath.Join(t.TempDir(), "elsewhere")
	control.Git("clone", "-q", control.Path("sources/app"), scratch)
	control.Git("-C", scratch, "config", "user.email", "integration@dispat.test")
	control.Git("-C", scratch, "config", "user.name", "dispat integration")
	require.NoError(t, os.WriteFile(filepath.Join(scratch, "packages", "app", "main.txt"), []byte("advanced app\n"), 0o644))
	control.Git("-C", scratch, "commit", "-q", "-a", "-m", "chore: advance app")
	unfetched := control.Git("-C", scratch, "rev-parse", "HEAD")

	control.Git("update-index", "--cacheinfo", "160000,"+unfetched+",sources/app")
	control.Git("commit", "-q", "-m", "fix(app): control intent pinned ahead")
	control.Git("update-index", "--cacheinfo", "160000,"+released+",sources/app")
	control.Git("commit", "-q", "-m", "chore: rewind app pointer")

	require.Equal(t, released, control.Git("-C", "sources/app", "rev-parse", "HEAD"),
		"the fleet checkout stays on the released revision")
	missing := exec.Command("git", "-C", control.Path("sources/app"), "cat-file", "-e", unfetched)
	require.Error(t, missing.Run(), "precondition: the source clone must not hold the pinned revision")

	status := control.Status()
	assert.NotZero(t, status.Code, "stdout:\n%s\nstderr:\n%s", status.Stdout, status.Stderr)
	assert.True(t, harness.IsCodePresent(status.Events, "E333"), "events: %#v", status.Events)
	var projection harness.Event
	for _, event := range status.Events {
		if event.Code() == "E333" {
			projection = event
		}
	}
	assert.Contains(t, projection.Str("message"), "pins "+unfetched)
	assert.NotContains(t, status.Stderr, "merge-base",
		"an unfetched pin is a fleet condition, not a git command failure")
	assert.Equal(t, []string{"app@1.0.0"}, polyrepoTags(control, "sources/app"),
		"a refused plan adds no release tag")
}
