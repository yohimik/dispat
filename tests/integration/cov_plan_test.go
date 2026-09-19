// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the planner: the shared window scan attribution pays
// for itself with, the same sharing across a composed fleet, the correction
// diagnostics that have no package to name, and the scope terms a commit
// message may be written with.

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covReadFile reads a file a run wrote, failing the test when it is not there.
func covReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// covWindowSharingCommits is comfortably past the union length from which the
// planner stops scanning a window per package and starts identifying it. Below
// that length the identity costs more than the scan it replaces, so a fixture
// that wants the sharing has to be long enough to earn it.
const covWindowSharingCommits = 20

// TestPlanAuthorsShareOneWindowAcrossPackages: two packages released at the
// same boundaries ask the same question of every commit, so the attribution is
// computed once and shared. The answer has to be the same one an unshared scan
// would have given, which is what this asserts: both records name both people,
// in commit order, over a history long enough for the sharing to engage.
func TestPlanAuthorsShareOneWindowAcrossPackages(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(authorsConfig(&models.AuthorsConfig{Placement: "section"}))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	for i := range covWindowSharingCommits {
		name, mail := adaName, adaMail
		if i%2 == 1 {
			name, mail = graceMsg, graceMl
		}
		r.WriteFile(fmt.Sprintf("packages/core/change-%d.txt", i), "work\n")
		r.WriteFile(fmt.Sprintf("packages/utils/change-%d.txt", i), "work\n")
		r.CommitAs(name, mail, fmt.Sprintf("feat(core,utils): change %d", i))
	}

	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.IsTagged("utils@0.1.0"), "tags: %v", r.TagList())

	for _, pkg := range []string{"core", "utils"} {
		entry := changelogOf(t, r, pkg)
		assert.Contains(t, entry, "### Authors", "%s carries the section", pkg)
		assert.Contains(t, entry, adaName, "%s names the first author", pkg)
		assert.Contains(t, entry, graceMsg, "%s names the second", pkg)
		assert.Equal(t, 1, strings.Count(entry, adaName),
			"%s names each person once however many commits they wrote", pkg)
	}
}

// TestPlanAuthorsAcrossAComposedFleet: the same sharing, where a window is not
// one boundary but one per repository the package's history was attached from.
// The identity has to name every one of them, so that two packages whose
// windows differ only in a source repository's boundary are not given each
// other's authors.
func TestPlanAuthorsAcrossAComposedFleet(t *testing.T) {
	libSource := harness.New(t)
	libSource.SeedPackage("packages", "lib")
	libSource.Commit("feat(lib): bootstrap library")

	appSource := harness.New(t)
	appSource.SeedPackage("packages", "app")
	appSource.Commit("feat(app): bootstrap application")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", libSource)
	addPolyrepoSource(t, control, "app-source", "sources/app", appSource)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libraries":    "sources/lib/packages",
		"applications": "sources/app/packages",
	})
	cfg["changelog"] = map[string]any{
		"enabled": true,
		"authors": map[string]any{"placement": "section"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble the control repository")

	// Each source's own history, long enough that the planner identifies the
	// window instead of rescanning it per package.
	for i := range covWindowSharingCommits {
		name, mail := adaName, adaMail
		if i%2 == 1 {
			name, mail = graceMsg, graceMl
		}
		for _, src := range []struct{ path, pkg string }{
			{"sources/lib", "lib"}, {"sources/app", "app"},
		} {
			control.WriteFile(fmt.Sprintf("%s/packages/%s/change-%d.txt", src.path, src.pkg, i), "work\n")
			control.Git("-C", src.path, "add", "-A")
			control.Git("-C", src.path, "-c", "user.name="+name, "-c", "user.email="+mail,
				"commit", "-q", "-m", fmt.Sprintf("feat(%s): change %d", src.pkg, i))
		}
	}
	control.Git("add", "sources")
	control.Commit("chore: update the source pointers")

	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "lib@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/app"), "app@0.1.0")

	for _, src := range []struct{ path, pkg string }{
		{"sources/lib", "lib"}, {"sources/app", "app"},
	} {
		data := covReadFile(t, control.Path(src.path, "packages", src.pkg, "CHANGELOG.md"))
		assert.Contains(t, data, "### Authors", "%s carries the section", src.pkg)
		assert.Contains(t, data, adaName)
		assert.Contains(t, data, graceMsg)
	}
}

// TestPlanCorrectionDiagnosticsNameWhatTheyCouldNotReach: a correction whose
// targets all left the pending window addresses no package at all, so there is
// no package to report it against — and reporting it anyway, against nothing,
// is the whole point of the no-op diagnostic being the one a package may not
// suppress. A correction that does reach its target says which targets it
// resolved, where a reader looking for that can find it.
func TestPlanCorrectionDiagnosticsNameWhatTheyCouldNotReach(t *testing.T) {
	t.Run("a correction that reaches no package", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		r.ReleaseOK()
		shipped := r.Git("rev-parse", "HEAD")
		require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		r.Commit("chore(release): record the changelog")

		// No scope set of its own, and the only target is history: nothing
		// tells the correction which packages it was ever about.
		r.WriteFile("packages/core/more.txt", "work of its own\n")
		r.Commit("feat(core): work that does release")
		r.CommitEmpty("fix: restate what already shipped\n\nEdits: " + shipped)

		res := r.ReleaseOK()
		require.True(t, harness.IsCodePresent(res.Events, "W209"), "stdout:\n%s", res.Stdout)
		var reported harness.Event
		for _, e := range res.Events {
			if e.Code() == "W209" {
				reported = e
				break
			}
		}
		assert.Empty(t, reported.Package(), "there is no package the correction reached")
		assert.Contains(t, reported.Str("message"), shipped[:7],
			"and the target it looked for is named back")
		assert.True(t, r.IsTagged("core@0.2.0"),
			"the carrying commit's own work still releases; tags: %v", r.TagList())
	})

	t.Run("a correction naming a commit nobody has", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		// A well-formed object id that resolves to nothing: a sha copied out
		// of another clone, or one whose commit was never pushed here.
		r.CommitEmpty("fix(core): restate something\n\nEdits: " +
			"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

		res := r.Status()
		assert.True(t, harness.IsCodePresent(res.Events, "E210"),
			"the unresolvable target is reported rather than silently dropped: %s", res.Stdout)
		assert.Contains(t, res.Stdout, "deadbeef", "and the target is named back")
	})

	t.Run("a resolved correction names its targets", func(t *testing.T) {
		r := correctionsRepo(t)
		r.Commit("feat(core): bootstrap\n\n---\n\nfeat(utils): bootstrap")
		r.ReleaseOK()
		r.Commit("chore(release): record the changelog")

		r.WriteFile("packages/core/main.txt", "a defensive fix, not a rewrite\n")
		r.Commit("feat(core)!: rewrite internals")
		mistake := r.Git("rev-parse", "HEAD")
		r.CommitEmpty("fix(core): rewrite internals\n\nEdits: " + mistake)

		res := r.StatusOK("--log-level", "trace")
		assert.Contains(t, res.Stdout, "correction resolved")
		assert.Contains(t, res.Stdout, mistake, "the target is named as it was written")
	})
}

// TestPlanScopeTermsReachTheirPackages: every shape a scope term may take, in
// one history. A glob reaches the packages it matches and says so when it
// matches none; "." is the packages the commit's own files are in; "*" is the
// workspace; and an exclusion naming nothing is a warning where an inclusion
// naming nothing is an error, because a typo in an include silently drops a
// release and a stale exclusion is what a refactor leaves behind.
func TestPlanScopeTermsReachTheirPackages(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "coreutils")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,coreutils,web): bootstrap every package")
	r.ReleaseOK()
	r.Commit("chore(release): record the changelog")

	r.WriteFile("packages/web/own.txt", "derived from the files\n")
	r.Commit("fix(.): the packages this commit's own files are in")
	r.CommitEmpty("fix(core*): a glob over two package names")
	r.CommitEmpty("fix(nothing*): a glob nothing answers to")
	r.CommitEmpty("fix(*,-ghost): the workspace, less a package that is not there")
	r.CommitEmpty("fix(ghost): an include naming no package")

	res := r.Status()
	assert.True(t, harness.IsCodePresent(res.Events, "W134"),
		"the glob that matched nothing is reported: %s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "W130"),
		"an exclusion naming no package is a warning: %s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "E130"),
		"an inclusion naming no package is an error: %s", res.Stdout)

	for _, pkg := range []string{"core", "coreutils", "web"} {
		line := harness.GraphLine(res.Events, pkg)
		assert.Equal(t, "0.1.0 -> 0.1.1", line.Str("version"),
			"%s was reached by the terms above: %s", pkg, line.Str("message"))
	}
}
