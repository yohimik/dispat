// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// ExecutionCycleError is an impossible order of the plan's actual stage and
// provider-outcome tasks. It is checked before any task or release effect.
type ExecutionCycleError struct{ Tasks []string }

func (e *ExecutionCycleError) Error() string {
	return fmt.Sprintf("E233: execution task graph has a cycle involving %s", strings.Join(e.Tasks, ", "))
}

func (*ExecutionCycleError) DiagnosticCode() string     { return plan.CodeStageWorkspaceCycle }
func (*ExecutionCycleError) DiagnosticCategory() string { return "execution-configuration" }

// ValidateExecutionGraph checks the exact compiled graph without consuming
// it. The extra outcome waits depend on this plan's releasing packages, so a
// workspace's earlier configuration graph check alone cannot decide them.
func ValidateExecutionGraph(p *plan.Plan) error {
	_, changed := newReleasingResults(p)
	if len(changed) == 0 {
		return nil
	}
	plans := newStagePlans(p, changed)
	sched, _ := (&Executor{}).buildTaskGraph(p, changed, plans)
	cyclic := sched.CycleNodes()
	if len(cyclic) == 0 {
		return nil
	}
	tasks := make([]string, 0, len(cyclic))
	for _, t := range cyclic {
		stage := t.kind.String()
		if t.name != "" {
			stage = t.name
		}
		tasks = append(tasks, t.pkg+":"+stage)
	}
	return &ExecutionCycleError{Tasks: tasks}
}
