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
	building := &discovery{pkgs: benchWorkspacePackages(packages, &model.Space{BuildOutputs: []string{"dist"}})}
	undeclared := &discovery{pkgs: benchWorkspacePackages(packages, &model.Space{})}
	sweeps := map[string][]string{"test": {"coverage", "reports/junit"}}
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
		{
			name:   "checkBuildOutputRoots",
			budget: 3200, // measured 2916: the roots, the overlap sort and one fold per package
			run:    building.checkBuildOutputRoots,
		},
		{
			name:   "checkRunOutputRoots",
			budget: 2700, // measured 2417: the build roots and one fold per package, shared by every sweep root
			run: func() error {
				return checkRunOutputRoots(sweeps, building.pkgs, "/Users/Bench/Repo")
			},
		},
		{
			name:   "checkBuildOutputRoots with no root declared",
			budget: 0, // the common workspace: no root, so nothing to index the packages for
			run:    undeclared.checkBuildOutputRoots,
		},
		{
			name:   "checkRunOutputRoots with no root declared",
			budget: 0,
			run: func() error {
				return checkRunOutputRoots(map[string][]string{"test": {}}, undeclared.pkgs, "/Users/Bench/Repo")
			},
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
