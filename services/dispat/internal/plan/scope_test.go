// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestScopeSetExpandsInclusionsBeforeExclusions: whatever order the terms are
// written in, every inclusion is expanded before any exclusion, so the empty
// globs are reported inclusions first (§6.1, §17.2).
func TestScopeSetExpandsInclusionsBeforeExclusions(t *testing.T) {
	git := newFakeGit(commit{sha: "c1", message: "feat(-x-*,y-*): nothing matches"}).tag("core", "1.0.0", "")
	p := compute(t, git, nil)
	var globs []string
	for _, d := range p.Diagnostics {
		if d.Code == CodeEmptyGlob {
			globs = append(globs, d.Message)
		}
	}
	require.Len(t, globs, 2, "%v", codes(p))
	assert.True(t, strings.Contains(globs[0], "y-*") && strings.Contains(globs[1], "x-*"), "%v", globs)
}

// TestScopeSetResolutionAllocatesOnlyItsResult pins a written scope-set's
// resolution at the resolved set itself: no filtered copy of its terms and no
// exclusion set beside it.
func TestScopeSetResolutionAllocatesOnlyItsResult(t *testing.T) {
	cp := &computation{log: zerolog.Nop(),
		pkgs:   []*model.Package{{Name: "core"}, {Name: "utils"}},
		byName: map[string]*model.Package{}, byFold: map[string]string{}}
	for _, p := range cp.pkgs {
		cp.byName[p.Name], cp.byFold[p.Name] = p, p.Name
	}
	scopes := ccme.ScopeSet{{Raw: "core", Name: "core"}, {Raw: "utils", Name: "utils"},
		{Raw: "-utils", Name: "utils", Exclude: true}}
	rec := &commitRec{}
	require.Equal(t, map[string]bool{"core": true}, cp.resolveScopeSet(scopes, true, rec).packages)
	allocations := testing.AllocsPerRun(100, func() { cp.resolveScopeSet(scopes, true, rec) })
	assert.LessOrEqual(t, allocations, 2.0, "the resolved map and its first group")
}
