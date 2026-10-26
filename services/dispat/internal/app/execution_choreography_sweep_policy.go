package app

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type sweepPolicyEdge struct {
	consumer string
	rank     int
}

type sweepPolicyBlocker struct {
	provider string
	rank     int
}

// sweepOutcomeEvaluator applies only the generic sweep's OnError rule. The
// release evaluator has different blocking and cause semantics. Provider
// order comes from the same plan list used by sweep.blocker; recording each
// direct edge once makes a settled failure O(outdegree) and a query O(1).
type sweepOutcomeEvaluator struct {
	onError string
	covered map[string]bool
	status  map[string]string
	from    map[string][]sweepPolicyEdge
	blocked map[string]sweepPolicyBlocker
}

func newSweepOutcomeEvaluator(pl *plan.Plan, covered []string, onError string) (*sweepOutcomeEvaluator, error) {
	if pl == nil || onError != OnErrorSkip && onError != OnErrorContinue {
		return nil, fmt.Errorf(choreographyIncompleteSweepOutcomePolicyErrorText)
	}
	e := &sweepOutcomeEvaluator{onError: onError,
		covered: make(map[string]bool, len(covered)), status: make(map[string]string, len(covered)),
		from: make(map[string][]sweepPolicyEdge), blocked: make(map[string]sweepPolicyBlocker)}
	for _, name := range covered {
		if name == choreographyAbsentIdentityText || e.covered[name] || pl.Releases[name] == nil {
			return nil, fmt.Errorf(choreographyInvalidSweepOutcomePackageErrorText)
		}
		e.covered[name] = choreographyKnownMemberState
	}
	for _, consumer := range covered {
		for rank, provider := range pl.Providers[consumer] {
			if e.covered[provider] {
				e.from[provider] = append(e.from[provider], sweepPolicyEdge{consumer: consumer, rank: rank})
			}
		}
	}
	return e, nil
}

func (e *sweepOutcomeEvaluator) ValidateOutcome(pkg, status string) error {
	if e == nil || !e.covered[pkg] {
		return fmt.Errorf(choreographyUncoveredSweepOutcomeErrorFormat, pkg)
	}
	switch status {
	case choreographyFailedOutcomeName, choreographySkippedOutcomeName, choreographyCancelledOutcomeName:
	default:
		return fmt.Errorf(choreographyInvalidSweepOutcomeErrorFormat, status)
	}
	if prior := e.status[pkg]; prior != choreographyAbsentIdentityText && prior != status {
		return fmt.Errorf(choreographyChangedSweepOutcomeErrorFormat, pkg)
	}
	return nil
}

func (e *sweepOutcomeEvaluator) ApplyOutcome(pkg, status string) error {
	if err := e.ValidateOutcome(pkg, status); err != nil {
		return err
	}
	if e.status[pkg] == status {
		return nil
	}
	e.status[pkg] = status
	if e.onError == OnErrorSkip && (status == choreographyFailedOutcomeName || status == choreographySkippedOutcomeName) {
		for _, edge := range e.from[pkg] {
			previous, exists := e.blocked[edge.consumer]
			if !exists || edge.rank < previous.rank {
				e.blocked[edge.consumer] = sweepPolicyBlocker{provider: pkg, rank: edge.rank}
			}
		}
	}
	return nil
}

func (e *sweepOutcomeEvaluator) ShouldSkip(pkg string) (bool, string, error) {
	if e == nil || !e.covered[pkg] {
		return choreographySkipDeclinedState, choreographyAbsentIdentityText, fmt.Errorf(choreographyUncoveredSweepSkipQueryErrorFormat, pkg)
	}
	if e.onError == OnErrorContinue {
		return choreographySkipDeclinedState, choreographyAbsentIdentityText, nil
	}
	blocker, exists := e.blocked[pkg]
	return exists, blocker.provider, nil
}
