// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyEventPageShrinksToEncodedByteLimit(t *testing.T) {
	requested := []int{}
	read := func(_ context.Context, cursor ChoreographyEventCursor, limit int) (ChoreographyEventPage, error) {
		requested = append(requested, limit)
		events := make([]ChoreographyEvent, limit)
		for i := range events {
			events[i] = ChoreographyEvent{Sequence: uint64(i + 1), Kind: ChoreographyTerminal,
				Task: strings.Repeat("t", 40000), TaskOID: "oid"}
		}
		return ChoreographyEventPage{Events: events,
			Cursor: ChoreographyEventCursor{OID: "cursor", Sequence: uint64(limit)},
			Head:   ChoreographyEventCursor{OID: "head", Sequence: 32}, More: limit < 32}, nil
	}
	document, err := encodeBoundedEventPage(t.Context(), read, ChoreographyEventCursor{}, 32)
	require.NoError(t, err)
	require.LessOrEqual(t, len(document), returnEventPageLimit)
	require.Equal(t, []int{32, 16}, requested)
	var page ChoreographyEventPage
	require.NoError(t, json.Unmarshal(document, &page))
	require.Len(t, page.Events, 16)
	require.True(t, page.More)
}
