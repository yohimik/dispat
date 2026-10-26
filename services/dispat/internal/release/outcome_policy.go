// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// PackageOutcome is a settled release status relevant to §19.3. A published
// package with an incomplete required record remains published but blocks
// consumers that require that record. A raw failed attempt is not an outcome.
type PackageOutcome string

const (
	OutcomePending       PackageOutcome = "pending"
	OutcomePublished     PackageOutcome = "published"
	OutcomeFailed        PackageOutcome = "failed"
	OutcomeSkipped       PackageOutcome = "skipped"
	OutcomeRecordBlocked PackageOutcome = "record-blocked"
	OutcomeCancelled     PackageOutcome = "cancelled"
)

func (o PackageOutcome) dead() bool {
	return o == OutcomeFailed || o == OutcomeSkipped || o == OutcomeRecordBlocked
}

func resultPackageOutcome(res *Result) PackageOutcome {
	if res == nil {
		return OutcomePending
	}
	if res.RecordBlocked {
		return OutcomeRecordBlocked
	}
	switch res.Status {
	case StatusPublished:
		return OutcomePublished
	case StatusFailed:
		return OutcomeFailed
	case StatusSkipped:
		return OutcomeSkipped
	case StatusCancelled:
		return OutcomeCancelled
	default:
		return OutcomePending
	}
}

// SkipPolicy is the one §19.3 predicate used by the compiled executor and an
// interpreting peer. Reached providers are the publication/blocking closure
// through unreleasing packages, obtained from the shared graph compiler.
type SkipPolicy struct {
	plan        *plan.Plan
	packageName string
	reached     []string
}

func newSkipPolicy(pkg string, p *plan.Plan, reached []string) SkipPolicy {
	return SkipPolicy{plan: p, packageName: pkg, reached: reached}
}

// Evaluate preserves shouldSkip's blocker priority and independent-cause
// rule. Pending providers are not inferred to have failed; required outcome
// waits in the graph determine when this question may be asked.
func (s SkipPolicy) Evaluate(outcome func(string) PackageOutcome) (bool, string) {
	return EvaluateCompiledPackagePolicy(compilePackagePolicy(s.packageName, s.plan, s.reached), outcome)
}

// OutcomeEvaluator retains signed package outcomes and lazily resolves only
// the skip policy of a package reaching admission. Applying one event is O(1)
// and never scans unrelated tasks; the reached-provider closure is charged
// separately from the linear task-DAG readiness kernel.
type OutcomeEvaluator struct {
	graph    *CompiledTaskGraph
	outcomes map[string]PackageOutcome
	policies map[string]SkipPolicy
	anyDead  bool
}

func (c *CompiledTaskGraph) NewOutcomeEvaluator() *OutcomeEvaluator {
	return &OutcomeEvaluator{graph: c,
		outcomes: make(map[string]PackageOutcome, len(c.plan.Releases)),
		policies: make(map[string]SkipPolicy)}
}

// Apply records one authenticated package-level settlement. Published may
// become record-blocked when a required durable record later fails; dead
// outcomes never become successful within the same run generation.
func (e *OutcomeEvaluator) ValidateOutcome(pkg, rawStatus string) error {
	status := PackageOutcome(rawStatus)
	if e == nil || e.graph == nil || e.graph.plan.Releases[pkg] == nil {
		return fmt.Errorf(releaseOutcomeUnplannedPackageErrorFormat, pkg)
	}
	switch status {
	case OutcomePublished, OutcomeFailed, OutcomeSkipped, OutcomeRecordBlocked, OutcomeCancelled:
	default:
		return fmt.Errorf(releaseOutcomeInvalidStatusErrorFormat, status)
	}
	prior := e.outcomes[pkg]
	if prior == status {
		return nil
	}
	if prior != releaseRuntimeAbsentIdentityText && prior != OutcomePending && !(prior == OutcomePublished && status == OutcomeRecordBlocked) {
		return fmt.Errorf(releaseOutcomeChangedStatusErrorFormat, pkg, prior, status)
	}
	return nil
}

func (e *OutcomeEvaluator) Apply(pkg string, status PackageOutcome) error {
	if err := e.ValidateOutcome(pkg, string(status)); err != nil {
		return err
	}
	if e.outcomes[pkg] == status {
		return nil
	}
	e.outcomes[pkg] = status
	if status.dead() {
		e.anyDead = releaseOutcomeDeadProviderState
	}
	return nil
}

// ApplyOutcome is the string-valued boundary used by the signed execution
// ledger. Validation remains in Apply, so a peer cannot silently accept a
// new outcome spelling as a successful release.
func (e *OutcomeEvaluator) ApplyOutcome(pkg, status string) error {
	return e.Apply(pkg, PackageOutcome(status))
}

func (e *OutcomeEvaluator) ShouldSkip(pkg string) (bool, string, error) {
	if e == nil || e.graph == nil || e.graph.plan.Releases[pkg] == nil {
		return releaseOutcomeSkipDeclinedState, releaseRuntimeAbsentIdentityText, fmt.Errorf(releaseOutcomeSkipUnplannedPackageErrorFormat, pkg)
	}
	// The ordinary successful path does not need a transitive provider walk.
	// Do not cache an empty closure that could become relevant after a failure.
	if !e.anyDead {
		return releaseOutcomeSkipDeclinedState, releaseRuntimeAbsentIdentityText, nil
	}
	policy, exists := e.policies[pkg]
	if !exists {
		policy = newSkipPolicy(pkg, e.graph.plan, e.graph.blocking.Indirect(pkg))
		e.policies[pkg] = policy
	}
	skip, blocker := policy.Evaluate(func(provider string) PackageOutcome { return e.outcomes[provider] })
	return skip, blocker, nil
}
