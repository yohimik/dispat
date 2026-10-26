//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowsDistributedNodeSlotsRefuseBeforeEffects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node-state")
	_, err := OpenNodeSlotAuthority(root, "node", 1)
	require.ErrorContains(t, err, "journal durability")
}
