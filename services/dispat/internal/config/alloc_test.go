//go:build !race

package config

// The race detector adds its own allocations, so these budgets only hold in a
// normal build, hence the build tag.

import (
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestDiscoveryCheckAllocationBudget pins the allocations of the discovery
// checks that scale with packages, at 500 packages sharing one root-level
// setting. It is a regression gate: a check that goes back to one parse or one
// fold per pair of packages allocates hundreds of thousands of times here.
func TestDiscoveryCheckAllocationBudget(t *testing.T) {
	const packages = 500
	aliased := benchWorkspacePackages(packages, &model.Space{
		AliasTags: []model.AliasTag{{Format: "{name}-v{major}", Moving: true}},
	})
	cases := []struct {
		name   string
		budget float64
		run    func() error
	}{
		{
			name:   "checkAliasTagsAreWriteOnly",
			budget: 7000, // measured 6526: the renders, the index and the seen map, linear in packages
			run:    func() error { return checkAliasTagsAreWriteOnly(aliased) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			got := testing.AllocsPerRun(5, func() { err = tc.run() })
			if err != nil {
				t.Fatal(err)
			}
			if got > tc.budget {
				t.Errorf("%s allocates %.0f times at %d packages, budget %.0f", tc.name, got, packages, tc.budget)
			}
		})
	}
}
