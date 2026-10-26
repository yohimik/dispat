// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "context"

// InterpretedRun is derived independently from the authenticated pinned
// bootstrap. Task order and graph identity must match the shared compiler.
// No first-node assignment is used to choose a task in this mode.
type InterpretedRun struct {
	Binding       ChoreographyRunBinding
	Tasks         []ChoreographyTaskSpec
	OutcomePolicy ChoreographyOutcomePolicyx
	// Predecessors are exact signed terminal events in the shared graph's
	// direct predecessor order. The app verifies each admitted Result before
	// deriving a frame or any transitive exported value.
	PrepareReady func(context.Context, ChoreographyReadyTask, []ChoreographyEvent) (Assignment, string, error)
	Close        func() error
}

// PrepareInterpretedRun materializes the signed bootstrap in private source
// checkouts, recomputes plan and graph, and returns the exact ready frame.
// Eligibility uses the signed post-preflight roster from the retained ledger
// root, never ambient node config. The object-store path comes from the
// verified mailbox, not a moving URL.
type PrepareInterpretedRun func(context.Context, RunBootstrap,
	ChoreographyRuntimeRoster, string) (*InterpretedRun, error)
