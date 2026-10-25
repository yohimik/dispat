// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// countingAncestryGit counts the ancestry questions that reach the Git
// implementation.
type countingAncestryGit struct {
	*mergedGit
	asked int
}

func (g *countingAncestryGit) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	g.asked++
	return g.mergedGit.IsAncestor(ctx, a, b)
}

// linearMergedGit is a linear history of the given length served with the
// union walk, commit i being "c<i>".
func linearMergedGit(commits int) *mergedGit {
	git := &mergedGit{fakeGit: newFakeGit(), at: make(map[string]int), behind: make(map[int][]bool)}
	for i := 0; i < commits; i++ {
		sha := fmt.Sprintf("c%02d", i)
		git.history = append(git.history, commit{sha: sha, message: fmt.Sprintf("fix(core): change %d", i),
			files: []string{"core/main.go"}})
		var parents []int
		if i > 0 {
			parents = []int{i - 1}
		}
		git.parents = append(git.parents, parents)
		git.at[sha] = i
	}
	return git
}

// TestComposedOwnTagsBehindTheUnionAskNoGit: in a composed workspace a
// package's own release tag the union does not hold is behind every commit
// of it, as in one history, so the owed-window decision asks git nothing.
// app released at c10, behind the union that starts there; core's newer
// release at c30 is in the union, and whether it reaches app's baseline used
// to be a git question.
func TestComposedOwnTagsBehindTheUnionAskNoGit(t *testing.T) {
	plan := func(isComposed bool) (*Plan, int) {
		git := linearMergedGit(40)
		git.tag("core", "1.0.0", "c05").tag("core", "1.1.0", "c30").tag("app", "1.0.0", "c10")
		counting := &countingAncestryGit{mergedGit: git}
		pkgs := []*model.Package{{Name: "core", Dir: "/r/core"}, {Name: "app", Dir: "/r/app"}}
		opts := Options{Packages: pkgs, Root: "/r",
			Dependencies: []model.Dependency{{Consumer: "app", Provider: "core"}}}
		if isComposed {
			for _, p := range pkgs {
				p.Repository, p.RepoRoot = "source", "/r"
			}
			opts.Repositories = map[string]RepositoryHistory{"source": {Name: "source", Root: "/r", Git: counting}}
		}
		p, err := Compute(context.Background(), counting, opts)
		require.NoError(t, err)
		return p, counting.asked
	}
	single, askedSingle := plan(false)
	composed, askedComposed := plan(true)
	require.Zero(t, askedSingle)
	require.Zero(t, askedComposed, "a release in the union against a tag behind it is the index's answer")
	require.Equal(t, single.Order, composed.Order)
	require.Equal(t, canonical(reflect.ValueOf(single.Releases["app"].Next)),
		canonical(reflect.ValueOf(composed.Releases["app"].Next)))
}
