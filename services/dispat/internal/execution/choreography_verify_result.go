// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

type choreographyResultExpectation struct {
	roots     []string
	platforms []string
	produces  bool
}

func (c *Coordinator) registerChoreographyResult(task string, roots, platforms []string, produces bool) {
	c.choreographyRequests.Store(task, choreographyResultExpectation{
		roots: append([]string(nil), roots...), platforms: append([]string(nil), platforms...),
		produces: produces})
}

// RegisterCompiledChoreographyResults installs result expectations for every
// upfront descriptor before any peer can claim it. Executor drain order does
// not decide which ready task may return a Result.
func (c *Coordinator) RegisterCompiledChoreographyResults(
	templates []ChoreographyCompiledTemplate) error {
	if c == nil {
		return fmt.Errorf("execution: compiled result expectations have no coordinator")
	}
	for _, template := range templates {
		if template.Task == "" {
			return fmt.Errorf("execution: compiled result expectation has no task")
		}
		c.registerChoreographyResult(template.Task, template.Outputs,
			template.Platforms, len(template.Outputs) != 0)
	}
	return nil
}

// VerifyChoreographyResult is the first node's output gate before a signed
// Result becomes a durable receipt. The command frame registers its declared
// roots before offering the task. Installing/admitting the validated bytes is
// a later graph outcome, never a side effect of merely receiving a pack.
func (c *Coordinator) VerifyChoreographyResult(ctx context.Context,
	proposal ChoreographyResultProposal, result Result, store *gitx.LocalGitx) error {
	if c == nil || store == nil || proposal.Run != c.Run ||
		proposal.Generation != c.Generation || proposal.PlanDigest != c.PlanDigest {
		return fmt.Errorf("execution: result belongs to another pinned run")
	}
	value, ok := c.choreographyRequests.Load(proposal.Task)
	if !ok {
		return fmt.Errorf("execution: result has no registered command frame")
	}
	expect := value.(choreographyResultExpectation)
	if result.Status != StatusSucceeded && result.Status != StatusFailed && result.Status != StatusCancelled {
		return fmt.Errorf("execution: result has an invalid status")
	}
	if result.Outputs == nil {
		if expect.produces && result.Status == StatusSucceeded && len(expect.roots) > 0 {
			return fmt.Errorf("execution: successful producer has no declared output set")
		}
		return nil
	}
	if !expect.produces || len(expect.roots) == 0 {
		return fmt.Errorf("execution: result carries undeclared outputs")
	}
	_, err := ValidateOutputs(ctx, store, result.Outputs, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation,
		Task: proposal.Task, Attempt: proposal.Attempt,
		Roots: expect.roots, Platforms: expect.platforms, Limits: c.Limits,
	})
	return err
}
