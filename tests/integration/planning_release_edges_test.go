// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPlanningReleaseEdgesCrossesADependencyAndThenASharedGroup proves that
// repository reachability reaches its fixed point when the two kinds of edge
// alternate twice. Package a first carries its repository input to grouped
// peer b; b carries it over its dependency edge to consumer c; c then carries
// it to grouped peer d.
func TestPlanningReleaseEdgesCrossesADependencyAndThenASharedGroup(t *testing.T) {
	newSource := func(name, message string) *harness.Repo {
		source := harness.New(t)
		source.SeedPackage("packages", name)
		source.Commit(message)
		return source
	}

	control := harness.New(t)
	addPolyrepoSource(t, control, "a-source", "sources/a", newSource("a", "feat(a): provider work"))
	addPolyrepoSource(t, control, "b-source", "sources/b", newSource("b", "chore(b): bootstrap consumer"))
	addPolyrepoSource(t, control, "c-source", "sources/c", newSource("c", "chore(c): bootstrap grouped peer"))
	addPolyrepoSource(t, control, "d-source", "sources/d", newSource("d", "chore(d): bootstrap second peer"))

	cfg := polyrepoFile()
	cfg["versionGroups"] = map[string]any{
		"clients": map[string]any{"versioning": "fixed"},
		"servers": map[string]any{"versioning": "fixed"},
	}
	cfg["spaces"] = map[string]any{
		"a": map[string]any{
			"path":         []string{"sources/a/packages"},
			"versionGroup": "clients",
		},
		"b": map[string]any{
			"path":         []string{"sources/b/packages"},
			"versionGroup": "clients",
		},
		"c": map[string]any{
			"path":         []string{"sources/c/packages"},
			"versionGroup": "servers",
		},
		"d": map[string]any{
			"path":         []string{"sources/d/packages"},
			"versionGroup": "servers",
		},
	}
	cfg["dependencies"] = map[string]any{"c": []any{"b"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: compose provider and grouped clients")

	res := control.StatusOK()
	a := harness.GraphLine(res.Events, "a")
	b := harness.GraphLine(res.Events, "b")
	c := harness.GraphLine(res.Events, "c")
	d := harness.GraphLine(res.Events, "d")
	require.NotEmpty(t, a.Str("version"), "provider was omitted from plan: %s", res.Stdout)
	require.NotEmpty(t, b.Str("version"), "consumer was omitted from plan: %s", res.Stdout)
	require.NotEmpty(t, c.Str("version"), "group peer was omitted from plan: %s", res.Stdout)
	require.NotEmpty(t, d.Str("version"), "second group peer was omitted from plan: %s", res.Stdout)
	assert.Equal(t, "minor", a.Str("bump"), "the provider keeps its direct feature bump")
	assert.Equal(t, a.Str("version"), b.Str("version"),
		"the fixed group aligns the provider and its peer: %s", res.Stdout)
	assert.Contains(t, b.Str("message"), "changed", "the provider's group carries the frontier to its peer")
	assert.Contains(t, c.Str("message"), "changed", "the grouped peer carries the frontier to its dependent")
	assert.Equal(t, c.Str("version"), d.Str("version"), "the second fixed group aligns its members")
	assert.Contains(t, d.Str("message"), "changed", "the dependent carries the frontier to its second group peer")
	assert.Empty(t, control.TagList(), "status only plans; it does not publish")
}
