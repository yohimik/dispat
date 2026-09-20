// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a release's Updates say about a provider that is not releasing.
//
// The record reaches the changelog's dependencies section, the GitHub release
// body, DISPAT_UPDATED_* and DISPAT_DEPENDENCIES, so the version it names is
// the version every reader and every scripted version stage picks up. A
// provider dispat is not publishing this run has exactly one such version,
// and it is the one already in the registry.

func TestAHeldProviderCarriesItsPublishedVersion(t *testing.T) {
	// Run 1 released core@1.0.0 with a feature that reaches app, and app's
	// leg failed. An operator then decides core's later fix is not going out
	// yet and holds it. app still owes the catch-up, and what it picks up is
	// core@1.0.0: the withheld 1.0.1 will carry no tag, so naming it would
	// leave a dependency line, a release body and a version script pointing
	// at a version that does not exist.
	git := newFakeGit(
		commit{sha: "c0", message: "chore: baseline"},
		commit{sha: "c1", message: "feat(core)^: a feature the consumers need"},
		commit{sha: "c2", message: "fix(core): a later fix\n\n---\n\n" +
			"release(core): not this run\n\nRelease-As: none\n"},
	).tag("core", "0.9.0", "c0").tag("core", "1.0.0", "c1").tag("app", "0.1.0", "c0")

	p := compute(t, git, nil)

	core := p.Releases["core"]
	require.True(t, core.Held, "core is held")
	assertVersion(t, v(1, 0, 1), core.Next, "W154 still reports the version the hold withholds")

	app := p.Releases["app"]
	require.True(t, app.IsReleasing(), "app owes the catch-up from the published feature")
	require.Len(t, app.Updates, 1)
	update := app.Updates[0]
	assert.Equal(t, "core", update.Name)
	assert.Equal(t, "1.0.0", update.To.String(), "the version core actually carries at the end of the run")
	assert.Equal(t, "0.9.0", update.From.String(), "what app last shipped against, off the tags")
	assert.Equal(t, "core@1.0.0", update.Tag, "the tag link a reader follows")
}

func TestAReleasingProviderStillCarriesItsNextVersion(t *testing.T) {
	// The control: nothing changes for a provider this run publishes.
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core)^: a feature the consumers need"},
	).tag("core", "1.0.0", "").tag("app", "0.1.0", "")

	p := compute(t, git, nil)

	app := p.Releases["app"]
	require.True(t, app.IsReleasing())
	require.Len(t, app.Updates, 1)
	assert.Equal(t, "1.1.0", app.Updates[0].To.String())
	assert.Equal(t, "1.0.0", app.Updates[0].From.String())
}
