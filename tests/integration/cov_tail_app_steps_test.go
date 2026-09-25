// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Long-tail coverage for the standalone step commands and the notifications
// around them: the summary a person reads when the log is not machine
// readable, the edits a narrowing flag drops, the answer stream that runs out
// mid-prompt, and the delivery an endpoint refuses in a way no retry would
// change. Each one is the ordinary half of a feature whose exceptional half
// the suite already holds.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covTailPrettyRepo is a workspace whose configured log format is the pretty
// one a person reads, which is what makes the step commands print a summary
// instead of logging one.
func covTailPrettyRepo(t *testing.T) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.LogFormat = "pretty"
	cfg.Spaces["libs"] = covTailAVSpace(&models.AutoVersionConfig{Enabled: models.Bool(true)})
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "workspace:*"}}`)
	r.WriteFile("packages/web/README.md", "core: pinned\n")
	r.Commit("feat(core,web): bootstrap")
	return r
}

// TestCovTailStepCommandsSummariseForAPerson: the step commands are run by
// hand as often as by CI, and a run whose log format is the readable one
// prints its tally on standard output rather than logging it as a JSON line
// nobody asked for. The counts are the same either way; only where they go
// differs.
func TestCovTailStepCommandsSummariseForAPerson(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"the writer sweep": {
			args: []string{"autowriter", "--set-version", "{version}", "--since", "all"},
			want: "applied",
		},
		"the replacer sweep": {
			args: []string{"autoreplacer", "--replace", "core: pinned=>core: {version}",
				"--files", "README.md", "--since", "all"},
			want: "occurrence(s)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := covTailPrettyRepo(t)
			res := r.Command(tc.args...)
			require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout, "package(s):", "the tally is printed, not logged")
			assert.Contains(t, res.Stdout, tc.want)
			// A version the sweep wrote is an applied edit: the summary must
			// not say "0 applied" beside the manifests it changed.
			assert.NotContains(t, res.Stdout, " 0 applied", "stdout:\n%s", res.Stdout)
			assert.Empty(t, res.Events, "a pretty run logs no JSON lines at all")
		})
	}
}

// TestCovTailAutoWriterLeavesTheVersionOfAPackageNobodyVersions: {version}
// resolves to the planned version of the covered package, and a package under
// versioning "none" has none. Writing the zero version instead would put
// "0.0.0" into a manifest nobody versions, so the own-version write is skipped
// and said so while the package's other edits still land.
func TestCovTailAutoWriterLeavesTheVersionOfAPackageNobodyVersions(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["tools"] = models.SpaceConfig{
		Path: models.PathList{"tools"}, Flow: buildPublish(), Versioning: "none",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("tools", "kit")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("tools/kit/package.json", `{"name": "@acme/kit", "version": "0.0.0"}`)
	r.Commit("feat(core,kit): bootstrap")

	res := r.Command("autowriter", "--set-version", "{version}", "--since", "all", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "own-version write skipped: the package has versioning",
		"the skip is said rather than left to the reader to infer from an unchanged file")
	assert.Contains(t, covTailReadFile(t, r, "packages", "core", "package.json"), `"version": "0.1.0"`,
		"the versioned package is still stamped")
	assert.Contains(t, covTailReadFile(t, r, "tools", "kit", "package.json"), `"version": "0.0.0"`,
		"and the unversioned one keeps what it had")
}

// TestCovTailComputeStopsWhenTheAnswersRunOut: --interactive asks per
// suggestion, and a stream that ends is an answer of its own — the remaining
// suggestions stay unapplied and the config is left as it was, rather than the
// command treating end of input as consent or as a failure.
func TestCovTailComputeStopsWhenTheAnswersRunOut(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	r.WriteConfigModel(cfg)
	before := covTailReadFile(t, r, "dispat.json")
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name": "@acme/core", "version": "0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name": "@acme/web", "version": "0.0.0", "dependencies": {"@acme/core": "^0.0.1"}}`)
	r.Commit("feat(core,web): a workspace edge no config declares")

	// No answers at all: standard input is at its end before the first prompt.
	res := r.Command("compute", "--interactive")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "apply?", "the prompt was asked")
	assert.Equal(t, before, covTailReadFile(t, r, "dispat.json"),
		"and a question nobody answered changes nothing")
}

// TestCovTailWorkspaceLogNamesTheFoldersItExcluded: a .dispatexclude takes a
// folder out of a space, which is a silent thing to do to somebody's release
// plan. The folder and the space are said at debug, so the question "why is my
// package not in the plan" has an answer in the log.
func TestCovTailWorkspaceLogNamesTheFoldersItExcluded(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "vendored")
	r.WriteFile("packages/.dispatexclude", "vendored\n")
	r.Commit("feat(core): bootstrap with a folder the space does not own")

	res := r.StatusOK("--log-level", "debug")
	assert.Contains(t, res.Stdout, "package folder excluded by .dispatexclude")
	assert.Contains(t, res.Stdout, "vendored")
	for _, e := range res.Events {
		assert.NotEqual(t, "vendored", e.Package(), "and the folder is in no plan line")
	}
}

// TestCovTailWebhookGivesUpOnAStatusNoRetryWouldChange: a 5xx and a 429 are
// answers a later attempt could outlive, and a 400 is not. Retrying one is
// only a slower way to fail, so the ladder stops at the first non-retryable
// status, the failure is the ordinary W239, and the run is unaffected either
// way.
func TestCovTailWebhookGivesUpOnAStatusNoRetryWouldChange(t *testing.T) {
	sink := newWebhookSink(t, 400)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: sink.srv.URL, Events: []string{"script.*"}}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Command("trigger", "progress", "40", "compiling assets")
	require.Equal(t, 0, res.Code, "a notification may never fail a script; stdout:\n%s", res.Stdout)
	assert.True(t, harness.IsCodePresent(res.Events, "W239"), "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "webhook delivery failed")
	assert.Len(t, sink.all(), 1, "a refusal no retry would change is tried once")
}

// TestCovTailWebhookFormatRendersTheProgressValue: a rendered payload is for
// an endpoint that wants its own shape, and `progress` is the one event
// carrying a number rather than a string. It renders as the number for the
// event that has one and as nothing for every event that does not, so a
// template embedding it stays valid JSON throughout a run.
func TestCovTailWebhookFormatRendersTheProgressValue(t *testing.T) {
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild, models.WebhookConfig{
		URL:    sink.srv.URL,
		Format: `{"event":"{event}","percent":"{progress}","said":"{message}"}`,
	}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	require.Equal(t, 0, r.Command("trigger", "progress", "40", "compiling assets").Code)
	deliveries := sink.all()
	require.Len(t, deliveries, 1)
	body := string(deliveries[0].Body)
	assert.Contains(t, body, `"percent":"40"`, "the one typed event carries its number")
	assert.Contains(t, body, `"said":"compiling assets"`)
	assert.Contains(t, body, `"event":"script.progress"`)

	// An event with no progress renders the same template with nothing in
	// that position rather than with a zero somebody would read as a value.
	require.Equal(t, 0, r.Command("trigger", "deployed", "version is live").Code)
	deliveries = sink.all()
	require.Len(t, deliveries, 2)
	assert.Contains(t, string(deliveries[1].Body), `"percent":""`)
}
