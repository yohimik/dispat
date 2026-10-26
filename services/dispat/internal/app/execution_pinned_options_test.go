// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
)

func TestPinnedSweepDocumentsBindFailurePolicy(t *testing.T) {
	a := &App{root: t.TempDir()}
	for _, test := range []struct{ stated, resolved string }{
		{"", OnErrorSkip}, {OnErrorContinue, OnErrorContinue},
	} {
		selection, _, err := a.pinnedSweepDocuments("test", RunOptions{OnError: test.stated}, []string{"core"})
		require.NoError(t, err)
		var decoded pinnedPlanningSelection
		require.NoError(t, json.Unmarshal(selection, &decoded))
		require.Equal(t, test.resolved, decoded.OnError)
	}
}

func TestPinnedSelectionResolvesRelativeEntryDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	require.NoError(t, os.MkdirAll(root, 0o755))
	t.Chdir(root)
	a := &App{root: "."}

	selection, err := a.pinnedSelection("release", filter.Filter{Dir: "."})
	require.NoError(t, err)
	require.Equal(t, ".", selection.Dir)

	_, err = a.pinnedSelection("release", filter.Filter{Dir: base})
	require.ErrorContains(t, err, "outside the pinned workspace")
}
