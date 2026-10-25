// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReleasesKeepOnlyTheUnitsTheyName: the unit-keyed attribution and
// commits a plan hands its releases hold the units some release names, and
// not every unit the history parsed, which would keep every parsed message of
// the union alive for as long as the plan.
func TestReleasesKeepOnlyTheUnitsTheyName(t *testing.T) {
	git := newFakeGit(
		commit{sha: "c1", message: "feat(core): released already", author: "Ann", email: "ann@example.com"},
		commit{sha: "c2", message: "docs(core): no bump, so in no release", author: "Bo", email: "bo@example.com"},
		commit{sha: "c3", message: "fix(core): pending", author: "Cy", email: "cy@example.com"},
	).tag("core", "1.0.0", "c1").tag("utils", "1.0.0", "").tag("app", "1.0.0", "")
	p := compute(t, git, nil)
	core := p.Releases["core"]
	require.Len(t, core.Units, 1)
	unit := core.Units[0]
	assert.Equal(t, "c3", core.UnitCommit(unit))
	assert.Equal(t, []Author{{Name: "Cy", Email: "cy@example.com"}}, core.AuthorsFor(unit))
	assert.Len(t, core.UnitCommits, 1, "c1 and c2 parsed, and no release names their units")
	assert.Len(t, core.UnitAuthors, 1)
	for _, name := range []string{"utils", "app"} {
		rel := p.Releases[name]
		assert.Equal(t, "c3", rel.UnitCommit(unit), "%s shares the one map", name)
	}
}
