// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	choreographyPickupTestRun       = "run"
	choreographyPickupTestTask      = "core:build"
	choreographyPickupTestNode      = "worker-a"
	choreographyPickupTestOtherNode = "worker-b"
	choreographyPickupTestBadID     = "not-a-pickup-id"
)

func TestChoreographyPickupBranchesBindUniquePhysicalIdentity(t *testing.T) {
	first := ChoreographyPickupIdentity{
		Run: choreographyPickupTestRun, Task: choreographyPickupTestTask,
		Node: choreographyPickupTestNode, PickupID: NewChoreographyPickupID(),
	}
	second := first
	second.PickupID = NewChoreographyPickupID()
	otherNode := first
	otherNode.Node = choreographyPickupTestOtherNode

	pending, err := FormatChoreographyPendingBranch(first)
	require.NoError(t, err)
	output, err := FormatChoreographyOutcomeBranch(first, StatusSucceeded)
	require.NoError(t, err)
	failed, err := FormatChoreographyOutcomeBranch(first, StatusFailed)
	require.NoError(t, err)
	cancelled, err := FormatChoreographyOutcomeBranch(first, StatusCancelled)
	require.NoError(t, err)
	require.NotEqual(t, pending, output)
	require.NotEqual(t, pending, failed)
	require.NotEqual(t, output, failed)
	require.Equal(t, failed, cancelled)
	require.True(t, strings.HasPrefix(pending, choreographyPickupPendingBranchPrefix))
	require.True(t, strings.HasPrefix(output, choreographyPickupOutputBranchPrefix))
	require.True(t, strings.HasPrefix(failed, choreographyPickupFailedBranchPrefix))

	otherPickupBranch, err := FormatChoreographyPendingBranch(second)
	require.NoError(t, err)
	otherNodeBranch, err := FormatChoreographyPendingBranch(otherNode)
	require.NoError(t, err)
	require.NotEqual(t, pending, otherPickupBranch)
	require.NotEqual(t, pending, otherNodeBranch)
	replayed, err := FormatChoreographyPendingBranch(first)
	require.NoError(t, err)
	require.Equal(t, pending, replayed)
}

func TestChoreographyPickupBranchRejectsUnboundIdentity(t *testing.T) {
	identity := ChoreographyPickupIdentity{
		Run: choreographyPickupTestRun, Task: choreographyPickupTestTask,
		Node: choreographyPickupTestNode, PickupID: choreographyPickupTestBadID,
	}
	_, err := FormatChoreographyPendingBranch(identity)
	require.Error(t, err)
	identity.PickupID = strings.ToUpper(NewChoreographyPickupID())
	_, err = FormatChoreographyPendingBranch(identity)
	require.Error(t, err)
	identity.PickupID = NewChoreographyPickupID()
	_, err = FormatChoreographyOutcomeBranch(identity, choreographyPickupMissingIdentity)
	require.Error(t, err)
}
