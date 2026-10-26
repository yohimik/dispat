package app

import (
	"fmt"
	"slices"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// sweepOutputAccumulator reproduces scriptWork.resolve's provider merge and
// Sequence.RunMergingOutputs' own-export merge. Finality, including a failed
// script, is the boundary: a failed attempt eligible for retry contributes
// nothing yet. The caller supplies only verified signed final Result exports;
// a failed frame may also contribute a typed declared path when its partial
// output manifest was captured and accepted. Typed paths are rehomed into
// this private plan only after that exact result is verified.
type sweepOutputAccumulator struct {
	plan     *plan.Plan
	covered  map[string]bool
	prepared map[string]bool
	settled  map[string]bool
}

func newSweepOutputAccumulator(pl *plan.Plan, covered []string) (*sweepOutputAccumulator, error) {
	if pl == nil {
		return nil, fmt.Errorf("execution: sweep output plan is absent")
	}
	a := &sweepOutputAccumulator{plan: pl,
		covered:  make(map[string]bool, len(covered)),
		prepared: make(map[string]bool, len(covered)),
		settled:  make(map[string]bool, len(covered))}
	for _, name := range covered {
		if name == "" || a.covered[name] || pl.Releases[name] == nil {
			return nil, fmt.Errorf("execution: sweep output names an invalid covered package")
		}
		a.covered[name] = true
	}
	return a, nil
}

// Prepare mirrors scriptWork.resolve before the selected script is looked
// up. Providers merge in name order and only covered providers contribute.
// A no-script or later skipped package still inherits these outputs.
func (a *sweepOutputAccumulator) Prepare(pkg string) ([]plan.Output, error) {
	if a == nil || !a.covered[pkg] {
		return nil, fmt.Errorf("execution: sweep output preparation names an uncovered package")
	}
	rel := a.plan.Releases[pkg]
	if !a.prepared[pkg] {
		providers := slices.Clone(a.plan.Providers[pkg])
		slices.Sort(providers)
		for _, provider := range providers {
			if a.covered[provider] && !a.settled[provider] {
				return nil, fmt.Errorf("execution: sweep provider %q has no accepted final outcome", provider)
			}
		}
		for _, provider := range providers {
			if !a.covered[provider] {
				continue
			}
			release.MergeOutputs(rel, a.plan.Releases[provider].Outputs)
		}
		a.prepared[pkg] = true
	}
	return slices.Clone(rel.Outputs), nil
}

// Finalize mirrors RunMergingOutputs even for a failed script: valid literal
// exports made before its failure remain visible under OnErrorContinue.
// A virtual no-script gate passes nil exports after Prepare.
func (a *sweepOutputAccumulator) Finalize(pkg string, own []plan.Output) error {
	if a == nil || !a.covered[pkg] || !a.prepared[pkg] || a.settled[pkg] {
		return fmt.Errorf("execution: sweep output finality is out of order")
	}
	release.MergeOutputs(a.plan.Releases[pkg], own)
	a.settled[pkg] = true
	return nil
}
