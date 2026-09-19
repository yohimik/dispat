// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: what a step command makes of the run environment it
// was handed.
//
// Goal 37 owns the steps wired into a real release. This file drives the same
// wiring directly, by handing a step command the DISPAT_* environment a stage
// script inherits, because a run cannot easily be made to produce the
// environments that matter here: a version that does not parse, a listing
// naming an update whose variables are not there, a pin the step's own plan
// cannot reach. Each of those is a record about to be written wrong, so each
// one either corrects itself out loud (W228) or refuses with nothing written
// (E219).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// runEnvFor renders the environment a stage script of a release inherits,
// for a run releasing pkg at version under tag.
func runEnvFor(pkg, version, tag string, extra ...string) []string {
	return append([]string{
		"DISPAT_PACKAGE=" + pkg,
		"DISPAT_NEW_VERSION=" + version,
		"DISPAT_TAG=" + tag,
	}, extra...)
}

// TestCovStepRefusesARunEnvironmentItCannotHonor: a step invoked inside a run
// is held to that run's answers. An environment it cannot read, and a plan it
// cannot align to the environment, both stop the step before anything is
// written.
func TestCovStepRefusesARunEnvironmentItCannotHonor(t *testing.T) {
	t.Run("a pinned version that does not parse", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		res := r.CommandEnv(runEnvFor("core", "one point oh", "core@0.1.0"), "changelog")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "does not parse")
		assert.Equal(t, "", changelogOf(t, r, "core"), "nothing was written")
	})

	t.Run("a listing naming an update it does not describe", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		res := r.CommandEnv(runEnvFor("core", "0.1.0", "core@0.1.0",
			"DISPAT_UPDATED_PACKAGES=UTILS"), "changelog", "--log-format", "json")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, diagnosticText(res), "do not describe an update")
		assert.True(t, harness.IsCodePresent(res.Events, "E219"), "stdout:\n%s", res.Stdout)
		assert.Equal(t, "", changelogOf(t, r, "core"), "nothing was written")
	})

	t.Run("a package the step's own plan does not release", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")
		r.ReleaseOK()
		require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
		before := changelogOf(t, r, "core")

		res := r.CommandEnv(runEnvFor("core", "0.2.0", "core@0.2.0"), "changelog", "--log-format", "json")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "E219", "core"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, diagnosticText(res), "the step's own plan does not")
		assert.Equal(t, before, changelogOf(t, r, "core"), "the existing record is untouched")
	})

	t.Run("a tag the aligned version does not render", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		res := r.CommandEnv(runEnvFor("core", "0.2.0", "core-v0.2.0"), "changelog", "--log-format", "json")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "E219", "core"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, diagnosticText(res), "renders tag")
		assert.Equal(t, "", changelogOf(t, r, "core"), "nothing was written")
	})
}

// TestCovStepAlignsItsPlanToTheRunAndSaysSo: where the step's own replan can
// be corrected it is corrected rather than refused, and the correction is
// reported: the run's version and its provider movements are the authority,
// and the record written states them.
func TestCovStepAlignsItsPlanToTheRunAndSaysSo(t *testing.T) {
	t.Run("a version the run decided", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		res := r.CommandEnv(runEnvFor("core", "0.2.0", "core@0.2.0"), "changelog", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W228", "core"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, changelogOf(t, r, "core"), "## core@0.2.0 (",
			"the entry is written at the run's version")
	})

	t.Run("provider movements the run listed", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		res := r.CommandEnv(runEnvFor("core", "0.1.0", "core@0.1.0",
			"DISPAT_UPDATED_PACKAGES=SHARED",
			"DISPAT_UPDATED_SHARED_NAME=shared",
			"DISPAT_UPDATED_SHARED_OLD_VERSION=1.0.0",
			"DISPAT_UPDATED_SHARED_NEW_VERSION=1.1.0",
			"DISPAT_UPDATED_SHARED_TAG=shared@1.1.0",
		), "changelog", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.True(t, harness.IsCodePresentForPackage(res.Events, "W228", "core"), "stdout:\n%s", res.Stdout)
		assert.Contains(t, changelogOf(t, r, "core"), "shared: 1.0.0 -> 1.1.0",
			"the record states the movement the run made")
	})

	t.Run("a workspace listing whose entries do not all resolve", func(t *testing.T) {
		r := singlePackageRepo(t, echoBuild)
		r.Commit("feat(core): first feature")

		// The listing only feeds tag masking, so an entry naming a package
		// this workspace does not have, and one whose version does not parse,
		// are skipped rather than refused.
		res := r.CommandEnv(runEnvFor("core", "0.1.0", "core@0.1.0",
			"DISPAT_WORKSPACE_PACKAGES=CORE GHOST BROKEN",
			"DISPAT_WORKSPACE_CORE_NAME=core",
			"DISPAT_WORKSPACE_CORE_VERSION=0.1.0",
			"DISPAT_WORKSPACE_CORE_RELEASING=true",
			"DISPAT_WORKSPACE_GHOST_NAME=ghost",
			"DISPAT_WORKSPACE_GHOST_VERSION=1.0.0",
			"DISPAT_WORKSPACE_GHOST_RELEASING=true",
			"DISPAT_WORKSPACE_BROKEN_NAME=broken",
			"DISPAT_WORKSPACE_BROKEN_VERSION=not a version",
			"DISPAT_WORKSPACE_BROKEN_RELEASING=true",
		), "changelog", "--log-format", "json")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, changelogOf(t, r, "core"), "## core@0.1.0 (")
	})
}
