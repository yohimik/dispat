// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyResultVerifyMissingIdentity       = ""
	choreographyResultVerifyNoOutputRoots         = 0
	choreographyResultVerifyNoCoordinatorError    = "execution: compiled result expectations have no coordinator"
	choreographyResultVerifyNoTaskError           = "execution: compiled result expectation has no task"
	choreographyResultVerifyWrongRunError         = "execution: result belongs to another pinned run"
	choreographyResultVerifyUnregisteredTaskError = "execution: result has no registered command frame"
	choreographyResultVerifyInvalidStatusError    = "execution: result has an invalid status"
	choreographyResultVerifyMissingOutputsError   = "execution: successful producer has no declared output set"
	choreographyResultVerifyUndeclaredOutputError = "execution: result carries undeclared outputs"
)

type choreographyResultExpectation struct {
	roots     []string
	platforms []string
	produces  bool
}

type choreographyResultRegistration struct {
	Task      string
	Roots     []string
	Platforms []string
	Produces  bool
}

func (c *Coordinator) registerChoreographyResult(registration choreographyResultRegistration) {
	c.choreographyRequests.Store(registration.Task, choreographyResultExpectation{
		roots:     append([]string(nil), registration.Roots...),
		platforms: append([]string(nil), registration.Platforms...),
		produces:  registration.Produces})
}

// RegisterCompiledChoreographyResults installs result expectations for every
// upfront descriptor before any peer can claim it. Executor drain order does
// not decide which ready task may return a Result.
func (c *Coordinator) RegisterCompiledChoreographyResults(
	templates []ChoreographyCompiledTemplate) error {
	if c == nil {
		return fmt.Errorf(choreographyResultVerifyNoCoordinatorError)
	}
	for _, template := range templates {
		if template.Task == choreographyResultVerifyMissingIdentity {
			return fmt.Errorf(choreographyResultVerifyNoTaskError)
		}
		c.registerChoreographyResult(choreographyResultRegistration{Task: template.Task,
			Roots: template.Outputs, Platforms: template.Platforms,
			Produces: len(template.Outputs) != choreographyResultVerifyNoOutputRoots})
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
		return fmt.Errorf(choreographyResultVerifyWrongRunError)
	}
	value, ok := c.choreographyRequests.Load(proposal.Task)
	if !ok {
		return fmt.Errorf(choreographyResultVerifyUnregisteredTaskError)
	}
	expect := value.(choreographyResultExpectation)
	if result.Status != StatusSucceeded && result.Status != StatusFailed && result.Status != StatusCancelled {
		return fmt.Errorf(choreographyResultVerifyInvalidStatusError)
	}
	if result.Outputs == nil {
		if expect.produces && result.Status == StatusSucceeded && len(expect.roots) > choreographyResultVerifyNoOutputRoots {
			return fmt.Errorf(choreographyResultVerifyMissingOutputsError)
		}
		return nil
	}
	if !expect.produces || len(expect.roots) == choreographyResultVerifyNoOutputRoots {
		return fmt.Errorf(choreographyResultVerifyUndeclaredOutputError)
	}
	_, err := ValidateOutputs(ctx, store, result.Outputs, OutputExpectation{
		Run: c.Run, PlanDigest: c.PlanDigest, Generation: c.Generation,
		Task: proposal.Task, Attempt: proposal.Attempt,
		Roots: expect.roots, Platforms: expect.platforms, Limits: c.Limits,
	})
	return err
}
