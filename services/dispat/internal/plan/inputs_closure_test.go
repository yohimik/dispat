// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestReleaseRepositoryInputsAgreeWithTheWalk draws workspaces where the
// closure is hard: version groups whose members sit on both sides of a
// dependency edge, so that group and dependency edges close a cycle; groups of
// one; more repositories than one word holds; a control input on some
// packages; and providers the plan does not order, which carry nothing.
func TestReleaseRepositoryInputsAgreeWithTheWalk(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for round := 0; round < 120; round++ {
		repositoryCount := 1 + rng.Intn(6)
		if round%10 == 0 {
			repositoryCount = 70 // two words
		}
		cp := &computation{log: zerolog.Nop(), controlRepo: "r00",
			histories: map[string]RepositoryHistory{}, byName: map[string]*model.Package{},
			providers: map[string][]string{}, repositoryReach: map[string][]string{}, controlInputs: map[string]bool{}}
		var repositories []string
		for i := 0; i < repositoryCount; i++ {
			name := fmt.Sprintf("r%02d", i)
			repositories = append(repositories, name)
			cp.histories[name] = RepositoryHistory{Name: name}
		}
		spaces := []*model.Space{{Name: "solo"}}
		for g := 0; g < 1+rng.Intn(4); g++ {
			spaces = append(spaces, &model.Space{Name: fmt.Sprintf("g%d", g), Versioning: model.VersioningFixed})
		}
		n := 1 + rng.Intn(14)
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("p%02d", i)
			cp.order = append(cp.order, name)
			cp.byName[name] = &model.Package{Name: name, Space: spaces[rng.Intn(len(spaces))]}
			// Providers come earlier in plan order, as a dependency order has it.
			for j := 0; j < i; j++ {
				if rng.Intn(4) == 0 {
					cp.providers[name] = append(cp.providers[name], cp.order[j])
				}
			}
			if rng.Intn(5) == 0 {
				cp.providers[name] = append(cp.providers[name], "not-in-the-plan", cp.order[rng.Intn(i+1)])
			}
			for k := rng.Intn(3); k > 0; k-- {
				cp.repositoryReach[name] = append(cp.repositoryReach[name], strings.ToUpper(repositories[rng.Intn(repositoryCount)]))
			}
			cp.controlInputs[name] = rng.Intn(6) == 0
		}

		wantRepositories, want := releaseRepositoryInputsByWalk(cp)
		gotRepositories, got := cp.releaseRepositoryInputs()
		require.Equal(t, wantRepositories, gotRepositories)
		require.Len(t, got, len(want))
		for _, name := range cp.order {
			assert.Equalf(t, want[name], got[name], "round %d: inputs of %s", round, name)
		}
	}
}

// TestStronglyConnectedEmitsProvidersLast pins the one property the closure
// relies on beyond the components themselves: a component is emitted only
// after every component it has an edge into.
func TestStronglyConnectedEmitsProvidersLast(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for round := 0; round < 200; round++ {
		n := 1 + rng.Intn(12)
		out := make([][]int, n)
		for from := range out {
			for k := rng.Intn(3); k > 0; k-- {
				out[from] = append(out[from], rng.Intn(n))
			}
		}
		component, emitted := stronglyConnected(out)

		reaches := func(from int) []bool {
			seen := make([]bool, n)
			for stack := []int{from}; len(stack) > 0; {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if !seen[c] {
					seen[c] = true
					stack = append(stack, out[c]...)
				}
			}
			return seen
		}
		members := 0
		for c, nodes := range emitted {
			members += len(nodes)
			for _, a := range nodes {
				require.Equal(t, c, component[a])
			}
		}
		require.Equal(t, n, members, "every node is in exactly one component")
		for a := 0; a < n; a++ {
			fromA := reaches(a)
			for b := 0; b < n; b++ {
				mutual := fromA[b] && reaches(b)[a]
				require.Equalf(t, mutual, component[a] == component[b], "round %d: %d and %d", round, a, b)
				if fromA[b] && !mutual {
					require.Lessf(t, component[b], component[a], "round %d: %d flows on to %d", round, a, b)
				}
			}
		}
	}
	component, emitted := stronglyConnected(nil)
	assert.Empty(t, component)
	assert.Empty(t, emitted)
}

// releaseRepositoryInputsByWalk is the closure as it was computed before the
// components were condensed: the group union, then one walk of the package
// graph per repository to the fixed point. It is kept as the definition
// releaseRepositoryInputs has to agree with.
func releaseRepositoryInputsByWalk(cp *computation) ([]string, map[string][]uint64) {
	if len(cp.histories) == 0 {
		return nil, nil
	}
	repositories := make([]string, 0, len(cp.histories))
	index := make(map[string]int, len(cp.histories))
	for _, history := range cp.histories {
		repositories = append(repositories, history.Name)
	}
	slices.Sort(repositories)
	for i, repository := range repositories {
		index[strings.ToLower(repository)] = i
	}
	wordCount := (len(repositories) + 63) / 64
	sets := make(map[string][]uint64, len(cp.order))
	groups := make(map[string][]string)
	groupSets := make(map[string][]uint64)
	dependents := make(map[string][]string, len(cp.order))
	for _, name := range cp.order {
		bits := make([]uint64, wordCount)
		for _, repository := range cp.repositoryReach[name] {
			if i, ok := index[strings.ToLower(repository)]; ok {
				bits[i/64] |= uint64(1) << uint(i%64)
			}
		}
		if cp.controlInputs[name] {
			if i, ok := index[strings.ToLower(cp.controlRepo)]; ok {
				bits[i/64] |= uint64(1) << uint(i%64)
			}
		}
		sets[name] = bits
		if pkg := cp.byName[name]; pkg != nil {
			if group := pkg.VersionGroupIdentity(); group != "" {
				groups[group] = append(groups[group], name)
				if groupSets[group] == nil {
					groupSets[group] = make([]uint64, wordCount)
				}
				for i, word := range bits {
					groupSets[group][i] |= word
				}
			}
		}
		seen := make(map[string]bool)
		for _, provider := range cp.providers[name] {
			if !seen[provider] {
				dependents[provider] = append(dependents[provider], name)
				seen[provider] = true
			}
		}
	}

	for group, members := range groups {
		for _, name := range members {
			for i, word := range groupSets[group] {
				sets[name][i] |= word
			}
		}
	}

	// Dependency and group edges can alternate (a group member can introduce
	// a provider input that changes a downstream group). Propagating one
	// repository bit at a time reaches the exact fixed point in
	// O(Q*(P+E+V)), where Q is repositories and V is shared-group membership,
	// without repeatedly scanning whole bitsets as individual inputs arrive.
	for repositoryIndex := range repositories {
		word := repositoryIndex / 64
		mask := uint64(1) << uint(repositoryIndex%64)
		queue := make([]string, 0, len(cp.order))
		seen := make(map[string]bool, len(cp.order))
		seenGroups := make(map[string]bool, len(groups))
		for _, name := range cp.order {
			if sets[name][word]&mask != 0 {
				seen[name] = true
				queue = append(queue, name)
			}
		}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			sets[name][word] |= mask
			for _, dependent := range dependents[name] {
				if !seen[dependent] {
					seen[dependent] = true
					queue = append(queue, dependent)
				}
			}
			pkg := cp.byName[name]
			if pkg == nil || pkg.VersionGroupIdentity() == "" {
				continue
			}
			group := pkg.VersionGroupIdentity()
			if seenGroups[group] {
				continue
			}
			seenGroups[group] = true
			for _, member := range groups[group] {
				if !seen[member] {
					seen[member] = true
					queue = append(queue, member)
				}
			}
		}
	}

	result := make(map[string][]uint64, len(cp.order))
	interned := make(map[string][]uint64)
	for _, name := range cp.order {
		values := sets[name]
		key := repositoryWordsKey(values)
		if shared, ok := interned[key]; ok {
			values = shared
		} else {
			interned[key] = values
		}
		result[name] = values
	}
	return repositories, result
}
