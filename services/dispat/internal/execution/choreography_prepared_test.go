// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparedProviderPanicSettlesItsFuture(t *testing.T) {
	coordinator := &Coordinator{preparations: make(map[string]*preparation),
		dispatch: Dispatch{Prepare: func(string) (*PreparedProvider, error) {
			panic("invalid provider adapter")
		}}}
	first := coordinator.ensurePrepared(t.Context(), "base")
	require.ErrorContains(t, first, "preparing provider base panicked")
	second := coordinator.ensurePrepared(t.Context(), "base")
	require.EqualError(t, second, first.Error())
	require.Len(t, coordinator.PreparedRecords(), 1)
}
